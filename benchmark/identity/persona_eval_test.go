// Package personas evaluates cross-meeting speaker-persona accuracy on real
// meeting audio (AMI corpus), using noto's production embedding + matching path.
//
// Ground truth: AMI ES2002 a/b/c/d are four separate sessions with the SAME
// four participants; the RTTM speaker labels are global participant IDs, so
// "did speaker X in meeting 1 get linked to the same profile in meeting 2?"
// has an unambiguous answer.
//
// Two experiments:
//
//	A (split): one meeting cut into N time chunks → cross-chunk linking
//	           (same session: an upper bound on what's achievable).
//	B (cross-session): enroll from session a, link sessions b/c/d
//	           (different recordings → the realistic, harder test).
//
// Assets are gated: run `python3 benchmark/identity/fetch.py` first, and install
// the model (tools/voiceprint or `noto speaker-model download`). Run with:
//
//	go test ./benchmark/identity/ -run Persona -v
package identity

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/speakers"
	"github.com/lukasstrickler/noto/internal/platform/providers/speaker"

	// Register the suite's -hours/-seed flags so the shared `go test ./benchmark/...`
	// invocation parses here too (this bench picks its own AMI sessions explicitly).
	_ "github.com/lukasstrickler/noto/benchmark/internal/sample"
)

type turn struct{ start, dur float64 }

func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// loadEmbedder loads the production ECAPA embedder, skipping if assets absent.
func loadEmbedder(t *testing.T) *speaker.LocalEmbedder {
	t.Helper()
	root := repoRoot()
	model := envOr("ECAPA_MODEL", filepath.Join(root, "tools/voiceprint/models/ecapa512.onnx"))
	lib := envOr("ORT_LIB", filepath.Join(root, "tools/voiceprint/onnxruntime/lib/libonnxruntime.so"))
	ffmpeg := envOr("FFMPEG", filepath.Join(root, "tools/voiceprint/bin/ffmpeg"))
	if !exists(model) || !exists(lib) {
		t.Skip("voiceprint model/lib missing (noto speaker-model download)")
	}
	eng, err := speaker.NewECAPA(model, lib)
	if err != nil {
		t.Fatalf("NewECAPA: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	return speaker.NewLocalEmbedder(eng, ffmpeg)
}

func amiDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(repoRoot(), "benchmark/identity/ami")
	if !exists(filepath.Join(dir, "ES2002a.rttm")) {
		t.Skip("AMI assets missing (run: python3 benchmark/identity/fetch.py)")
	}
	return dir
}

// parseRTTM returns speaker -> turns and the meeting end time.
func parseRTTM(t *testing.T, path string) (map[string][]turn, float64) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open rttm: %v", err)
	}
	defer f.Close()
	out := map[string][]turn{}
	var end float64
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 || fields[0] != "SPEAKER" {
			continue
		}
		st, _ := strconv.ParseFloat(fields[3], 64)
		du, _ := strconv.ParseFloat(fields[4], 64)
		spk := fields[7]
		out[spk] = append(out[spk], turn{st, du})
		if st+du > end {
			end = st + du
		}
	}
	return out, end
}

// embedPersonas runs the production embedder over the turns falling in [t0,t1),
// returning one L2-normalized persona vector per speaker.
func embedPersonas(t *testing.T, emb *speaker.LocalEmbedder, wavPath string, turns map[string][]turn, t0, t1 float64) map[string][]float64 {
	t.Helper()
	data, err := os.ReadFile(wavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	tr := &artifacts.Transcript{MeetingID: "m"}
	for spk, ts := range turns {
		used := false
		for i, tn := range ts {
			s, e := math.Max(tn.start, t0), math.Min(tn.start+tn.dur, t1)
			if e-s <= 0 {
				continue
			}
			tr.Segments = append(tr.Segments, artifacts.Segment{
				ID: fmt.Sprintf("%s_%d", spk, i), SpeakerID: spk,
				StartSeconds: s, EndSeconds: e,
			})
			used = true
		}
		if used {
			tr.Speakers = append(tr.Speakers, artifacts.Speaker{ID: spk, ProviderLabel: spk})
		}
	}
	out, err := emb.EmbedSpeakers(context.Background(), data, tr)
	if err != nil {
		t.Fatalf("EmbedSpeakers: %v", err)
	}
	return out
}

func cos(a, b []float64) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	return dot / (math.Sqrt(na)*math.Sqrt(nb) + 1e-12)
}

func sortedKeys(m map[string][]float64) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// linkReport scores each persona in `query` against the enrolled gallery and
// logs rank-1 accuracy, score separation, and decisions at two threshold pairs.
type stats struct {
	total, rank1   int
	sameSum        float64
	impostorSum    float64
	maxImpostor    float64 // worst cross-speaker score seen → bounds false-link risk at any gallery size
	minSame        float64 // weakest true-link score → bounds missed links
	minMargin      float64
	autoShip, pend int // shipped thresholds 0.70/0.55: correct auto / pending
	missShip       int // would mint a NEW profile despite being an enrolled person
	falseShip      int // an impostor scored >= pending (false-link risk)
	autoCal, pendC int // calibrated 0.58/0.50
	missCal        int
	falseCal       int
}

