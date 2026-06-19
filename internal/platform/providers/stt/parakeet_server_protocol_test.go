package stt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestParakeetServerScriptProtocol pins the contract between the Go warm-server
// engine and scripts/parakeet_stt_server.py: the NeMo-backed server emits ONE
// token per word (leading-space word starts), so words() must reconstruct
// exactly the server's word offsets. Runs the real script in its no-ML fake
// mode (NOTO_PARAKEET_FAKE=1: one word per second of audio), so this needs
// python3 but no torch/NeMo.
func TestParakeetServerScriptProtocol(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "parakeet_stt_server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("server script not found: %v", err)
	}
	t.Setenv("NOTO_PARAKEET_FAKE", "1")
	eng, err := newParakeetServerEngine(EngineConfig{
		Options: map[string]string{"python": python, "script": script},
	})
	if err != nil {
		t.Fatalf("newParakeetServerEngine: %v", err)
	}

	words, err := eng.Recognize(context.Background(), tinyWAV3s(t), TranscribeOptions{})
	if err != nil {
		t.Fatalf("Recognize via server script: %v", err)
	}
	if len(words) != 3 {
		t.Fatalf("words = %d, want 3 (fake emits one word/second over 3 s)", len(words))
	}
	for i, w := range words {
		wantStart := float64(i)
		if w.Text != "w"+string(rune('0'+i)) || w.StartSeconds != wantStart || w.EndSeconds != wantStart+0.5 {
			t.Errorf("word[%d] = %q [%v,%v], want %q [%v,%v]",
				i, w.Text, w.StartSeconds, w.EndSeconds, "w"+string(rune('0'+i)), wantStart, wantStart+0.5)
		}
	}

	// The batched request path (chunk groups / RecognizeBatch) must come back in
	// input order with per-audio word streams.
	be, ok := eng.(BatchSTTEngine)
	if !ok {
		t.Fatal("parakeet-server engine should implement BatchSTTEngine")
	}
	batch, err := be.RecognizeBatch(context.Background(), [][]byte{tinyWAV3s(t), tinyWAV3s(t)}, TranscribeOptions{})
	if err != nil {
		t.Fatalf("RecognizeBatch via server script: %v", err)
	}
	if len(batch) != 2 || len(batch[0]) != 3 || len(batch[1]) != 3 {
		t.Fatalf("batch shape = %d/%v, want 2 audios × 3 words", len(batch), batch)
	}
}

// tinyWAV3s is a 3 s silent 16 kHz mono s16 WAV (the fake backend derives word
// count from duration; tinyWAV's 0.1 s yields zero words).
func tinyWAV3s(t *testing.T) []byte {
	t.Helper()
	return buildWAV(16000, 1, 16, make([]byte, 2*16000*3))
}
