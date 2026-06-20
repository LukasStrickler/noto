package speaker

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// repoRoot resolves the repo root relative to this test file.
func repoRoot() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", "..", ".."))
}

// testEngine loads the model+lib from the dev tools dir (or env), skipping if absent.
func testEngine(t *testing.T) (*LocalEmbedder, map[string][]string) {
	t.Helper()
	root := repoRoot()
	model := envOr("ECAPA_MODEL", filepath.Join(root, "tools/voiceprint/models/ecapa512.onnx"))
	lib := envOr("ORT_LIB", filepath.Join(root, "tools/voiceprint/onnxruntime/lib/libonnxruntime.so"))
	ffmpeg := envOr("FFMPEG", filepath.Join(root, "tools/voiceprint/bin/ffmpeg"))
	audioDir := filepath.Join(root, "benchmark/dataset/librispeech")
	if !exists(model) || !exists(lib) || !exists(filepath.Join(audioDir, "manifest.json")) {
		t.Skip("voiceprint assets missing (run: noto speaker-model download + bench fetch)")
	}
	eng, err := NewECAPA(model, lib)
	if err != nil {
		t.Fatalf("NewECAPA: %v", err)
	}
	t.Cleanup(func() { eng.Close() })
	var man map[string][]string
	raw, _ := os.ReadFile(filepath.Join(audioDir, "manifest.json"))
	if err := json.Unmarshal(raw, &man); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	for s, fs := range man {
		for i := range fs {
			fs[i] = filepath.Join(audioDir, fs[i])
		}
		man[s] = fs
	}
	return NewLocalEmbedder(eng, ffmpeg), man
}

// buildMeeting concatenates (speaker,clipPath) items into one WAV blob + transcript,
// labelling each speaker A,B,C… in order.
func (l *LocalEmbedder) buildMeeting(t *testing.T, items [][2]string) ([]byte, *artifacts.Transcript) {
	t.Helper()
	var pcm []float32
	var segs []artifacts.Segment
	var spks []artifacts.Speaker
	pos := 0.0
	for i, it := range items {
		label := string(rune('A' + i))
		x, err := DecodeFile(l.ffmpeg, it[1])
		if err != nil {
			t.Fatalf("decode %s: %v", it[1], err)
		}
		dur := float64(len(x)) / sampleRate
		spks = append(spks, artifacts.Speaker{ID: label, ProviderLabel: label})
		segs = append(segs, artifacts.Segment{
			ID: label + "0", SpeakerID: label,
			StartSeconds: pos, EndSeconds: pos + dur,
		})
		pcm = append(pcm, x...)
		pos += dur
	}
	tr := &artifacts.Transcript{MeetingID: "m", Speakers: spks, Segments: segs}
	return encodeWAV(pcm), tr
}

func TestLocalEmbedder_CrossMeetingSeparation(t *testing.T) {
	emb, man := testEngine(t)
	var spk []string
	for s := range man {
		if len(man[s]) >= 2 {
			spk = append(spk, s)
		}
	}
	if len(spk) < 3 {
		t.Skip("need >=3 speakers with >=2 clips")
	}
	// Sort for a DETERMINISTIC speaker triple: map iteration order is randomized,
	// so picking spk[0..2] from an unsorted slice chose different speakers each run
	// — and some pairs are acoustically closer than others, which intermittently
	// pushed the different-speaker cosine past 0.40 (a flaky failure under load).
	sort.Strings(spk)
	s0, s1, s2 := spk[0], spk[1], spk[2]

	// meeting A: s0,s1,s2 (clip 0)   meeting B: s0,s1 (clip 1)
	wavA, trA := emb.buildMeeting(t, [][2]string{{s0, man[s0][0]}, {s1, man[s1][0]}, {s2, man[s2][0]}})
	wavB, trB := emb.buildMeeting(t, [][2]string{{s0, man[s0][1]}, {s1, man[s1][1]}})

	embA, err := emb.EmbedSpeakers(context.Background(), wavA, trA)
	if err != nil {
		t.Fatalf("EmbedSpeakers A: %v", err)
	}
	embB, err := emb.EmbedSpeakers(context.Background(), wavB, trB)
	if err != nil {
		t.Fatalf("EmbedSpeakers B: %v", err)
	}
	for _, m := range []map[string][]float64{embA, embB} {
		for lbl, v := range m {
			if len(v) != embeddingSize {
				t.Fatalf("label %s dim=%d want %d", lbl, len(v), embeddingSize)
			}
		}
	}
	// same speaker across meetings (A.A vs B.A) must beat different (A.A vs B.B)
	same := cos(embA["A"], embB["A"]) // s0 vs s0
	diff := cos(embA["A"], embB["B"]) // s0 vs s1
	t.Logf("cross-meeting cosine: same-speaker=%.3f different-speaker=%.3f", same, diff)
	if same <= diff {
		t.Fatalf("same-speaker cosine %.3f not greater than different %.3f", same, diff)
	}
	if same < 0.40 {
		t.Fatalf("same-speaker cosine %.3f below sane floor 0.40", same)
	}
	if diff > 0.40 {
		t.Fatalf("different-speaker cosine %.3f above 0.40 (would risk a false merge)", diff)
	}
}

func envOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
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

func encodeWAV(pcm []float32) []byte {
	var b bytes.Buffer
	dataLen := len(pcm) * 2
	w16 := func(v uint16) { binary.Write(&b, binary.LittleEndian, v) }
	w32 := func(v uint32) { binary.Write(&b, binary.LittleEndian, v) }
	b.WriteString("RIFF")
	w32(uint32(36 + dataLen))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	w32(16)
	w16(1) // PCM
	w16(1) // mono
	w32(sampleRate)
	w32(sampleRate * 2) // byte rate
	w16(2)              // block align
	w16(16)             // bits
	b.WriteString("data")
	w32(uint32(dataLen))
	for _, v := range pcm {
		s := v * 32768
		if s > 32767 {
			s = 32767
		} else if s < -32768 {
			s = -32768
		}
		w16(uint16(int16(s)))
	}
	return b.Bytes()
}