func score(query, gallery map[string][]float64, st *stats, t *testing.T, label string) {
	galKeys := sortedKeys(gallery)
	st.minMargin = math.Inf(1)
	st.minSame = math.Inf(1)
	st.maxImpostor = math.Inf(-1)
	for _, spk := range sortedKeys(query) {
		if _, ok := gallery[spk]; !ok {
			continue // not enrolled; can't be a cross-meeting link
		}
		qv := query[spk]
		best, bestScore := "", -2.0
		bestImpostor := -2.0
		for _, g := range galKeys {
			s := cos(qv, gallery[g])
			if s > bestScore {
				bestScore, best = s, g
			}
			if g != spk && s > bestImpostor {
				bestImpostor = s
			}
		}
		same := cos(qv, gallery[spk])
		st.total++
		if best == spk {
			st.rank1++
		}
		st.sameSum += same
		st.impostorSum += bestImpostor
		if bestImpostor > st.maxImpostor {
			st.maxImpostor = bestImpostor
		}
		if same < st.minSame {
			st.minSame = same
		}
		if m := same - bestImpostor; m < st.minMargin {
			st.minMargin = m
		}
		// shipped 0.70 / 0.55 (constants in core/speakers)
		switch {
		case same >= speakers.AutoThreshold:
			st.autoShip++
		case same >= speakers.PendingThreshold:
			st.pend++
		default:
			st.missShip++
		}
		if bestImpostor >= speakers.PendingThreshold {
			st.falseShip++
		}
		// calibrated 0.58 / 0.50 (precision sweep)
		switch {
		case same >= 0.58:
			st.autoCal++
		case same >= 0.50:
			st.pendC++
		default:
			st.missCal++
		}
		if bestImpostor >= 0.50 {
			st.falseCal++
		}
		t.Logf("    %-6s same=%.3f bestImpostor=%.3f -> rank1 %s (best=%s)",
			spk, same, bestImpostor, map[bool]string{true: "OK", false: "WRONG"}[best == spk], best)
	}
}

func (s stats) log(t *testing.T, title string) {
	if s.total == 0 {
		t.Logf("%s: no comparable personas", title)
		return
	}
	t.Logf("%s", title)
	t.Logf("  rank-1 identification : %d/%d = %.1f%%", s.rank1, s.total, 100*float64(s.rank1)/float64(s.total))
	t.Logf("  mean cosine same/impostor : %.3f / %.3f  (margin min %.3f, avg %.3f)",
		s.sameSum/float64(s.total), s.impostorSum/float64(s.total), s.minMargin,
		(s.sameSum-s.impostorSum)/float64(s.total))
	t.Logf("  worst case: weakest true-link %.3f, strongest impostor %.3f  (impostor < 0.55 ⇒ no false link at any roster size)",
		s.minSame, s.maxImpostor)
	t.Logf("  shipped  0.70/0.55: auto-link %d, pending %d, missed(new) %d | false-link risk %d",
		s.autoShip, s.pend, s.missShip, s.falseShip)
	t.Logf("  calibr.  0.58/0.50: auto-link %d, pending %d, missed(new) %d | false-link risk %d",
		s.autoCal, s.pendC, s.missCal, s.falseCal)
}

func TestPersona_CrossSession(t *testing.T) {
	emb := loadEmbedder(t)
	dir := amiDir(t)
	sessions := []string{"ES2002a", "ES2002b", "ES2002c", "ES2002d"}

	t.Log("enrolling personas from ES2002a (first meeting) ...")
	turnsA, _ := parseRTTM(t, filepath.Join(dir, "ES2002a.rttm"))
	enrolled := embedPersonas(t, emb, filepath.Join(dir, "ES2002a.wav"), turnsA, 0, 1e9)
	t.Logf("enrolled %d personas: %v", len(enrolled), sortedKeys(enrolled))

	var agg stats
	agg.minMargin = math.Inf(1)
	agg.maxImpostor = math.Inf(-1)
	agg.minSame = math.Inf(1)
	for _, s := range sessions[1:] {
		turns, _ := parseRTTM(t, filepath.Join(dir, s+".rttm"))
		q := embedPersonas(t, emb, filepath.Join(dir, s+".wav"), turns, 0, 1e9)
		t.Logf("--- %s vs enrolled (production matcher decisions below) ---", s)
		// show what the SHIPPED matcher actually decides, profile-resolved
		cands := make([]speakers.Candidate, 0, len(enrolled))
		for _, spk := range sortedKeys(enrolled) {
			cands = append(cands, speakers.Candidate{ProfileID: spk, Name: spk, Centroid: enrolled[spk]})
		}
		var sst stats
		score(q, enrolled, &sst, t, s)
		for _, spk := range sortedKeys(q) {
			if _, ok := enrolled[spk]; !ok {
				continue
			}
			dec, err := speakers.Match(q[spk], cands)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			t.Logf("    matcher: %-6s -> %-6s status=%-7s score=%.3f %s",
				spk, dec.ProfileID, dec.Status, dec.Score,
				map[bool]string{true: "✓", false: "✗ WRONG-LINK"}[dec.ProfileID == spk])
		}
		sst.log(t, "  "+s+" summary")
		// fold into aggregate
		agg.total += sst.total
		agg.rank1 += sst.rank1
		agg.sameSum += sst.sameSum
		agg.impostorSum += sst.impostorSum
		agg.minMargin = math.Min(agg.minMargin, sst.minMargin)
		agg.maxImpostor = math.Max(agg.maxImpostor, sst.maxImpostor)
		agg.minSame = math.Min(agg.minSame, sst.minSame)
		agg.autoShip += sst.autoShip
		agg.pend += sst.pend
		agg.missShip += sst.missShip
		agg.falseShip += sst.falseShip
		agg.autoCal += sst.autoCal
		agg.pendC += sst.pendC
		agg.missCal += sst.missCal
		agg.falseCal += sst.falseCal
	}
	t.Log("=========================================================")
	agg.log(t, "CROSS-SESSION TOTAL (enroll a, link b+c+d)")
	t.Log("=========================================================")

	if agg.total == 0 {
		t.Fatal("no personas compared")
	}
	if acc := float64(agg.rank1) / float64(agg.total); acc < 0.75 {
		t.Fatalf("cross-session rank-1 accuracy %.1f%% below floor 75%%", 100*acc)
	}
	if agg.falseShip > 0 {
		t.Logf("NOTE: %d impostor pair(s) scored >= shipped pending 0.55", agg.falseShip)
	}
}

