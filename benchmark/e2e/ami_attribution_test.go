package e2e

// Local, FREE attribution lab. The expensive GPU output (raw STT word timings +
// raw diarization turns) is already cached in every captured hyp JSON, so the
// merge step that produces the attribution tax (cpWER − WER) can be re-run and
// re-scored entirely off-box. This test reads BENCH_HYP_DIR + the local AMI
// references and prints cpWER under several attribution strategies plus the
// ORACLE ceiling (perfect per-word speaker from the RTTM) — so we know how much
// of the tax a better merge can actually recover before changing production.
//
//	BENCH_HYP_DIR=/tmp/ami_dev/hyps go test ./benchmark/e2e -run TestAMIAttributionLab -v
//
// Skips when BENCH_HYP_DIR is unset, so it never runs in the normal suite.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/metrics"
)

// attrStrategy assigns a speaker label to each hyp word, given the raw words, the
// raw diarization turns, and (for the oracle) the reference turns.
type attrStrategy struct {
	name string
	fn   func(words []hypWord, turns []hypTurn, refTurns []dataset.Turn) []string
}

// --- strategies -------------------------------------------------------------

// baked: whatever the captured pipeline already assigned (current production).
func attrBaked(words []hypWord, _ []hypTurn, _ []dataset.Turn) []string {
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = w.Speaker
	}
	return out
}

// midpoint: faithful replica of merge.Attribute — the turn containing the word
// midpoint, else greatest overlap, else nearest in time. Sanity check vs baked.
func attrMidpoint(words []hypWord, turns []hypTurn, _ []dataset.Turn) []string {
	st := sortTurns(turns)
	out := make([]string, len(words))
	for i, w := range words {
		mid := (w.Start + w.End) / 2
		bestOv, bestSpk := 0.0, ""
		nearGap, nearSpk := -1.0, ""
		hit := ""
		for _, t := range st {
			if mid >= t.Start && mid < t.End {
				hit = t.Speaker
				break
			}
			if ov := ovl(w.Start, w.End, t.Start, t.End); ov > bestOv {
				bestOv, bestSpk = ov, t.Speaker
			}
			g := gap(mid, t.Start, t.End)
			if nearGap < 0 || g < nearGap {
				nearGap, nearSpk = g, t.Speaker
			}
		}
		switch {
		case hit != "":
			out[i] = hit
		case bestOv > 0:
			out[i] = bestSpk
		default:
			out[i] = nearSpk
		}
	}
	return out
}

// overlap: greatest temporal overlap is the PRIMARY rule (not midpoint), so a
// word that is mostly inside speaker A but whose midpoint tips just past a jittery
// turn boundary into B stays with A. Nearest in time only when nothing overlaps.
func attrOverlap(words []hypWord, turns []hypTurn, _ []dataset.Turn) []string {
	st := sortTurns(turns)
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = bestOverlapSpk(w.Start, w.End, st)
	}
	return out
}

// overlapSmooth: overlap assignment, then collapse single-word speaker flips that
// sit between two agreeing neighbours (A A [B] A A → A A A A A). Such 1-word
// islands are almost always diarization boundary jitter, not a real interjection.
func attrOverlapSmooth(words []hypWord, turns []hypTurn, rt []dataset.Turn) []string {
	out := attrOverlap(words, turns, rt)
	for i := 1; i < len(out)-1; i++ {
		if out[i] != out[i-1] && out[i-1] == out[i+1] && out[i-1] != "" {
			out[i] = out[i-1]
		}
	}
	return out
}

// oracle: assign each hyp word to the REFERENCE speaker talking at its time
// (greatest overlap with the RTTM turns). The best attribution physically
// possible given the STT words are fixed — the floor cpWER, all tax removed.
func attrOracle(words []hypWord, _ []hypTurn, refTurns []dataset.Turn) []string {
	st := make([]hypTurn, len(refTurns))
	for i, t := range refTurns {
		st[i] = hypTurn{Speaker: t.Speaker, Start: t.StartSeconds, End: t.EndSeconds}
	}
	sort.SliceStable(st, func(i, j int) bool { return st[i].Start < st[j].Start })
	out := make([]string, len(words))
	for i, w := range words {
		out[i] = bestOverlapSpk(w.Start, w.End, st)
	}
	return out
}

// --- geometry helpers -------------------------------------------------------

func sortTurns(turns []hypTurn) []hypTurn {
	st := append([]hypTurn(nil), turns...)
	sort.SliceStable(st, func(i, j int) bool { return st[i].Start < st[j].Start })
	return st
}

func bestOverlapSpk(start, end float64, st []hypTurn) string {
	bestOv, bestSpk := 0.0, ""
	nearGap, nearSpk := -1.0, ""
	mid := (start + end) / 2
	for _, t := range st {
		if ov := ovl(start, end, t.Start, t.End); ov > bestOv {
			bestOv, bestSpk = ov, t.Speaker
		}
		g := gap(mid, t.Start, t.End)
		if nearGap < 0 || g < nearGap {
			nearGap, nearSpk = g, t.Speaker
		}
	}
	if bestOv > 0 {
		return bestSpk
	}
	return nearSpk
}

