package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Agent spend caps (§8.4 / §6 cost-explosion guard). These bound how much GPU
// money a single agent or hypothesis can burn before a human looks, independent
// of any per-run tier cap.
const (
	AgentDailyCapUSD       = 5.0
	HypothesisWeeklyCapUSD = 15.0
)

const SchemaSpendRecordV1 = "spend_record.v1"

// SpendRecord is one append-only line accounting GPU spend for a billed run.
type SpendRecord struct {
	SchemaVersion    string    `json:"schema_version"`
	RunID            string    `json:"run_id"`
	AgentID          string    `json:"agent_id,omitempty"`
	HypothesisID     string    `json:"hypothesis_id,omitempty"`
	Tier             string    `json:"tier,omitempty"`
	Timestamp        time.Time `json:"timestamp"`
	EstimatedCostUSD float64   `json:"estimated_cost_usd"`
}

// SpendLog is the append-only ledger of GPU spend, kept alongside the decision
// ledger under benchmarks/. It answers the rolling-window questions the runner
// asks before launching a GPU run (§22.14 step 2: CheckSpendCaps).
type SpendLog struct {
	Path             string
	AgentDailyCap    float64
	HypothesisWeekly float64
	now              func() time.Time
}

func NewSpendLog(store *Store) *SpendLog {
	return &SpendLog{
		Path:             filepath.Join(store.Root, "spend.jsonl"),
		AgentDailyCap:    AgentDailyCapUSD,
		HypothesisWeekly: HypothesisWeeklyCapUSD,
		now:              time.Now,
	}
}

// SpendCapError reports a cap that a projected run would exceed.
type SpendCapError struct {
	Scope        string // "agent" | "hypothesis"
	ID           string
	Window       string // "day" | "week"
	SpentUSD     float64
	ProjectedUSD float64
	CapUSD       float64
}

func (e *SpendCapError) Error() string {
	return fmt.Sprintf("%s spend cap exceeded: %s spent $%.2f + projected $%.2f over $%.2f/%s",
		e.Scope, e.ID, e.SpentUSD, e.ProjectedUSD, e.CapUSD, e.Window)
}

// CheckCaps rejects a run whose projected cost would push the agent over its
// daily cap or the hypothesis over its weekly cap. Empty IDs skip the matching
// cap (nothing to attribute), matching the "set NOTO_AGENT_ID for accounting"
// convention without hard-failing un-tagged runs.
func (l *SpendLog) CheckCaps(agentID, hypothesisID string, projectedUSD float64) error {
	now := l.clock()
	records, err := l.ReadAll()
	if err != nil {
		return err
	}
	if agentID != "" {
		spent := sumSpend(records, now.Add(-24*time.Hour), func(r SpendRecord) bool { return r.AgentID == agentID })
		if spent+projectedUSD > l.AgentDailyCap {
			return &SpendCapError{Scope: "agent", ID: agentID, Window: "day", SpentUSD: spent, ProjectedUSD: projectedUSD, CapUSD: l.AgentDailyCap}
		}
	}
	if hypothesisID != "" {
		spent := sumSpend(records, now.Add(-7*24*time.Hour), func(r SpendRecord) bool { return r.HypothesisID == hypothesisID })
		if spent+projectedUSD > l.HypothesisWeekly {
			return &SpendCapError{Scope: "hypothesis", ID: hypothesisID, Window: "week", SpentUSD: spent, ProjectedUSD: projectedUSD, CapUSD: l.HypothesisWeekly}
		}
	}
	return nil
}

// Record appends actual spend after a run completes, stamping schema + time.
func (l *SpendLog) Record(rec SpendRecord) error {
	rec.SchemaVersion = SchemaSpendRecordV1
	if rec.Timestamp.IsZero() {
		rec.Timestamp = l.clock()
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (l *SpendLog) ReadAll() ([]SpendRecord, error) {
	f, err := os.Open(l.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []SpendRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var r SpendRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, sc.Err()
}

func (l *SpendLog) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

func sumSpend(records []SpendRecord, since time.Time, match func(SpendRecord) bool) float64 {
	total := 0.0
	for _, r := range records {
		if r.Timestamp.Before(since) {
			continue
		}
		if match(r) {
			total += r.EstimatedCostUSD
		}
	}
	return total
}