func TestPersona_SplitMeeting(t *testing.T) {
	emb := loadEmbedder(t)
	dir := amiDir(t)
	meeting := envOr("PERSONA_SPLIT", "ES2002d") // longest session by default
	turns, end := parseRTTM(t, filepath.Join(dir, meeting+".rttm"))
	wav := filepath.Join(dir, meeting+".wav")
	const chunks = 3
	w := end / chunks
	t.Logf("splitting %s (%.0fs) into %d chunks of %.0fs", meeting, end, chunks, w)

	personas := make([]map[string][]float64, chunks)
	for c := 0; c < chunks; c++ {
		personas[c] = embedPersonas(t, emb, wav, turns, float64(c)*w, float64(c+1)*w)
		t.Logf("  chunk %d personas: %v", c, sortedKeys(personas[c]))
	}
	var agg stats
	agg.minMargin = math.Inf(1)
	agg.maxImpostor = math.Inf(-1)
	agg.minSame = math.Inf(1)
	for c := 1; c < chunks; c++ {
		t.Logf("--- chunk %d vs chunk 0 ---", c)
		var sst stats
		score(personas[c], personas[0], &sst, t, fmt.Sprintf("chunk%d", c))
		sst.log(t, fmt.Sprintf("  chunk %d summary", c))
		agg.total += sst.total
		agg.rank1 += sst.rank1
		agg.sameSum += sst.sameSum
		agg.impostorSum += sst.impostorSum
		agg.minMargin = math.Min(agg.minMargin, sst.minMargin)
		agg.maxImpostor = math.Max(agg.maxImpostor, sst.maxImpostor)
		agg.minSame = math.Min(agg.minSame, sst.minSame)
		agg.autoShip += sst.autoShip
		agg.pend += sst.pend
		agg.missShip += sst.missShip
		agg.falseShip += sst.falseShip
		agg.autoCal += sst.autoCal
		agg.pendC += sst.pendC
		agg.missCal += sst.missCal
		agg.falseCal += sst.falseCal
	}
	agg.log(t, "SPLIT-MEETING TOTAL (same session, upper bound)")
	if agg.total == 0 {
		t.Fatal("no personas compared")
	}
}

// ---- deep benchmark over the curated ~50-speaker dataset.json ----

type dsMeeting struct {
	ID       string   `json:"id"`
	Speakers []string `json:"speakers"`
}
type dataset struct {
	Name     string      `json:"name"`
	Speakers []string    `json:"speakers"`
	Meetings []dsMeeting `json:"meetings"`
}

func loadDataset(t *testing.T) dataset {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(), "benchmark/identity/dataset.json"))
	if err != nil {
		t.Skip("dataset.json missing (run: python3 benchmark/identity/build_dataset.py)")
	}
	var ds dataset
	if err := json.Unmarshal(raw, &ds); err != nil {
		t.Fatalf("dataset.json: %v", err)
	}
	return ds
}

// frac returns the fraction of xs satisfying pred.
func frac(xs []float64, pred func(float64) bool) float64 {
	if len(xs) == 0 {
		return 0
	}
	n := 0
	for _, x := range xs {
		if pred(x) {
			n++
		}
	}
	return float64(n) / float64(len(xs))
}

// eer sweeps every observed score as a threshold and returns the equal-error
// rate (where FAR==FRR) and that threshold.
func eer(genuine, impostor []float64) (float64, float64) {
	cands := append(append([]float64{}, genuine...), impostor...)
	sort.Float64s(cands)
	bestGap, eerVal, thr := math.Inf(1), 1.0, 0.0
	for _, c := range cands {
		far := frac(impostor, func(s float64) bool { return s >= c })
		frr := frac(genuine, func(s float64) bool { return s < c })
		if g := math.Abs(far - frr); g < bestGap {
			bestGap, eerVal, thr = g, (far+frr)/2, c
		}
	}
	return eerVal, thr
}

// TestPersona_Benchmark50 runs identification + verification across the full
// roster: enroll each speaker from their first meeting, then test every other
// meeting they appear in against the whole gallery.
func TestPersona_Benchmark50(t *testing.T) {
	if os.Getenv("PERSONA_DEEP") == "" {
		t.Skip("deep benchmark (~200s, 39 meeting decodes) — set PERSONA_DEEP=1 to run")
	}
	emb := loadEmbedder(t)
	dir := amiDir(t)
	ds := loadDataset(t)
	roster := map[string]bool{}
	for _, s := range ds.Speakers {
		roster[s] = true
	}

	// embed each available meeting once (one wav decode per meeting)
	type pv struct {
		meeting string
		vec     []float64
	}
	bySpeaker := map[string][]pv{}
	meetings := 0
	for _, m := range ds.Meetings {
		wav := filepath.Join(dir, m.ID+".wav")
		rttm := filepath.Join(dir, m.ID+".rttm")
		if !exists(wav) || !exists(rttm) {
			continue
		}
		turns, _ := parseRTTM(t, rttm)
		personas := embedPersonas(t, emb, wav, turns, 0, 1e9)
		for spk, v := range personas {
			if roster[spk] {
				bySpeaker[spk] = append(bySpeaker[spk], pv{m.ID, v})
			}
		}
		meetings++
	}
	if meetings == 0 {
		t.Skip("no dataset media present (run: python3 benchmark/identity/fetch.py)")
	}

	// gallery = first meeting per speaker (single-meeting enrollment).
	gallery := map[string][]float64{}
	galKeys := []string{}
	tests := []pv{}
	testSpk := []string{}
	for spk, pvs := range bySpeaker {
		if len(pvs) == 0 {
			continue
		}
		gallery[spk] = pvs[0].vec
		galKeys = append(galKeys, spk)
		for _, p := range pvs[1:] {
			tests = append(tests, p)
			testSpk = append(testSpk, spk)
		}
	}
	sort.Strings(galKeys)
	t.Logf("dataset %q: %d meetings present, %d enrolled speakers, %d test personas",
		ds.Name, meetings, len(gallery), len(tests))
	if len(tests) == 0 {
		t.Skip("need speakers with >=2 meetings present")
	}

	// identification (closed-set) + verification scores
	var rank1, rank5 int
	var genuine, impostor []float64
	worstGenuine, bestImpostor := math.Inf(1), math.Inf(-1)
	for i, tp := range tests {
		true_ := testSpk[i]
		type sc struct {
			spk string
			s   float64
		}
		scores := make([]sc, 0, len(galKeys))
		for _, g := range galKeys {
			s := cos(tp.vec, gallery[g])
			scores = append(scores, sc{g, s})
			if g == true_ {
				genuine = append(genuine, s)
				if s < worstGenuine {
					worstGenuine = s
				}
			} else {
				impostor = append(impostor, s)
				if s > bestImpostor {
					bestImpostor = s
				}
			}
		}
		sort.Slice(scores, func(a, b int) bool { return scores[a].s > scores[b].s })
		if scores[0].spk == true_ {
			rank1++
		}
		for k := 0; k < 5 && k < len(scores); k++ {
			if scores[k].spk == true_ {
				rank5++
				break
			}
		}
	}
	n := float64(len(tests))
	eerVal, eerThr := eer(genuine, impostor)

	t.Log("=========================================================")
	t.Logf("DEEP BENCHMARK — %d-speaker gallery, AMI Mix-Headset", len(gallery))
	t.Log("=========================================================")
	t.Logf("identification: rank-1 %d/%d = %.1f%%  rank-5 %.1f%%",
		rank1, len(tests), 100*float64(rank1)/n, 100*float64(rank5)/n)
	t.Logf("verification : EER %.2f%% @thr %.3f  (%d genuine, %d impostor pairs)",
		100*eerVal, eerThr, len(genuine), len(impostor))
	t.Logf("score sep    : genuine min %.3f, impostor max %.3f", worstGenuine, bestImpostor)
	for _, op := range []struct {
		name       string
		auto, pend float64
	}{{"shipped", speakers.AutoThreshold, speakers.PendingThreshold}, {"calibrated", 0.58, 0.50}} {
		far := frac(impostor, func(s float64) bool { return s >= op.pend })     // any impostor reaching pending = false-link risk
		farAuto := frac(impostor, func(s float64) bool { return s >= op.auto }) // false auto-merge
		autoOK := frac(genuine, func(s float64) bool { return s >= op.auto })
		pendOK := frac(genuine, func(s float64) bool { return s >= op.pend })
		t.Logf("  %-10s auto≥%.2f/pend≥%.2f: true-link auto %.1f%% / surfaced %.1f%% | impostor≥pend %.2f%%, ≥auto %.2f%%",
			op.name, op.auto, op.pend, 100*autoOK, 100*pendOK, 100*far, 100*farAuto)
	}

	if acc := float64(rank1) / n; acc < 0.80 {
		t.Fatalf("rank-1 %.1f%% below floor 80%% on %d-speaker gallery", 100*acc, len(gallery))
	}
}