func ovl(aS, aE, bS, bE float64) float64 {
	lo, hi := maxf(aS, bS), minf(aE, bE)
	if hi <= lo {
		return 0
	}
	return hi - lo
}

func gap(t, s, e float64) float64 {
	if t < s {
		return s - t
	}
	if t > e {
		return t - e
	}
	return 0
}

func maxf(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}
func minf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// groupBySpeaker concatenates each speaker's words (time order) and normalizes —
// the shape metrics.CpWER consumes.
func groupBySpeaker(words []hypWord, spk []string) map[string][]string {
	type tw struct {
		start float64
		text  string
	}
	by := map[string][]tw{}
	for i, w := range words {
		by[spk[i]] = append(by[spk[i]], tw{w.Start, w.Text})
	}
	out := map[string][]string{}
	for s, ws := range by {
		sort.SliceStable(ws, func(i, j int) bool { return ws[i].start < ws[j].start })
		var b []string
		for _, w := range ws {
			b = append(b, w.text)
		}
		if toks := metrics.Normalize(joinSpace(b)); len(toks) > 0 {
			out[s] = toks
		}
	}
	return out
}

func TestAMIAttributionLab(t *testing.T) {
	hypDir := os.Getenv("BENCH_HYP_DIR")
	if hypDir == "" {
		t.Skip("set BENCH_HYP_DIR to cached AMI hyps (free local attribution lab)")
	}
	hyps, _ := filepath.Glob(filepath.Join(hypDir, "*.json"))
	if len(hyps) == 0 {
		t.Skipf("no hyp JSON in %s", hypDir)
	}

	strategies := []attrStrategy{
		{"baked", attrBaked},
		{"midpoint", attrMidpoint},
		{"overlap", attrOverlap},
		{"ovl+smooth", attrOverlapSmooth},
		{"ORACLE", attrOracle},
	}

	// Aggregate cpWER errors/reflen per strategy, plus the plain WER denominator.
	type agg struct{ e, r float64 }
	cp := make([]agg, len(strategies))
	var werE, werR float64
	var meetings int

	t.Logf("%-10s %7s | %s", "meeting", "WER", "cpWER by strategy →")
	header := ""
	for _, s := range strategies {
		header += pad(s.name)
	}
	t.Logf("%-10s %7s | %s", "", "", header)

	for _, hp := range hyps {
		raw, err := os.ReadFile(hp)
		if err != nil {
			t.Fatalf("read %s: %v", hp, err)
		}
		var hm hypMeeting
		if err := json.Unmarshal(raw, &hm); err != nil {
			t.Fatalf("hyp %s: %v", hp, err)
		}
		refWords, err := dataset.LoadWords(filepath.Join(amiWordsDir(), hm.ID+".words.json"))
		if err != nil {
			continue
		}
		refTurns, err := dataset.LoadRTTM(filepath.Join(amiAudioDir(), hm.ID+".rttm"))
		if err != nil {
			continue
		}
		ref := dataset.Meeting{ID: hm.ID, Words: refWords, Turns: refTurns}
		refBySpk := ref.ReferenceBySpeaker()

		wer := metrics.WER(ref.Reference(), hypWordTokens(hm.Words))
		werE += float64(wer.Errors())
		werR += float64(wer.RefLen)

		row := ""
		for si, s := range strategies {
			spk := s.fn(hm.Words, hm.Turns, refTurns)
			c := metrics.CpWER(refBySpk, groupBySpeaker(hm.Words, spk))
			cp[si].e += float64(c.Errors())
			cp[si].r += float64(c.RefLen)
			row += padf(c.Rate * 100)
		}
		t.Logf("%-10s %6.1f%% | %s", hm.ID, wer.Rate*100, row)
		meetings++
	}
	if meetings == 0 {
		t.Skip("no AMI meetings had both hyps and references")
	}

	div := func(a, b float64) float64 {
		if b == 0 {
			return 0
		}
		return a / b
	}
	overallWER := div(werE, werR) * 100
	t.Logf("")
	t.Logf("OVERALL over %d meetings — WER %.1f%%", meetings, overallWER)
	t.Logf("%-12s %8s %8s %8s", "strategy", "cpWER", "tax", "vs baked")
	bakedRate := div(cp[0].e, cp[0].r) * 100
	for si, s := range strategies {
		rate := div(cp[si].e, cp[si].r) * 100
		t.Logf("%-12s %7.1f%% %+7.1f %+7.1f", s.name, rate, rate-overallWER, rate-bakedRate)
	}
}

func pad(s string) string {
	for len(s) < 11 {
		s += " "
	}
	return s
}

func padf(v float64) string {
	s := ""
	d := int(v*10 + 0.5)
	s = itoa(d/10) + "." + itoa(d%10) + "%"
	for len(s) < 11 {
		s += " "
	}
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
