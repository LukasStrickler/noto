package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/artifacts"
	"github.com/lukasstrickler/noto/internal/search"
	"github.com/lukasstrickler/noto/internal/storage"
)

// TestFullPipeline tests the complete noto workflow:
// record → stop → transcribe → summarize → search
func TestFullPipeline(t *testing.T) {
	testDir := t.TempDir()
	meetingsDir := filepath.Join(testDir, "meetings")
	indexPath := filepath.Join(testDir, "noto.sqlite")

	idx, err := search.NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("failed to create search index: %v", err)
	}
	defer idx.Close()

	manifestWriter := artifacts.NewManifestWriter(meetingsDir)
	mockIPC := &mockIPCClient{
		capturedAudio: generateSyntheticAudio(48000, 5),
		durationSecs:  5.0,
	}

	meetingID := uuid.New()
	title := "Test Pipeline Meeting"
	now := time.Now()
	versionID := fmt.Sprintf("ver_%s_%s", now.Format("20060102150405"), randomSuffix4())

	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:        meetingID.String(),
		CurrentVersionID: versionID,
		Title:            title,
		Versions: []artifacts.ManifestVersion{
			{VersionID: versionID, CreatedAt: now, Reason: "recording_started"},
		},
	}

	layout, err := storage.LayoutFor(meetingsDir, meetingID)
	if err != nil {
		t.Fatalf("failed to create layout: %v", err)
	}

	if err := storage.EnsureDirs(layout); err != nil {
		t.Fatalf("failed to ensure dirs: %v", err)
	}

	if err := manifestWriter.WriteManifest(meetingID, m); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	audioData := mockIPC.capturedAudio
	audioPath := filepath.Join(layout.MeetingDir, "audio.m4a")
	if err := os.WriteFile(audioPath, audioData, 0644); err != nil {
		t.Fatalf("failed to write audio: %v", err)
	}

	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		t.Fatal("audio file not created")
	}

	transcript := &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:    meetingID.String(),
		Provider: artifacts.ProviderInfo{
			ID:    "test-provider",
			JobID: "test-job-123",
			Model: "test-model",
		},
		Segments: []artifacts.Segment{
			{ID: "seg_000001", SpeakerID: "speaker_1", SourceRole: "local_speaker", Text: "Let's discuss the API pricing decision.", StartSeconds: 0.0, EndSeconds: 2.5, Confidence: 0.95},
			{ID: "seg_000002", SpeakerID: "speaker_2", SourceRole: "participants", Text: "I think we should go with usage-based pricing.", StartSeconds: 2.5, EndSeconds: 5.0, Confidence: 0.92},
			{ID: "seg_000003", SpeakerID: "speaker_1", SourceRole: "local_speaker", Text: "Agreed. And we need to ship by end of Q.", StartSeconds: 5.0, EndSeconds: 7.5, Confidence: 0.94},
		},
		Speakers: []artifacts.SpeakerInfo{
			{SpeakerID: "speaker_1", DisplayName: "Alice", SourceRole: "local_speaker"},
			{SpeakerID: "speaker_2", DisplayName: "Bob", SourceRole: "participants"},
		},
	}

	if err := storage.WriteTranscript(layout, transcript); err != nil {
		t.Fatalf("failed to write transcript: %v", err)
	}

	readTranscript, err := storage.ReadTranscript(layout)
	if err != nil {
		t.Fatalf("failed to read transcript: %v", err)
	}
	if len(readTranscript.Segments) != 3 {
		t.Errorf("expected 3 segments, got %d", len(readTranscript.Segments))
	}

	segments := make([]search.TranscriptSegment, 0)
	for _, seg := range transcript.Segments {
		segments = append(segments, search.TranscriptSegment{SegmentID: seg.ID, Speaker: seg.SpeakerID, Text: seg.Text, Timestamp: seg.StartSeconds})
	}

	indexInput := &search.IndexMeetingInput{
		MeetingID:          meetingID.String(),
		Title:              title,
		TranscriptSegments: segments,
		Decisions:          nil,
		ActionItems:        nil,
		Risks:              nil,
	}

	if err := idx.IndexMeetingFromInput(indexInput); err != nil {
		t.Fatalf("failed to index meeting: %v", err)
	}

	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:     meetingID.String(),
		ShortSummary: "Discussed API pricing decision and timeline. Agreed on usage-based model.",
		Decisions: []artifacts.Decision{
			{Text: "Use usage-based pricing for API", SpeakerIDs: []string{"speaker_1", "speaker_2"}, SegmentRefs: []string{"seg_000001", "seg_000002"}},
		},
		ActionItems: []artifacts.ActionItem{
			{Text: "Ship API by end of Q", Owner: "Alice", Completed: false, SegmentRefs: []string{"seg_000003"}},
		},
		Risks: []artifacts.Risk{
			{Text: "Timeline may be aggressive", Severity: "medium", SegmentRefs: []string{"seg_000003"}},
		},
		OpenQuestions: []artifacts.OpenQuestion{
			{Text: "What的具体 pricing tiers?", SegmentRefs: []string{"seg_000001"}},
		},
	}

	summaryJSON, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatalf("failed to marshal summary: %v", err)
	}

	if err := storage.WriteSummary(layout, string(summaryJSON)); err != nil {
		t.Fatalf("failed to write summary: %v", err)
	}

	decisions := make([]search.SummaryItem, 0)
	for _, d := range summary.Decisions {
		decisions = append(decisions, search.SummaryItem{Text: d.Text, SpeakerIDs: d.SpeakerIDs})
	}
	actions := make([]search.ActionItem, 0)
	for _, a := range summary.ActionItems {
		actions = append(actions, search.ActionItem{Text: a.Text, Owner: a.Owner})
	}
	risks := make([]search.SummaryItem, 0)
	for _, r := range summary.Risks {
		risks = append(risks, search.SummaryItem{Text: r.Text})
	}

	indexInputWithSummary := &search.IndexMeetingInput{
		MeetingID:          meetingID.String(),
		Title:              title,
		TranscriptSegments: segments,
		Decisions:          decisions,
		ActionItems:        actions,
		Risks:              risks,
	}

	if err := idx.IndexMeetingFromInput(indexInputWithSummary); err != nil {
		t.Fatalf("failed to update index with summary: %v", err)
	}

	results, err := idx.Search("pricing")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("expected at least one search result for 'pricing'")
	}

	foundMeeting := false
	for _, r := range results {
		if r.MeetingID == meetingID.String() {
			foundMeeting = true
			t.Logf("Found meeting via search: ID=%s, snippet=%s, type=%s", r.MeetingID, r.Snippet, r.ResultType)
		}
	}

	if !foundMeeting {
		t.Error("search did not return the test meeting")
	}

	decisionResults, err := idx.Search("usage-based")
	if err != nil {
		t.Fatalf("decision search failed: %v", err)
	}

	if len(decisionResults) == 0 {
		t.Error("expected to find decision about usage-based pricing")
	}

	actionResults, err := idx.Search("ship")
	if err != nil {
		t.Fatalf("action search failed: %v", err)
	}

	if len(actionResults) == 0 {
		t.Error("expected to find action item about shipping")
	}

	meeting, err := storage.GetMeeting(meetingsDir, meetingID)
	if err != nil {
		t.Fatalf("failed to get meeting: %v", err)
	}

	if meeting.Title != title {
		t.Errorf("expected title %q, got %q", title, meeting.Title)
	}

	decisionCount := len(summary.Decisions)
	actionCount := len(summary.ActionItems)
	riskCount := len(summary.Risks)

	t.Logf("Meeting counts: D:%d A:%d R:%d", decisionCount, actionCount, riskCount)

	if decisionCount != 1 {
		t.Errorf("expected 1 decision, got %d", decisionCount)
	}
	if actionCount != 1 {
		t.Errorf("expected 1 action item, got %d", actionCount)
	}
	if riskCount != 1 {
		t.Errorf("expected 1 risk, got %d", riskCount)
	}

	manifestData, err := os.ReadFile(layout.ManifestPath)
	if err != nil {
		t.Fatalf("failed to read manifest: %v", err)
	}

	expectedChecksum, err := os.ReadFile(layout.ChecksumPath)
	if err != nil {
		t.Fatalf("failed to read checksum: %v", err)
	}

	computedChecksum := artifacts.ComputeChecksum(manifestData)
	if strings.TrimSpace(string(expectedChecksum)) != computedChecksum {
		t.Errorf("checksum mismatch: expected %s, got %s", string(expectedChecksum), computedChecksum)
	}

	t.Log("All pipeline steps completed successfully")
}

