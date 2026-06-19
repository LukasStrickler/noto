package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// updateGolden regenerates the .json.golden fixtures:
//
//	go test ./internal/cli/ -run TestCLIJSON -update
var updateGolden = flag.Bool("update", false, "update CLI --json golden files")

// seededGoldenClient is a deterministic backend for the --json contract
// fixtures. Every field is fixed (no time.Now, no PID) so the golden
// output is stable across runs and machines.
func seededGoldenClient() *testutil.FakeClient {
	fc := testutil.NewFakeClient()
	created := time.Date(2026, 5, 20, 9, 30, 0, 0, time.UTC)

	m := notoapi.Meeting{
		ID: "mtg-1", Title: "Quarterly planning",
		Status: notoapi.StatusSummarized, CreatedAt: created,
		DurationSeconds: 1830, DecisionCount: 2, ActionCount: 3, RiskCount: 1,
		ShortSummary: "Planned the quarter.",
	}
	fc.Meetings = []notoapi.Meeting{m}
	fc.MeetingByID["mtg-1"] = m
	fc.Transcripts["mtg-1"] = notoapi.Transcript{
		MeetingID: "mtg-1",
		Segments: []notoapi.TranscriptSegment{
			{ID: "s1", SpeakerID: "spk1", Speaker: "Maya", Role: "local_speaker", StartSec: 1, EndSec: 4, Text: "Let's start."},
		},
	}
	fc.Summaries["mtg-1"] = notoapi.Summary{MeetingID: "mtg-1", ShortSummary: "Planned the quarter.", Markdown: "# Summary\n\nPlanned the quarter."}
	fc.Files["mtg-1"] = notoapi.MeetingFiles{MeetingID: "mtg-1", Transcript: "/n/transcript.json", SummaryMD: "/n/summary.md"}
	fc.Handoffs["mtg-1"] = notoapi.AgentHandoff{MeetingID: "mtg-1", VersionID: "ver-1", Files: fc.Files["mtg-1"], Commands: []string{"noto transcript --json mtg-1"}}
	fc.Providers = []notoapi.ProviderInfo{{ID: "parakeet-local", DisplayName: "Parakeet TDT (local)", Kind: "stt", Capabilities: []string{"transcribe", "word_timestamps"}}}
	fc.Jobs = []notoapi.Job{{ID: "job-1", Kind: notoapi.JobTranscribe, Status: notoapi.JobSucceeded, Progress: 1}}
	fc.HealthInfo = notoapi.Health{OK: true, Version: "golden", StartedAt: created}
	fc.SystemInfo = notoapi.System{
		SchemaVersion: "system.v1", Mode: "local", Hostname: "golden-host",
		OS: "darwin", Arch: "arm64", Accelerator: "coreml", AcceleratorOK: true,
		ModelTier: "accurate", StorageType: "local", CaptureAvailable: true, Version: "golden",
	}
	fc.Recording = notoapi.RecordingState{Active: false}
	fc.SearchValue = notoapi.SearchResult{Hits: []notoapi.SearchHit{{MeetingID: "mtg-1", MeetingTitle: "Quarterly planning", Speaker: "Maya", Timestamp: 12, Snippet: "planning the quarter"}}}
	return fc
}

// TestCLIJSON locks the --json output of every agent-facing read command.
// The shape of these envelopes is the contract AI agents consume, so a
// change here should be a deliberate, reviewed golden update.
func TestCLIJSON(t *testing.T) {
	cases := []struct {
		name string
		run  func(a *app) int
	}{
		{"list", func(a *app) int { return a.runList([]string{"--json"}) }},
		{"show", func(a *app) int { return a.runShow([]string{"mtg-1", "--json"}) }},
		{"search", func(a *app) int { return a.runSearch([]string{"--json", "planning"}) }},
		{"transcript", func(a *app) int { return a.runTranscript([]string{"mtg-1", "--json"}) }},
		{"summary", func(a *app) int { return a.runSummary([]string{"mtg-1", "--json"}) }},
		{"files", func(a *app) int { return a.runFiles([]string{"mtg-1"}) }},
		{"agent", func(a *app) int { return a.runAgent([]string{"mtg-1"}) }},
		{"status", func(a *app) int { return a.runStatus([]string{"--json"}) }},
		{"providers", func(a *app) int { return a.runProviders([]string{"list", "--json"}) }},
		{"jobs", func(a *app) int { return a.runJobs([]string{"--json"}) }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := seededGoldenClient()
			var out, errOut bytes.Buffer
			a := &app{
				out:    &out,
				errOut: &errOut,
				connectFn: func(context.Context) (notoapi.Client, func(), int) {
					return fc, func() {}, 0
				},
			}

			if code := tc.run(a); code != 0 {
				t.Fatalf("%s exit %d; stderr=%s", tc.name, code, errOut.String())
			}
			got := out.Bytes()

			// Every read command's --json output must be valid JSON — this
			// alone catches a command that forgot to wire up --json.
			if !json.Valid(got) {
				t.Fatalf("%s --json produced invalid JSON:\n%s", tc.name, got)
			}

			golden := filepath.Join("testdata", "json", tc.name+".json.golden")
			if *updateGolden {
				if err := os.MkdirAll(filepath.Dir(golden), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(golden, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("read golden (run with -update to create): %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("%s --json drifted from golden.\n--- got ---\n%s\n--- want ---\n%s", tc.name, got, want)
			}
		})
	}
}