// TestPersona_Stress is the failure-focused review: it embeds the dataset once,
// then probes the conditions the happy-path benchmark hides — open-set
// rejection (strangers must NOT match), short-speech degradation, and the
// hardest confusable pairs / weakest true-links.
func TestPersona_Stress(t *testing.T) {
	if os.Getenv("PERSONA_DEEP") == "" {
		t.Skip("stress review (~200s) — set PERSONA_DEEP=1 to run")
	}
	emb := loadEmbedder(t)
	dir := amiDir(t)
	ds := loadDataset(t)
	roster := map[string]bool{}
	for _, s := range ds.Speakers {
		roster[s] = true
	}
	type pv struct {
		meeting string
		vec     []float64
		speech  float64 // ground-truth speech seconds for this speaker in this meeting
	}
	bySpeaker := map[string][]pv{}
	meetings := 0
	for _, m := range ds.Meetings {
		wav := filepath.Join(dir, m.ID+".wav")
		rttm := filepath.Join(dir, m.ID+".rttm")
		if !exists(wav) || !exists(rttm) {
			continue
		}
		turns, _ := parseRTTM(t, rttm)
		personas := embedPersonas(t, emb, wav, turns, 0, 1e9)
		for spk, v := range personas {
			if !roster[spk] {
				continue
			}
			var sp float64
			for _, tn := range turns[spk] {
				sp += tn.dur
			}
			bySpeaker[spk] = append(bySpeaker[spk], pv{m.ID, v, sp})
		}
		meetings++
	}
	if meetings == 0 {
		t.Skip("no dataset media present (run: python3 benchmark/identity/fetch.py)")
	}
	speakers_ := make([]string, 0, len(bySpeaker))
	for s := range bySpeaker {
		speakers_ = append(speakers_, s)
	}
	sort.Strings(speakers_)

	t.Log("=========================================================")
	t.Logf("STRESS / FAILURE REVIEW — %d speakers, %d meetings", len(speakers_), meetings)
	t.Log("=========================================================")

	// ---- (1) OPEN-SET: half enrolled, half strangers; strangers MUST be rejected.
	var enrolledSpk, unknownSpk []string
	for i, s := range speakers_ {
		if i%2 == 0 {
			enrolledSpk = append(enrolledSpk, s)
		} else {
			unknownSpk = append(unknownSpk, s)
		}
	}
	gallery := map[string][]float64{}
	for _, s := range enrolledSpk {
		gallery[s] = bySpeaker[s][0].vec // enroll from first meeting
	}
	bestMatch := func(v []float64) (string, float64) {
		bs, bv := "", -2.0
		for g, c := range gallery {
			if s := cos(v, c); s > bv {
				bs, bv = g, s
			}
		}
		return bs, bv
	}
	// enrolled members: their later meetings should accept to the right profile
	var knownTests, knownAcceptPend, knownAcceptAuto, knownCorrect int
	for _, s := range enrolledSpk {
		for _, p := range bySpeaker[s][1:] {
			knownTests++
			who, sc := bestMatch(p.vec)
			if sc >= speakers.PendingThreshold {
				knownAcceptPend++
				if who == s {
					knownCorrect++
				}
			}
			if sc >= speakers.AutoThreshold && who == s {
				knownAcceptAuto++
			}
		}
	}
	// strangers: ALL their meetings should be rejected (no match >= pending)
	var strangerTests, faPend, faAuto int
	for _, s := range unknownSpk {
		for _, p := range bySpeaker[s] {
			strangerTests++
			_, sc := bestMatch(p.vec)
			if sc >= speakers.PendingThreshold {
				faPend++
			}
			if sc >= speakers.AutoThreshold {
				faAuto++
			}
		}
	}
	t.Logf("(1) OPEN-SET (%d enrolled, %d strangers):", len(enrolledSpk), len(unknownSpk))
	t.Logf("    known    : %d tests, accepted@pend %d (correct %d), auto-correct %d  -> miss %d",
		knownTests, knownAcceptPend, knownCorrect, knownAcceptAuto, knownTests-knownAcceptPend)
	t.Logf("    strangers: %d tests, FALSE-ACCEPT @pend(0.55) %d (%.2f%%), @auto(0.70) %d (%.2f%%)",
		strangerTests, faPend, 100*float64(faPend)/float64(strangerTests),
		faAuto, 100*float64(faAuto)/float64(strangerTests))

	// ---- (2) SHORT-SPEECH DEGRADATION: closed-set rank-1 + mean genuine by speech budget.
	full := map[string][]float64{}
	for _, s := range speakers_ {
		full[s] = bySpeaker[s][0].vec
	}
	type bucket struct {
		lo, hi float64
		n, ok  int
		genSum float64
	}
	buckets := []*bucket{{0, 5, 0, 0, 0}, {5, 15, 0, 0, 0}, {15, 45, 0, 0, 0}, {45, 1e9, 0, 0, 0}}
	for _, s := range speakers_ {
		for _, p := range bySpeaker[s][1:] {
			best, bv := "", -2.0
			for g, c := range full {
				if sc := cos(p.vec, c); sc > bv {
					best, bv = g, sc
				}
			}
			gen := cos(p.vec, full[s])
			for _, b := range buckets {
				if p.speech >= b.lo && p.speech < b.hi {
					b.n++
					b.genSum += gen
					if best == s {
						b.ok++
					}
				}
			}
		}
	}
	t.Log("(2) SHORT-SPEECH DEGRADATION (closed-set, 52-way gallery, by test-meeting speech):")
	for _, b := range buckets {
		if b.n == 0 {
			continue
		}
		hi := "+"
		if b.hi < 1e8 {
			hi = strconv.Itoa(int(b.hi))
		}
		t.Logf("    %4.0f-%-4s s speech: rank-1 %d/%d = %5.1f%%  mean-genuine %.3f",
			b.lo, hi, b.ok, b.n, 100*float64(b.ok)/float64(b.n), b.genSum/float64(b.n))
	}
	if buckets[0].n+buckets[1].n == 0 {
		t.Log("    NOTE: no test persona under 15s — AMI scenario participants all speak")
		t.Log("    minutes, so the short-speech failure mode is NOT exercised by this corpus.")
	}

	// ---- (3) HARDEST PAIRS + WEAKEST TRUE-LINKS (named).
	type pair struct {
		a, b string
		s    float64
	}
	var imps []pair
	var gens []pair
	for _, s := range speakers_ {
		for _, p := range bySpeaker[s][1:] {
			gens = append(gens, pair{s, p.meeting, cos(p.vec, full[s])})
			for _, g := range speakers_ {
				if g != s {
					imps = append(imps, pair{s + "/" + p.meeting, g, cos(p.vec, full[g])})
				}
			}
		}
	}
	sort.Slice(imps, func(i, j int) bool { return imps[i].s > imps[j].s })
	sort.Slice(gens, func(i, j int) bool { return gens[i].s < gens[j].s })
	t.Log("(3) HARDEST IMPOSTOR PAIRS (highest wrong-speaker cosine):")
	for i := 0; i < 6 && i < len(imps); i++ {
		flag := ""
		if imps[i].s >= speakers.PendingThreshold {
			flag = "  <- ≥pending (false suggestion)"
		}
		t.Logf("    %-22s vs %-8s = %.3f%s", imps[i].a, imps[i].b, imps[i].s, flag)
	}
	t.Log("    WEAKEST TRUE-LINKS (lowest same-speaker cosine):")
	for i := 0; i < 6 && i < len(gens); i++ {
		flag := ""
		if gens[i].s < speakers.PendingThreshold {
			flag = "  <- BELOW pending (missed link!)"
		}
		t.Logf("    %-8s %-8s = %.3f%s", gens[i].a, gens[i].b, gens[i].s, flag)
	}

	// ---- (4) DECISION LAYER: plain Match vs MatchConfident (margin gate).
	// The gate downgrades an auto-merge to pending when the top-2 profiles are
	// within DefaultAutoMargin. This measures its cost (good auto-links it now
	// asks to confirm) vs benefit (wrong auto-merges it prevents), on the live
	// matcher path — not raw cosines.
	cands := make([]speakers.Candidate, 0, len(full))
	for _, s := range speakers_ {
		cands = append(cands, speakers.Candidate{ProfileID: s, Name: s, Centroid: full[s]})
	}
	var (
		plainAutoOK, plainAutoBad int // plain Match auto-confirms (right / wrong profile)
		confAutoOK, confAutoBad   int // MatchConfident auto-confirms
		costDowngrade             int // correct auto under plain → pending under MatchConfident (the price)
		benefitSaved              int // wrong auto under plain → not-auto under MatchConfident (the win)
		genTests                  int
	)
	for _, s := range speakers_ {
		for _, p := range bySpeaker[s][1:] {
			genTests++
			pl, err := speakers.Match(p.vec, cands)
			if err != nil {
				t.Fatalf("Match: %v", err)
			}
			cf, err := speakers.MatchConfident(p.vec, cands)
			if err != nil {
				t.Fatalf("MatchConfident: %v", err)
			}
			plAuto := pl.Status == speakers.StatusAuto
			cfAuto := cf.Status == speakers.StatusAuto
			plRight := plAuto && pl.ProfileID == s
			plWrong := plAuto && pl.ProfileID != s
			if plRight {
				plainAutoOK++
			}
			if plWrong {
				plainAutoBad++
			}
			if cfAuto && cf.ProfileID == s {
				confAutoOK++
			}
			if cfAuto && cf.ProfileID != s {
				confAutoBad++
			}
			if plRight && !cfAuto {
				costDowngrade++
			}
			if plWrong && !cfAuto {
				benefitSaved++
			}
		}
	}
	// strangers vs the REAL open-set gallery (the enrolled half) — a stranger is
	// genuinely absent here, so this is the honest false-accept test. The margin
	// gate only downgrades auto→pending, so it can lower silent merges but cannot
	// reduce pending-band false suggestions (that needs a higher pending floor).
	galCands := make([]speakers.Candidate, 0, len(gallery))
	for _, s := range enrolledSpk {
		galCands = append(galCands, speakers.Candidate{ProfileID: s, Name: s, Centroid: gallery[s]})
	}
	var strAutoPlain, strAutoConf, strPendPlain, strPendConf int
	for _, s := range unknownSpk {
		for _, p := range bySpeaker[s] {
			pl, _ := speakers.Match(p.vec, galCands)
			cf, _ := speakers.MatchConfident(p.vec, galCands)
			if pl.Status == speakers.StatusAuto {
				strAutoPlain++
			}
			if cf.Status == speakers.StatusAuto {
				strAutoConf++
			}
			if pl.Status == speakers.StatusPending {
				strPendPlain++
			}
			if cf.Status == speakers.StatusPending {
				strPendConf++
			}
		}
	}
	t.Logf("(4) DECISION LAYER — plain Match vs MatchConfident over %d genuine tests, full %d-profile gallery:", genTests, len(cands))
	t.Logf("    plain Match     : auto-correct %d, auto-WRONG %d", plainAutoOK, plainAutoBad)
	t.Logf("    MatchConfident  : auto-correct %d, auto-WRONG %d", confAutoOK, confAutoBad)
	t.Logf("    margin gate cost: %d correct auto-links downgraded to pending (%.2f%% of genuine)",
		costDowngrade, 100*float64(costDowngrade)/float64(genTests))
	t.Logf("    margin gate win : %d wrong auto-merges prevented", benefitSaved)
	t.Logf("    open-set strangers (%d tests vs %d-profile half-gallery):", strangerTests, len(galCands))
	t.Logf("      auto-merge   : plain %d, confident %d  (silent wrong merges)", strAutoPlain, strAutoConf)
	t.Logf("      pending sugg.: plain %d, confident %d  (margin gate cannot lower this)", strPendPlain, strPendConf)
}