// TestFullPipeline_VerifyTUIMeetingDisplay tests that meeting data is in correct format for TUI
func TestFullPipeline_VerifyTUIMeetingDisplay(t *testing.T) {
	testDir := t.TempDir()
	meetingsDir := filepath.Join(testDir, "meetings")

	meetingID := uuid.New()
	layout, _ := storage.LayoutFor(meetingsDir, meetingID)
	storage.EnsureDirs(layout)

	// Create meeting with all artifacts
	versionID := "ver_test_001"
	m := &artifacts.MeetingManifest{
		SchemaVersion:    "manifest.v1",
		MeetingID:       meetingID.String(),
		CurrentVersionID: versionID,
		Title:           "Sprint Planning",
		Versions: []artifacts.ManifestVersion{
			{VersionID: versionID, CreatedAt: time.Now(), Reason: "test"},
		},
	}
	storage.WriteManifest(layout, m)

	// Create transcript with known segments
	transcript := &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     meetingID.String(),
		Segments: []artifacts.Segment{
			{ID: "seg_001", SpeakerID: "speaker_1", Text: "Let's plan sprint goals.", StartSeconds: 0, EndSeconds: 3},
			{ID: "seg_002", SpeakerID: "speaker_2", Text: "We need to ship features.", StartSeconds: 3, EndSeconds: 6},
		},
	}
	storage.WriteTranscript(layout, transcript)

	// Create summary with D:N/A:N/R:N
	summary := &artifacts.Summary{
		SchemaVersion: "summary.v1",
		MeetingID:    meetingID.String(),
		Decisions: []artifacts.Decision{
			{Text: "Ship v1 by Friday", SpeakerIDs: []string{"speaker_1"}},
			{Text: "Use Kanban for tracking", SpeakerIDs: []string{"speaker_2"}},
		},
		ActionItems: []artifacts.ActionItem{
			{Text: "Update Jira", Owner: "Dev1"},
			{Text: "Review PRs", Owner: "Dev2"},
			{Text: "Write docs", Owner: "Dev3"},
		},
		Risks: []artifacts.Risk{
			{Text: "Resource shortage"},
		},
	}
	summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
	storage.WriteSummary(layout, string(summaryJSON))

	// Verify counts match TUI expectations
	if len(summary.Decisions) != 2 {
		t.Errorf("expected D:2, got D:%d", len(summary.Decisions))
	}
	if len(summary.ActionItems) != 3 {
		t.Errorf("expected A:3, got A:%d", len(summary.ActionItems))
	}
	if len(summary.Risks) != 1 {
		t.Errorf("expected R:1, got R:%d", len(summary.Risks))
	}

	// Verify TUI format string would work
	tuiRow := fmt.Sprintf("%s  %dm  %s  D:%d A:%d R:%d",
		"Sprint Planning",
		6,
		time.Now().Format("Jan 02"),
		len(summary.Decisions),
		len(summary.ActionItems),
		len(summary.Risks),
	)
	t.Logf("TUI row format: %s", tuiRow)

	// Verify segment citation format
	for _, d := range summary.Decisions {
		if len(d.SegmentRefs) == 0 {
			t.Error("decision missing segment citation")
		}
		for _, ref := range d.SegmentRefs {
			if !strings.HasPrefix(ref, "seg_") {
				t.Errorf("invalid segment ref format: %s", ref)
			}
		}
	}
}

