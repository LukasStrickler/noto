package bench

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/benchmark/dataset"
	"github.com/lukasstrickler/noto/benchmark/internal/sample"
)

func TestAppendRunAndCompare(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs.jsonl")

	// first run of a label: no previous
	prev, err := AppendRun(path, NewRun("e2e", "oracle", map[string]float64{"cpwer": 0.20, "der": 0.15}))
	if err != nil {
		t.Fatalf("AppendRun: %v", err)
	}
	if prev != nil {
		t.Errorf("first run should have no previous, got %+v", prev)
	}

	// an unrelated label must not become the "previous" for e2e
	if _, err := AppendRun(path, NewRun("stt", "oracle", map[string]float64{"wer": 0.10})); err != nil {
		t.Fatalf("AppendRun(stt): %v", err)
	}

	// second e2e run: previous is the first e2e run (label-matched), not stt
	prev, err = AppendRun(path, NewRun("e2e", "oracle", map[string]float64{"cpwer": 0.12, "der": 0.15}))
	if err != nil {
		t.Fatalf("AppendRun: %v", err)
	}
	if prev == nil {
		t.Fatal("second e2e run should have a previous")
	}
	if prev.Metrics["cpwer"] != 0.20 {
		t.Errorf("previous cpwer = %v, want 0.20", prev.Metrics["cpwer"])
	}

	changes := Compare(prev, NewRun("e2e", "oracle", map[string]float64{"cpwer": 0.12, "der": 0.15}))
	got := map[string]Change{}
	for _, c := range changes {
		got[c.Name] = c
	}
	// cpwer improved 0.20 → 0.12
	if c := got["cpwer"]; !c.Improved() || c.Arrow() != "▼" {
		t.Errorf("cpwer change = %+v (arrow %s), want improvement", c, c.Arrow())
	}
	// der unchanged
	if c := got["der"]; c.Delta != 0 || c.Arrow() != "=" {
		t.Errorf("der change = %+v, want unchanged", c)
	}
}

// mtg builds a meeting whose AudioSeconds() == dur via a single trailing turn.
func mtg(id string, dur float64) dataset.Meeting {
	return dataset.Meeting{ID: id, Turns: []dataset.Turn{{Speaker: "A", StartSeconds: 0, EndSeconds: dur}}}
}

func TestRunMeetingsResultsInInputOrder(t *testing.T) {
	defer setJobs(3)()
	meetings := []dataset.Meeting{mtg("a", 10), mtg("b", 30), mtg("c", 5), mtg("d", 20), mtg("e", 15)}
	out := RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) string { return m.ID })
	want := []string{"a", "b", "c", "d", "e"} // input order, regardless of LPT processing
	for i := range want {
		if out[i] != want[i] {
			t.Fatalf("out[%d] = %q, want %q (results must stay in input order)", i, out[i], want[i])
		}
	}
}

func TestRunMeetingsLPTStartsLongestFirst(t *testing.T) {
	defer setJobs(2)()
	// Durations out of order; with 2 workers the first 2 STARTED must be the 2
	// longest (b=30, d=20), not the first two in input order (a, b).
	meetings := []dataset.Meeting{mtg("a", 10), mtg("b", 30), mtg("c", 5), mtg("d", 20), mtg("e", 15)}

	var mu sync.Mutex
	var startOrder []string
	var inFlight, maxInFlight int32
	RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) string {
		mu.Lock()
		startOrder = append(startOrder, m.ID)
		mu.Unlock()
		n := atomic.AddInt32(&inFlight, 1)
		for { // track the high-water concurrency
			hi := atomic.LoadInt32(&maxInFlight)
			if n <= hi || atomic.CompareAndSwapInt32(&maxInFlight, hi, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return m.ID
	})

	if len(startOrder) < 2 || startOrder[0] != "b" || startOrder[1] != "d" {
		t.Fatalf("first starts = %v, want the two longest [b d] first (LPT)", startOrder[:min(2, len(startOrder))])
	}
	if maxInFlight > 2 {
		t.Fatalf("max concurrency = %d, want ≤ jobs (2)", maxInFlight)
	}
}

func TestRunMeetingsJobs1IsSequential(t *testing.T) {
	defer setJobs(1)()
	meetings := []dataset.Meeting{mtg("a", 10), mtg("b", 30), mtg("c", 5)}
	var maxInFlight, inFlight int32
	RunMeetings(t, meetings, func(t *testing.T, m dataset.Meeting) string {
		n := atomic.AddInt32(&inFlight, 1)
		if n > atomic.LoadInt32(&maxInFlight) {
			atomic.StoreInt32(&maxInFlight, n)
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&inFlight, -1)
		return m.ID
	})
	if maxInFlight != 1 {
		t.Fatalf("jobs=1 ran %d concurrently, want strictly 1 (honest single stream)", maxInFlight)
	}
}

// setJobs sets the -jobs flag for a test and returns a restore func.
func setJobs(n int) func() {
	old := *sample.Jobs
	*sample.Jobs = n
	return func() { *sample.Jobs = old }
}

func TestReadRunsMissingFileIsEmpty(t *testing.T) {
	runs, err := ReadRuns(filepath.Join(t.TempDir(), "nope.jsonl"))
	if err != nil {
		t.Fatalf("ReadRuns missing: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected empty history, got %d", len(runs))
	}
}

func TestCompareWorseIsUpArrow(t *testing.T) {
	prev := &Run{Metrics: map[string]float64{"wer": 0.10}}
	cur := Run{Metrics: map[string]float64{"wer": 0.18}}
	c := Compare(prev, cur)[0]
	if c.Improved() || c.Arrow() != "▲" {
		t.Errorf("worse metric = %+v (arrow %s), want ▲", c, c.Arrow())
	}
}
