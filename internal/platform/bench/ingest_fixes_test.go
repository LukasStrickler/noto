package bench

import (
	"os"
	"path/filepath"
	"testing"
)

// A single truncated/corrupt hyp file (what an interrupted incremental capture
// leaves) must be skipped, not abort the whole load — the good meetings still
// score, matching the Python producer's per-file try/except.
func TestLoadMeetingHyps_SkipsMalformed(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.json"),
		[]byte(`{"meeting_id":"good","audio_sec":100}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.json"),
		[]byte(`{"meeting_id":"bad","words":[`), 0o600); err != nil { // truncated JSON
		t.Fatal(err)
	}
	hyps, err := loadMeetingHyps(dir)
	if err != nil {
		t.Fatalf("one corrupt file must not fail the whole load: %v", err)
	}
	if len(hyps) != 1 || hyps[0].MeetingID != "good" {
		t.Fatalf("want only the good meeting, got %+v", hyps)
	}
}

// extractPyannoteStderr must isolate the single bracketed timing line from a
// multi-line setup.log so the per-line parsers don't sweep tokens from other
// lines; the isolated line must then parse the live bracketed stage group.
func TestExtractPyannoteStderr_IsolatesBracketedLine(t *testing.T) {
	log := "starting server\n[pyannote-server] timing worker=0 kept=0.85 [segmentation=800 embeddings=4800 clustering=200]\nshutdown complete"
	got := extractPyannoteStderr(log)
	if got == log {
		t.Fatal("must isolate the timing line, not return the whole multi-line log")
	}
	if pt := ParsePyannoteStderr(got); pt.StagesMS["embeddings"] != 4800 {
		t.Fatalf("isolated line should parse the bracketed stages, got %v", pt.StagesMS)
	}
}
