package diarize

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakePyannoteEngine builds the engine against the REAL server script in its
// no-ML fake mode (NOTO_PYANNOTE_FAKE=1: one 1 s turn per second of audio,
// completion deliberately staggered by request id so concurrent requests
// finish OUT of submission order) — so the concurrent client (id demux,
// shared process, restart) is tested with python3 but no torch/pyannote.
func fakePyannoteEngine(t *testing.T) SegmentEngine {
	t.Helper()
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "scripts", "pyannote_diar_server.py"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(script); err != nil {
		t.Skipf("server script not found: %v", err)
	}
	t.Setenv("NOTO_PYANNOTE_FAKE", "1")
	eng, err := newPyannoteServerEngine(EngineConfig{Options: map[string]string{
		"python": python,
		"script": script,
	}})
	if err != nil {
		t.Fatalf("newPyannoteServerEngine: %v", err)
	}
	return eng
}

// wavOfSeconds returns a silent 16 kHz mono s16 WAV of the given duration —
// the fake backend derives the turn count from it, so each request's response
// is distinguishable and a demux mix-up is caught by content, not just count.
func wavOfSeconds(sec int) []byte {
	pcm := make([]byte, 2*16000*sec)
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

func assertFakeTurns(t *testing.T, turns []EngineTurn, sec, nspk int) {
	t.Helper()
	if len(turns) != sec {
		t.Fatalf("turns = %d, want %d (fake emits one turn/second)", len(turns), sec)
	}
	for i, tr := range turns {
		wantSpk := fmt.Sprintf("SPEAKER_%02d", i%nspk)
		if tr.Speaker != wantSpk || tr.StartSeconds != float64(i) || tr.EndSeconds != float64(i)+1 {
			t.Fatalf("turn[%d] = %+v, want {%s %d %d}", i, tr, wantSpk, i, i+1)
		}
	}
}

// TestPyannoteServerConcurrentOutOfOrder is the core client contract: many
// Segment calls in flight against ONE warm process, responses arriving out of
// order (the fake staggers completion by id), each demultiplexed back to its
// caller. A routing bug shows up as a duration/turn-count mismatch.
func TestPyannoteServerConcurrentOutOfOrder(t *testing.T) {
	eng := fakePyannoteEngine(t)
	ctx := context.Background()

	const n = 6
	errs := make([]error, n)
	turns := make([][]EngineTurn, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			turns[i], errs[i] = eng.Segment(ctx, wavOfSeconds(i+2), DiarizeOptions{NumSpeakers: 2})
		}(i)
	}
	wg.Wait()
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("Segment %d: %v", i, errs[i])
		}
		assertFakeTurns(t, turns[i], i+2, 2)
	}
}

// TestPyannoteServerSharedProcess pins the registry: engine instances with
// identical config share ONE client (= one warm process, one CUDA context) —
// the whole point of the one-process architecture vs a process-per-engine pool.
func TestPyannoteServerSharedProcess(t *testing.T) {
	e1 := fakePyannoteEngine(t).(*pyannoteServerEngine)
	e2 := fakePyannoteEngine(t).(*pyannoteServerEngine)
	if e1.client != e2.client {
		t.Fatal("two engines with identical config must share one client/process")
	}
	turns, err := e2.Segment(context.Background(), wavOfSeconds(3), DiarizeOptions{NumSpeakers: 2})
	if err != nil {
		t.Fatalf("Segment via second handle: %v", err)
	}
	assertFakeTurns(t, turns, 3, 2)
}

// TestPyannoteServerSegmentBatch covers the fan-out batch path (concurrent
// single-wav requests, results in input order).
func TestPyannoteServerSegmentBatch(t *testing.T) {
	eng := fakePyannoteEngine(t)
	be, ok := eng.(BatchSegmentEngine)
	if !ok {
		t.Fatal("pyannote server engine does not implement BatchSegmentEngine")
	}
	out, err := be.SegmentBatch(context.Background(),
		[][]byte{wavOfSeconds(3), wavOfSeconds(5)},
		[]DiarizeOptions{{NumSpeakers: 2}, {NumSpeakers: 3}})
	if err != nil {
		t.Fatalf("SegmentBatch: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("batch results = %d, want 2", len(out))
	}
	assertFakeTurns(t, out[0], 3, 2)
	assertFakeTurns(t, out[1], 5, 3)
}

func TestPyannoteServerErrorResponse(t *testing.T) {
	eng := fakePyannoteEngine(t)
	_, err := eng.Segment(context.Background(), wavOfSeconds(2), DiarizeOptions{NumSpeakers: 7})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want server error 'boom'", err)
	}
}

func TestPyannoteServerRestartsAfterCrash(t *testing.T) {
	eng := fakePyannoteEngine(t)
	ctx := context.Background()

	// num_speakers=9 makes the fake exit mid-request; both the attempt and its
	// single retry hit it, so the call errors…
	if _, err := eng.Segment(ctx, wavOfSeconds(2), DiarizeOptions{NumSpeakers: 9}); err == nil {
		t.Fatal("expected error from crashed server")
	}
	// …but the shared client recovers: the next normal call restarts the process.
	turns, err := eng.Segment(ctx, wavOfSeconds(4), DiarizeOptions{NumSpeakers: 2})
	if err != nil {
		t.Fatalf("Segment after crash: %v", err)
	}
	assertFakeTurns(t, turns, 4, 2)
}

func TestPyannoteServerFactoryValidation(t *testing.T) {
	if _, err := newPyannoteServerEngine(EngineConfig{Options: map[string]string{"python": "/bin/sh"}}); err == nil {
		t.Error("expected error when script is unset")
	}
	if _, err := newPyannoteServerEngine(EngineConfig{Options: map[string]string{
		"python": "/bin/sh", "script": "/nonexistent/server.py",
	}}); err == nil {
		t.Error("expected error for missing script")
	}
}
