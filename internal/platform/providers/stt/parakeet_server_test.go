package stt

import (
	"context"
	"encoding/binary"
	"path/filepath"
	"testing"
)

// fakeParakeetEngine builds the warm-STT engine against the sh-based fake
// server, so the JSONL client (handshake, request/response matching, noise
// skipping, restart) is tested with no python/NeMo dependency — the project's
// fakes-first rule.
func fakeParakeetEngine(t *testing.T) STTEngine {
	t.Helper()
	script, err := filepath.Abs(filepath.Join("testdata", "fake_parakeet_server.sh"))
	if err != nil {
		t.Fatal(err)
	}
	eng, err := newParakeetServerEngine(EngineConfig{
		Options: map[string]string{"python": "/bin/sh", "script": script},
	})
	if err != nil {
		t.Fatalf("newParakeetServerEngine: %v", err)
	}
	return eng
}

// tinyWAV returns a minimal valid 16 kHz mono s16 RIFF/WAVE blob so the chunker
// spills it directly (no ffmpeg in tests).
func tinyWAV() []byte {
	pcm := make([]byte, 3200)
	buf := make([]byte, 44+len(pcm))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+len(pcm)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1)
	binary.LittleEndian.PutUint16(buf[22:24], 1)
	binary.LittleEndian.PutUint32(buf[24:28], 16000)
	binary.LittleEndian.PutUint32(buf[28:32], 32000)
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(len(pcm)))
	return buf
}

func TestParakeetServerEngineWarmRequests(t *testing.T) {
	eng := fakeParakeetEngine(t)
	ctx := context.Background()

	// Two calls against the SAME warm process (ids increment; the fake echoes the
	// id so a mismatch would hang/fail). Word reconstruction reuses the CLI
	// engine's sherpaResult.words: " hello"/" world" → two words with timings.
	for call := 1; call <= 2; call++ {
		words, err := eng.Recognize(ctx, tinyWAV(), TranscribeOptions{})
		if err != nil {
			t.Fatalf("Recognize call %d: %v", call, err)
		}
		if len(words) != 2 || words[0].Text != "hello" || words[1].Text != "world" {
			t.Fatalf("call %d words = %+v", call, words)
		}
		if words[0].StartSeconds != 0 || words[1].StartSeconds != 0.5 {
			t.Fatalf("call %d timings = %+v", call, words)
		}
	}
}

func TestParakeetServerEngineRecognizeBatch(t *testing.T) {
	eng := fakeParakeetEngine(t)
	be, ok := eng.(BatchSTTEngine)
	if !ok {
		t.Fatal("parakeet server engine does not implement BatchSTTEngine")
	}
	out, err := be.RecognizeBatch(context.Background(), [][]byte{tinyWAV(), tinyWAV()}, TranscribeOptions{})
	if err != nil {
		t.Fatalf("RecognizeBatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("batch results = %d, want 2", len(out))
	}
	if out[0][0].Text != "hello" || out[1][1].Text != "world" {
		t.Fatalf("batch words misordered: %+v", out)
	}
}

func TestParakeetServerEngineErrorResponse(t *testing.T) {
	t.Setenv("FAKE_PARAKEET_MODE", "error")
	eng := fakeParakeetEngine(t)
	_, err := eng.Recognize(context.Background(), tinyWAV(), TranscribeOptions{})
	if err == nil || !contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want server error 'boom'", err)
	}
}

func TestParakeetServerEngineRestartsAfterCrash(t *testing.T) {
	t.Setenv("FAKE_PARAKEET_MODE", "crash")
	eng := fakeParakeetEngine(t)
	ctx := context.Background()
	// Crash mode makes the fake exit mid-request; both the attempt and its single
	// retry hit it, so the call errors.
	if _, err := eng.Recognize(ctx, tinyWAV(), TranscribeOptions{}); err == nil {
		t.Fatal("expected error from crashed server")
	}
}

func TestParakeetServerEngineFactoryValidation(t *testing.T) {
	if _, err := newParakeetServerEngine(EngineConfig{Options: map[string]string{"python": "/bin/sh"}}); err == nil {
		t.Error("expected error when script is unset")
	}
	if _, err := newParakeetServerEngine(EngineConfig{Options: map[string]string{
		"python": "/bin/sh", "script": "/nonexistent/server.py",
	}}); err == nil {
		t.Error("expected error for missing script")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
