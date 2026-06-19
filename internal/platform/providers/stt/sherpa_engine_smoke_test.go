package stt

import (
	"context"
	"os"
	"strings"
	"testing"
)

// TestSherpaEngineSmoke transcribes a real WAV through the full LocalSTT→sherpa
// subprocess path. Env-gated: set NOTO_SHERPA_BIN, NOTO_SHERPA_LIB,
// NOTO_SHERPA_MODELS, and NOTO_SHERPA_TESTWAV. Skips otherwise so `make test`
// stays asset-free.
func TestSherpaEngineSmoke(t *testing.T) {
	if os.Getenv("NOTO_SHERPA_BIN") == "" {
		t.Skip("set NOTO_SHERPA_BIN/LIB/MODELS/TESTWAV to run the real sherpa engine")
	}
	wav := os.Getenv("NOTO_SHERPA_TESTWAV")
	if wav == "" {
		t.Skip("set NOTO_SHERPA_TESTWAV")
	}
	p, err := NewLocalSTTByName("sherpa-parakeet", EngineConfig{ModelDir: os.Getenv("NOTO_SHERPA_MODELS")})
	if err != nil {
		t.Fatalf("build engine: %v", err)
	}
	audio, err := os.ReadFile(wav)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	tr, err := p.Transcribe(context.Background(), audio, TranscribeOptions{Language: "en"})
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	var sb strings.Builder
	for _, w := range tr.Words {
		sb.WriteString(w.Text)
		sb.WriteString(" ")
	}
	t.Logf("provider=%s words=%d segments=%d dur=%.1fs", p.ProviderID(), len(tr.Words), len(tr.Segments), tr.DurationSeconds)
	t.Logf("transcript: %q", strings.TrimSpace(sb.String()))
	if len(tr.Words) == 0 {
		t.Fatal("no words recognized")
	}
}