// ---- harder-condition harness: short speech, diarization noise, narrowband ----

type meetingVec struct {
	meeting string
	vec     []float64
}

func resolveFfmpeg() string {
	root := repoRoot()
	c := envOr("FFMPEG", filepath.Join(root, "tools/voiceprint/bin/ffmpeg"))
	if exists(c) {
		return c
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p
	}
	return ""
}

// embedTurnsBytes embeds the full-range turns from in-memory audio bytes.
func embedTurnsBytes(t *testing.T, emb *speaker.LocalEmbedder, data []byte, turns map[string][]turn) map[string][]float64 {
	t.Helper()
	tr := &artifacts.Transcript{MeetingID: "m"}
	for spk, ts := range turns {
		if len(ts) == 0 {
			continue
		}
		for i, tn := range ts {
			tr.Segments = append(tr.Segments, artifacts.Segment{
				ID: fmt.Sprintf("%s_%d", spk, i), SpeakerID: spk,
				StartSeconds: tn.start, EndSeconds: tn.start + tn.dur,
			})
		}
		tr.Speakers = append(tr.Speakers, artifacts.Speaker{ID: spk, ProviderLabel: spk})
	}
	out, err := emb.EmbedSpeakers(context.Background(), data, tr)
	if err != nil {
		t.Fatalf("EmbedSpeakers: %v", err)
	}
	return out
}