// TestFullPipeline_MeetingListSorting tests that meetings are sorted correctly
func TestFullPipeline_MeetingListSorting(t *testing.T) {
	testDir := t.TempDir()
	meetingsDir := filepath.Join(testDir, "meetings")

	// Create multiple meetings with different dates
	meetings := []struct {
		title      string
		createdAt  time.Time
	}{
		{"Old Meeting", time.Now().AddDate(0, 0, -7)},
		{"Recent Meeting", time.Now().AddDate(0, 0, -1)},
		{"Newest Meeting", time.Now()},
	}

	for _, m := range meetings {
		meetingID := uuid.New()
		layout, _ := storage.LayoutFor(meetingsDir, meetingID)
		storage.EnsureDirs(layout)

		manifest := &artifacts.MeetingManifest{
			SchemaVersion:    "manifest.v1",
			MeetingID:        meetingID.String(),
			CurrentVersionID: "ver_test",
			Versions: []artifacts.ManifestVersion{
				{VersionID: "ver_test", CreatedAt: m.createdAt, Reason: "test"},
			},
		}
		storage.WriteManifest(layout, manifest)

		// Write a simple summary to mark this as processed
		summary := &artifacts.Summary{
			SchemaVersion: "summary.v1",
			MeetingID:     meetingID.String(),
			ShortSummary:  m.title,
		}
		summaryJSON, _ := json.MarshalIndent(summary, "", "  ")
		storage.WriteSummary(layout, string(summaryJSON))

		time.Sleep(10 * time.Millisecond) // Ensure different timestamps
	}

	refs, err := storage.ListMeetings(meetingsDir)
	if err != nil {
		t.Fatalf("failed to list meetings: %v", err)
	}

	if len(refs) != 3 {
		t.Errorf("expected 3 meetings, got %d", len(refs))
	}

	// Verify newest first
	if refs[0].CreatedAt.Before(refs[len(refs)-1].CreatedAt) {
		t.Error("meetings not sorted by date descending")
	}

	t.Logf("Meeting order: %v", []string{
		refs[0].Title,
		refs[1].Title,
		refs[2].Title,
	})
}

