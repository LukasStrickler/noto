package bench

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// LedgerEntry is ledger_entry.v1.
type LedgerEntry struct {
	SchemaVersion                string             `json:"schema_version"`
	ExperimentID                 string             `json:"experiment_id,omitempty"`
	RunID                        string             `json:"run_id"`
	BaselineRunID                string             `json:"baseline_run_id,omitempty"`
	HypothesisID                 string             `json:"hypothesis_id,omitempty"`
	ProgramStep                  string             `json:"program_step,omitempty"`
	ExecutionProfile             string             `json:"execution_profile"`
	OperatingMode                string             `json:"operating_mode"`
	Decision                     string             `json:"decision"`
	AgentID                      string             `json:"agent_id,omitempty"`
	HistoricalUnverified         bool               `json:"historical_unverified"`
	CostPerProcessedAudioHourUSD float64            `json:"cost_per_processed_audio_hour_usd,omitempty"`
	MetricsDelta                 map[string]float64 `json:"metrics_delta,omitempty"`
	ArtifactDir                  string             `json:"artifact_dir,omitempty"`
}

const SchemaLedgerEntryV1 = "ledger_entry.v1"

// Ledger is append-only benchmark decisions.
type Ledger struct {
	Path string
}

func NewLedger(store *Store) *Ledger {
	return &Ledger{Path: filepath.Join(store.Root, "ledger.jsonl")}
}

func (l *Ledger) Append(entry LedgerEntry) error {
	if entry.SchemaVersion == "" {
		entry.SchemaVersion = SchemaLedgerEntryV1
	}
	if entry.Decision == "adopt" && entry.HistoricalUnverified {
		return fmt.Errorf("cannot adopt historical_unverified entry")
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

func (l *Ledger) ReadAll() ([]LedgerEntry, error) {
	f, err := os.Open(l.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var entries []LedgerEntry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var e LedgerEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, sc.Err()
}

// WinnerKey identifies a ledger winner slot.
type WinnerKey struct {
	Profile string
	Mode    string
}

// Winners returns latest adopt entry per (profile, mode).
func (l *Ledger) Winners() (map[WinnerKey]LedgerEntry, error) {
	entries, err := l.ReadAll()
	if err != nil {
		return nil, err
	}
	winners := map[WinnerKey]LedgerEntry{}
	for _, e := range entries {
		if e.Decision != "adopt" || e.HistoricalUnverified {
			continue
		}
		k := WinnerKey{Profile: e.ExecutionProfile, Mode: e.OperatingMode}
		winners[k] = e
	}
	return winners, nil
}