// trimTurns keeps each speaker's longest turns until cumulative speech >= budget.
func trimTurns(turns map[string][]turn, budget float64) map[string][]turn {
	out := map[string][]turn{}
	for spk, ts := range turns {
		cp := append([]turn(nil), ts...)
		sort.Slice(cp, func(i, j int) bool { return cp[i].dur > cp[j].dur })
		var acc float64
		var kept []turn
		for _, tn := range cp {
			kept = append(kept, tn)
			if acc += tn.dur; acc >= budget {
				break
			}
		}
		out[spk] = kept
	}
	return out
}

// corruptTurns reassigns a fraction of turns to a random other speaker in the
// same meeting, simulating diarizer speaker-confusion.
func corruptTurns(turns map[string][]turn, frac float64, rng *rand.Rand) map[string][]turn {
	spks := make([]string, 0, len(turns))
	for s := range turns {
		spks = append(spks, s)
	}
	sort.Strings(spks)
	out := map[string][]turn{}
	for _, s := range spks {
		out[s] = nil
	}
	for _, s := range spks {
		for _, tn := range turns[s] {
			dst := s
			if len(spks) > 1 && rng.Float64() < frac {
				for {
					dst = spks[rng.Intn(len(spks))]
					if dst != s {
						break
					}
				}
			}
			out[dst] = append(out[dst], tn)
		}
	}
	return out
}

// degradePhone band-limits + 8 kHz round-trips the wav (telephone quality).
func degradePhone(t *testing.T, ffmpeg, wavPath string) []byte {
	t.Helper()
	tmp, err := os.CreateTemp("", "phone-*.wav")
	if err != nil {
		t.Fatalf("temp: %v", err)
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	cmd := exec.Command(ffmpeg, "-v", "error", "-y", "-i", wavPath,
		"-ac", "1", "-af", "highpass=f=300,lowpass=f=3400,aresample=8000,aresample=16000",
		"-f", "wav", tmp.Name())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg degrade: %v: %s", err, out)
	}
	data, err := os.ReadFile(tmp.Name())
	if err != nil {
		t.Fatalf("read degraded: %v", err)
	}
	return data
}

