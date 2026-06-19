package stt

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestBuildParseWAVRoundTrip(t *testing.T) {
	// 16 kHz mono s16, 4 samples.
	pcm := []byte{1, 0, 2, 0, 3, 0, 4, 0}
	wav := buildWAV(16000, 1, 16, pcm)
	if !isWAVBytes(wav) {
		t.Fatal("buildWAV did not produce a RIFF/WAVE file")
	}
	rate, ch, bits, got, err := parseWAV(wav)
	if err != nil {
		t.Fatalf("parseWAV: %v", err)
	}
	if rate != 16000 || ch != 1 || bits != 16 {
		t.Errorf("fmt = %d/%d/%d, want 16000/1/16", rate, ch, bits)
	}
	if !bytes.Equal(got, pcm) {
		t.Errorf("pcm mismatch: got %v want %v", got, pcm)
	}
}

// TestParseWAVSkipsExtraChunks ensures the parser tolerates a LIST chunk before
// data (some encoders insert metadata), which a fixed-44-byte reader would miss.
func TestParseWAVSkipsExtraChunks(t *testing.T) {
	pcm := []byte{9, 0, 8, 0}
	var b bytes.Buffer
	b.WriteString("RIFF")
	b.Write(make([]byte, 4)) // size (unchecked)
	b.WriteString("WAVE")
	// fmt
	b.WriteString("fmt ")
	binary.Write(&b, binary.LittleEndian, uint32(16))
	binary.Write(&b, binary.LittleEndian, uint16(1))     // PCM
	binary.Write(&b, binary.LittleEndian, uint16(1))     // ch
	binary.Write(&b, binary.LittleEndian, uint32(16000)) // rate
	binary.Write(&b, binary.LittleEndian, uint32(32000)) // byteRate
	binary.Write(&b, binary.LittleEndian, uint16(2))     // blockAlign
	binary.Write(&b, binary.LittleEndian, uint16(16))    // bits
	// LIST chunk (odd size to exercise word-alignment padding)
	b.WriteString("LIST")
	binary.Write(&b, binary.LittleEndian, uint32(3))
	b.Write([]byte{0x61, 0x62, 0x63})
	b.WriteByte(0) // pad
	// data
	b.WriteString("data")
	binary.Write(&b, binary.LittleEndian, uint32(len(pcm)))
	b.Write(pcm)

	rate, ch, bits, got, err := parseWAV(b.Bytes())
	if err != nil {
		t.Fatalf("parseWAV: %v", err)
	}
	if rate != 16000 || ch != 1 || bits != 16 || !bytes.Equal(got, pcm) {
		t.Errorf("got %d/%d/%d data=%v, want 16000/1/16 data=%v", rate, ch, bits, got, pcm)
	}
}

func TestParseWAVRejectsNonWAV(t *testing.T) {
	if _, _, _, _, err := parseWAV([]byte("not a wav at all")); err == nil {
		t.Error("expected error for non-WAV input")
	}
}

// TestSherpaResultWords reconstructs words from subword tokens + timestamps.
func TestSherpaResultWords(t *testing.T) {
	r := sherpaResult{
		Text:       "the cat sat",
		Tokens:     []string{" the", " ca", "t", " sat"},
		Timestamps: []float64{0.0, 0.5, 0.7, 1.0},
		Durations:  []float64{0.2, 0.2, 0.1, 0.3},
	}
	words := r.words()
	if len(words) != 3 {
		t.Fatalf("got %d words, want 3: %+v", len(words), words)
	}
	if words[0].Text != "the" || words[1].Text != "cat" || words[2].Text != "sat" {
		t.Errorf("words = %q/%q/%q", words[0].Text, words[1].Text, words[2].Text)
	}
	// "cat" spans tokens " ca"(0.5) + "t"(end 0.8) → start 0.5, end 0.8.
	if words[1].StartSeconds != 0.5 || words[1].EndSeconds < 0.79 {
		t.Errorf("cat timing = %.2f-%.2f, want 0.50-0.80", words[1].StartSeconds, words[1].EndSeconds)
	}
}

// TestSherpaResultWordsConfidence carries per-token confidence onto words, taking
// the least-confident token for a multi-token word (a word is only as trustworthy
// as its weakest subword), and leaving Confidence 0 when the engine emits none.
func TestSherpaResultWordsConfidence(t *testing.T) {
	r := sherpaResult{
		Text:        "the cat sat",
		Tokens:      []string{" the", " ca", "t", " sat"},
		Timestamps:  []float64{0.0, 0.5, 0.7, 1.0},
		Durations:   []float64{0.2, 0.2, 0.1, 0.3},
		Confidences: []float64{0.95, 0.80, 0.40, 0.99},
	}
	words := r.words()
	if len(words) != 3 {
		t.Fatalf("got %d words, want 3", len(words))
	}
	if words[0].Confidence != 0.95 || words[2].Confidence != 0.99 {
		t.Errorf("single-token conf = %.2f/%.2f, want 0.95/0.99", words[0].Confidence, words[2].Confidence)
	}
	// "cat" = min(0.80, 0.40) = 0.40.
	if words[1].Confidence != 0.40 {
		t.Errorf("cat confidence = %.2f, want 0.40 (weakest subword)", words[1].Confidence)
	}

	// No Confidences emitted → all words leave Confidence at 0 (→ nil downstream).
	noConf := sherpaResult{Text: "hi", Tokens: []string{" hi"}, Timestamps: []float64{0}, Durations: []float64{0.1}}
	if w := noConf.words(); len(w) != 1 || w[0].Confidence != 0 {
		t.Errorf("no-confidence word = %+v, want Confidence 0", w)
	}
}
