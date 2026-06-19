package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/lukasstrickler/noto/benchmark/internal/sample"
)

// Run is one recorded benchmark run: its metrics plus enough context (runtime,
// sample) to know what produced them. All benchmark metrics here are
// "lower is better" (WER/DER/cpWER/SA-WER, RTF).
type Run struct {
	Time    string             `json:"time"`
	Label   string             `json:"label"`
	Runtime string             `json:"runtime"`
	Hours   float64            `json:"hours"`
	Seed    int64              `json:"seed"`
	Metrics map[string]float64 `json:"metrics"`
}

// NewRun stamps a run with the current time and the active -hours/-seed.
func NewRun(label, runtime string, metrics map[string]float64) Run {
	return Run{
		Time:    time.Now().UTC().Format(time.RFC3339),
		Label:   label,
		Runtime: runtime,
		Hours:   *sample.Hours,
		Seed:    *sample.Seed,
		Metrics: metrics,
	}
}

// ReadRuns loads the run history (a JSONL file, oldest first). A missing file is
// an empty history, not an error.
func ReadRuns(path string) ([]Run, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var runs []Run
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var r Run
		if err := json.Unmarshal(line, &r); err != nil {
			return nil, fmt.Errorf("parse run log %s: %w", path, err)
		}
		runs = append(runs, r)
	}
	return runs, sc.Err()
}

// AppendRun records a run to the JSONL history and returns the most recent prior
// run with the SAME label (for an apples-to-apples trend), or nil if this is the
// first such run.
func AppendRun(path string, run Run) (prev *Run, err error) {
	history, err := ReadRuns(path)
	if err != nil {
		return nil, err
	}
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Label == run.Label {
			p := history[i]
			prev = &p
			break
		}
	}
	line, err := json.Marshal(run)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		return nil, err
	}
	return prev, nil
}

// Change is one metric's movement between two runs.
type Change struct {
	Name     string
	Previous float64
	Current  float64
	Delta    float64 // current - previous
}

// Improved reports whether the metric got better (lower).
func (c Change) Improved() bool { return c.Delta < 0 }

// Arrow renders the trend direction: ▼ better, ▲ worse, = unchanged.
func (c Change) Arrow() string {
	switch {
	case c.Delta < 0:
		return "▼"
	case c.Delta > 0:
		return "▲"
	default:
		return "="
	}
}

// Compare diffs the current run against a previous one, one Change per metric the
// current run reports, sorted by name. Metrics absent from prev are shown against
// a zero previous (Delta == Current) but you can tell from prev being the zero
// value; new metrics are simply reported.
func Compare(prev *Run, cur Run) []Change {
	names := make([]string, 0, len(cur.Metrics))
	for n := range cur.Metrics {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]Change, 0, len(names))
	for _, n := range names {
		c := Change{Name: n, Current: cur.Metrics[n]}
		if prev != nil {
			if p, ok := prev.Metrics[n]; ok {
				c.Previous = p
				c.Delta = c.Current - p
			}
		}
		out = append(out, c)
	}
	return out
}