// condMetrics: closed-set rank-1, verification EER, and open-set (half enrolled)
// stranger false-accept rate at the pending threshold.
func condMetrics(by map[string][]meetingVec) (rank1, eerv, openFAR float64) {
	spks := make([]string, 0, len(by))
	for s := range by {
		spks = append(spks, s)
	}
	sort.Strings(spks)
	full := map[string][]float64{}
	for _, s := range spks {
		full[s] = by[s][0].vec
	}
	var ok, total int
	var gen, imp []float64
	for _, s := range spks {
		for _, p := range by[s][1:] {
			total++
			best, bv := "", -2.0
			for _, g := range spks {
				sc := cos(p.vec, full[g])
				if g == s {
					gen = append(gen, sc)
				} else {
					imp = append(imp, sc)
				}
				if sc > bv {
					best, bv = g, sc
				}
			}
			if best == s {
				ok++
			}
		}
	}
	// open-set: even-index enrolled, odd-index strangers
	gal := map[string][]float64{}
	for i, s := range spks {
		if i%2 == 0 {
			gal[s] = by[s][0].vec
		}
	}
	var st, fa int
	for i, s := range spks {
		if i%2 == 0 {
			continue
		}
		for _, p := range by[s] {
			st++
			bv := -2.0
			for _, c := range gal {
				if sc := cos(p.vec, c); sc > bv {
					bv = sc
				}
			}
			if bv >= speakers.PendingThreshold {
				fa++
			}
		}
	}
	eerv, _ = eer(gen, imp)
	if total > 0 {
		rank1 = float64(ok) / float64(total)
	}
	if st > 0 {
		openFAR = float64(fa) / float64(st)
	}
	return
}

func TestPersona_Conditions(t *testing.T) {
	if os.Getenv("PERSONA_DEEP") == "" {
		t.Skip("condition sweep (~12 min, several embedding passes) — set PERSONA_DEEP=1")
	}
	emb := loadEmbedder(t)
	dir := amiDir(t)
	ds := loadDataset(t)
	roster := map[string]bool{}
	for _, s := range ds.Speakers {
		roster[s] = true
	}
	ffmpeg := resolveFfmpeg()
	type mt struct {
		id, wav string
		turns   map[string][]turn
	}
	var mts []mt
	for _, m := range ds.Meetings {
		wav := filepath.Join(dir, m.ID+".wav")
		rttm := filepath.Join(dir, m.ID+".rttm")
		if exists(wav) && exists(rttm) {
			turns, _ := parseRTTM(t, rttm)
			mts = append(mts, mt{m.ID, wav, turns})
		}
	}
	if len(mts) == 0 {
		t.Skip("no dataset media present")
	}
	readWav := func(p string) []byte {
		d, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return d
	}
	run := func(name string, transform func(mt) ([]byte, map[string][]turn)) {
		by := map[string][]meetingVec{}
		for _, m := range mts {
			data, turns := transform(m)
			for spk, v := range embedTurnsBytes(t, emb, data, turns) {
				if roster[spk] {
					by[spk] = append(by[spk], meetingVec{m.id, v})
				}
			}
		}
		r, e, f := condMetrics(by)
		t.Logf("  %-24s rank-1 %5.1f%%   EER %5.2f%%   stranger-FAR@0.55 %5.2f%%",
			name, 100*r, 100*e, 100*f)
	}

	t.Log("=========================================================")
	t.Log("CONDITION SWEEP — how accuracy degrades off the clean path")
	t.Log("=========================================================")
	run("baseline (clean GT turns)", func(m mt) ([]byte, map[string][]turn) { return readWav(m.wav), m.turns })
	run("short speech: 6s/persona", func(m mt) ([]byte, map[string][]turn) { return readWav(m.wav), trimTurns(m.turns, 6) })
	run("short speech: 3s/persona", func(m mt) ([]byte, map[string][]turn) { return readWav(m.wav), trimTurns(m.turns, 3) })
	run("diarization noise: 15%", func(m mt) ([]byte, map[string][]turn) {
		return readWav(m.wav), corruptTurns(m.turns, 0.15, rand.New(rand.NewSource(1)))
	})
	run("diarization noise: 30%", func(m mt) ([]byte, map[string][]turn) {
		return readWav(m.wav), corruptTurns(m.turns, 0.30, rand.New(rand.NewSource(2)))
	})
	if ffmpeg != "" {
		run("telephone band (8kHz)", func(m mt) ([]byte, map[string][]turn) { return degradePhone(t, ffmpeg, m.wav), m.turns })
	}
}

// ---- SOTA comparison: mean vs robust aggregation × cosine vs AS-Norm scoring ----

func transcriptFromTurns(turns map[string][]turn) *artifacts.Transcript {
	tr := &artifacts.Transcript{MeetingID: "m"}
	for spk, ts := range turns {
		if len(ts) == 0 {
			continue
		}
		for i, tn := range ts {
			tr.Segments = append(tr.Segments, artifacts.Segment{
				ID: fmt.Sprintf("%s_%d", spk, i), SpeakerID: spk,
				StartSeconds: tn.start, EndSeconds: tn.start + tn.dur,
			})
		}
		tr.Speakers = append(tr.Speakers, artifacts.Speaker{ID: spk, ProviderLabel: spk})
	}
	return tr
}

func meanAgg(ws []speakers.Embedding) []float64 {
	if len(ws) == 0 {
		return nil
	}
	sum := make([]float64, len(ws[0]))
	for _, w := range ws {
		for i, v := range w {
			sum[i] += v
		}
	}
	var n float64
	for _, x := range sum {
		n += x * x
	}
	n = math.Sqrt(n) + 1e-12
	for i := range sum {
		sum[i] /= n
	}
	return sum
}

func robustAgg(ws []speakers.Embedding) []float64 {
	c, _ := speakers.RobustCentroid(ws, 0)
	return c
}

// percentileLow returns the value below which fraction p of xs fall.
func percentileLow(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return math.Inf(-1)
	}
	cp := append([]float64(nil), xs...)
	sort.Float64s(cp)
	idx := int(p * float64(len(cp)))
	if idx >= len(cp) {
		idx = len(cp) - 1
	}
	return cp[idx]
}