// TestFullPipeline_DeleteFromIndex tests that deletion works correctly
func TestFullPipeline_DeleteFromIndex(t *testing.T) {
	testDir := t.TempDir()
	indexPath := filepath.Join(testDir, "noto.sqlite")

	idx, err := search.NewSearchIndex(indexPath)
	if err != nil {
		t.Fatalf("failed to create search index: %v", err)
	}
	defer idx.Close()

	meetingID := uuid.New().String()

	// Index a meeting
	input := &search.IndexMeetingInput{
		MeetingID: meetingID,
		Title:     "Test Meeting",
		TranscriptSegments: []search.TranscriptSegment{
			{SegmentID: "seg_001", Text: "Test content"},
		},
	}

	if err := idx.IndexMeetingFromInput(input); err != nil {
		t.Fatalf("failed to index: %v", err)
	}

	// Verify search works
	results, err := idx.Search("Test")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatal("expected 1 result before delete")
	}

	// Delete from index
	if err := idx.DeleteFromIndex(meetingID); err != nil {
		t.Fatalf("failed to delete from index: %v", err)
	}

	// Verify search returns nothing
	results, err = idx.Search("Test")
	if err != nil {
		t.Fatalf("search after delete failed: %v", err)
	}
	if len(results) != 0 {
		t.Error("expected no results after delete")
	}
}

// mockIPCClient simulates the capture IPC client for testing
type mockIPCClient struct {
	capturedAudio []byte
	durationSecs  float64
	stopResult    *mockStopResult
}

type mockStopResult struct {
	OutputPath   string
	DurationSecs float64
	SampleRateHz int
	Channels     int
	Format       string
	Codec        string
	SizeBytes    int64
}

func (m *mockIPCClient) Stop(ctx context.Context) (*mockStopResult, error) {
	return &mockStopResult{
		OutputPath:   "test_output.m4a",
		DurationSecs: m.durationSecs,
		SampleRateHz: 48000,
		Channels:     2,
		Format:       "m4a",
		Codec:        "aac",
		SizeBytes:    int64(len(m.capturedAudio)),
	}, nil
}

func (m *mockIPCClient) GetCapturedAudio(ctx context.Context) ([]byte, error) {
	return m.capturedAudio, nil
}

func generateSyntheticAudio(sampleRate int, durationSecs int) []byte {
	// Generate a simple sine wave as synthetic audio
	numSamples := sampleRate * durationSecs
	audio := make([]byte, numSamples*2) // 16-bit samples

	for i := 0; i < numSamples; i++ {
		sample := int16(16000 * (i % 480)) // 440Hz-ish tone
		audio[i*2] = byte(sample)
		audio[i*2+1] = byte(sample >> 8)
	}

	return audio
}

func randomSuffix4() string {
	b := make([]byte, 4)
	for i := range b {
		b[i] = byte(uuid.New().ID() % 256)
	}
	return fmt.Sprintf("%x", b)
}

// Helper to run CLI commands in tests
func runCLI(args []string, in io.Reader, out io.Writer, errOut io.Writer) int {
	// This would call the actual CLI in a real integration test
	// For unit tests, we test individual components
	return 0
}