// sotaEval scores an aggregated dataset (speaker -> meeting vectors, gallery =
// first meeting) and returns closed-set rank-1, verification EER, and the
// impostor false-accept rate at a 1%-FRR operating point. With asnorm it scores
// via AS-Norm against a cohort of the other gallery speakers (so the operating
// point is comparable across speakers and channels).
func sotaEval(by map[string][]meetingVec, asnorm bool) (rank1, eerv, farAtFRR float64) {
	spks := make([]string, 0, len(by))
	for s := range by {
		spks = append(spks, s)
	}
	sort.Strings(spks)
	gallery := map[string][]float64{}
	for _, s := range spks {
		gallery[s] = by[s][0].vec
	}
	// cohort per target = every other gallery speaker (target excluded).
	cohortFor := map[string][]speakers.Embedding{}
	if asnorm {
		for _, tgt := range spks {
			c := make([]speakers.Embedding, 0, len(spks)-1)
			for _, g := range spks {
				if g != tgt {
					c = append(c, gallery[g])
				}
			}
			cohortFor[tgt] = c
		}
	}
	score := func(q []float64, tgt string) float64 {
		if asnorm {
			return speakers.ScoreASNorm(q, gallery[tgt], cohortFor[tgt], 0)
		}
		return cos(q, gallery[tgt])
	}
	var ok, total int
	var gen, imp []float64
	for _, s := range spks {
		for _, p := range by[s][1:] {
			total++
			best, bv := "", math.Inf(-1)
			for _, g := range spks {
				sc := score(p.vec, g)
				if g == s {
					gen = append(gen, sc)
				} else {
					imp = append(imp, sc)
				}
				if sc > bv {
					best, bv = g, sc
				}
			}
			if best == s {
				ok++
			}
		}
	}
	eerv, _ = eer(gen, imp)
	if total > 0 {
		rank1 = float64(ok) / float64(total)
	}
	thr := percentileLow(gen, 0.01) // keep ~99% of genuine
	farAtFRR = frac(imp, func(x float64) bool { return x >= thr })
	return
}

// TestPersona_SOTA is the head-to-head: for each condition it embeds windows
// once, aggregates them two ways (plain mean vs robust medoid-anchored centroid)
// and scores two ways (raw cosine vs AS-Norm), so the contribution of each
// technique to identification, EER, and open-set false-accepts is isolated.
func TestPersona_SOTA(t *testing.T) {
	if os.Getenv("PERSONA_DEEP") == "" {
		t.Skip("SOTA comparison (~15 min, 4 embedding passes) — set PERSONA_DEEP=1")
	}
	emb := loadEmbedder(t)
	dir := amiDir(t)
	ds := loadDataset(t)
	roster := map[string]bool{}
	for _, s := range ds.Speakers {
		roster[s] = true
	}
	ffmpeg := resolveFfmpeg()
	type mt struct {
		id, wav string
		turns   map[string][]turn
	}
	var mts []mt
	for _, m := range ds.Meetings {
		wav := filepath.Join(dir, m.ID+".wav")
		rttm := filepath.Join(dir, m.ID+".rttm")
		if exists(wav) && exists(rttm) {
			turns, _ := parseRTTM(t, rttm)
			mts = append(mts, mt{m.ID, wav, turns})
		}
	}
	if len(mts) == 0 {
		t.Skip("no dataset media present")
	}
	readWav := func(p string) []byte {
		d, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return d
	}
	run := func(name string, transform func(mt) ([]byte, map[string][]turn)) {
		type item struct {
			meeting string
			ws      []speakers.Embedding
		}
		winBy := map[string][]item{}
		for _, m := range mts {
			data, turns := transform(m)
			w, err := emb.EmbedSpeakerWindows(context.Background(), data, transcriptFromTurns(turns))
			if err != nil {
				t.Fatalf("EmbedSpeakerWindows: %v", err)
			}
			for spk, ws := range w {
				if roster[spk] {
					winBy[spk] = append(winBy[spk], item{m.id, ws})
				}
			}
		}
		meanBy := map[string][]meetingVec{}
		robustBy := map[string][]meetingVec{}
		for spk, items := range winBy {
			for _, it := range items {
				if mv := meanAgg(it.ws); mv != nil {
					meanBy[spk] = append(meanBy[spk], meetingVec{it.meeting, mv})
				}
				if rv := robustAgg(it.ws); rv != nil {
					robustBy[spk] = append(robustBy[spk], meetingVec{it.meeting, rv})
				}
			}
		}
		r1, e1, f1 := sotaEval(meanBy, false)
		r2, e2, f2 := sotaEval(robustBy, false)
		r3, e3, f3 := sotaEval(robustBy, true)
		t.Logf("  %-22s  mean+cosine    rank-1 %5.1f%%  EER %5.2f%%  FAR@1%%FRR %6.2f%%", name, 100*r1, 100*e1, 100*f1)
		t.Logf("  %-22s  robust+cosine  rank-1 %5.1f%%  EER %5.2f%%  FAR@1%%FRR %6.2f%%", "", 100*r2, 100*e2, 100*f2)
		t.Logf("  %-22s  robust+ASNorm  rank-1 %5.1f%%  EER %5.2f%%  FAR@1%%FRR %6.2f%%", "", 100*r3, 100*e3, 100*f3)
	}

	t.Log("=========================================================")
	t.Log("SOTA COMPARISON — aggregation (mean|robust) x scoring (cosine|AS-Norm)")
	t.Log("FAR@1FRR = stranger false-accepts at a 99-percent-genuine-recall operating point")
	t.Log("=========================================================")
	run("baseline", func(m mt) ([]byte, map[string][]turn) { return readWav(m.wav), m.turns })
	run("diarization noise 15%", func(m mt) ([]byte, map[string][]turn) {
		return readWav(m.wav), corruptTurns(m.turns, 0.15, rand.New(rand.NewSource(1)))
	})
	run("diarization noise 30%", func(m mt) ([]byte, map[string][]turn) {
		return readWav(m.wav), corruptTurns(m.turns, 0.30, rand.New(rand.NewSource(2)))
	})
	if ffmpeg != "" {
		run("telephone band 8kHz", func(m mt) ([]byte, map[string][]turn) { return degradePhone(t, ffmpeg, m.wav), m.turns })
	}
}
