package server_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/app/host"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestRemoteArtifactRepositoryRoundTrip drives a RemoteArtifactRepository (the
// edge's "store off-site" backend) against a real `noto serve` data plane over
// its /v1/repo/* API, and asserts the written-through artifacts land AND become
// queryable there — i.e. the remote plane stays the source of truth and the
// search index, exactly as if the pipeline had run on it.
func TestRemoteArtifactRepositoryRoundTrip(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	d, err := host.Start(ctx, host.Options{Address: sock})
	if err != nil {
		t.Fatalf("start data plane: %v", err)
	}
	defer d.Close()

	rr := repo.NewRemote("http://unix", "", t.TempDir())
	rr.HTTP = &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dl net.Dialer
				return dl.DialContext(ctx, "unix", sock)
			},
		},
	}

	id := uuid.New()
	if err := rr.CreateMeeting(ctx, id, repo.CreateMeetingOpts{Title: "Remote Standup", Reason: "recorded"}); err != nil {
		t.Fatalf("CreateMeeting: %v", err)
	}

	// Write-through a transcript and read it back verbatim.
	tr := &artifacts.Transcript{
		SchemaVersion: "transcript.v1",
		MeetingID:     id.String(),
		Provider:      artifacts.TranscriptProvider{ID: "parakeet-local"},
		Speakers:      []artifacts.Speaker{{ID: "spk_0", Label: "me", Origin: "local_speaker", ProviderLabel: "A", DisplayName: "You"}},
		Segments: []artifacts.Segment{
			{ID: "seg_0", SpeakerID: "spk_0", SourceRole: "local_speaker", StartSeconds: 0, EndSeconds: 3, Text: "kickoff on the offsite quokka roadmap"},
		},
	}
	if err := rr.SaveTranscript(ctx, id, tr); err != nil {
		t.Fatalf("SaveTranscript: %v", err)
	}
	got, err := rr.LoadTranscript(ctx, id)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if len(got.Segments) != 1 || got.Segments[0].Text != tr.Segments[0].Text {
		t.Errorf("transcript did not round-trip: %+v", got.Segments)
	}

	// Write-through a summary and read it back.
	sum := &artifacts.Summary{SchemaVersion: "summary.v1", MeetingID: id.String(), ShortSummary: "Planned the offsite."}
	if err := rr.SaveSummary(ctx, id, "# Notes\nPlanned the offsite.", sum); err != nil {
		t.Fatalf("SaveSummary: %v", err)
	}
	md, gotSum, err := rr.LoadSummary(ctx, id)
	if err != nil {
		t.Fatalf("LoadSummary: %v", err)
	}
	if gotSum == nil || gotSum.ShortSummary != "Planned the offsite." || md == "" {
		t.Errorf("summary did not round-trip: md=%q sum=%+v", md, gotSum)
	}

	// List + count reflect the write-through meeting.
	metas, err := rr.ListMeetings(ctx)
	if err != nil || len(metas) != 1 || metas[0].Title != "Remote Standup" {
		t.Fatalf("ListMeetings = %+v, err=%v", metas, err)
	}
	if n, err := rr.CountMeetings(ctx); err != nil || n != 1 {
		t.Errorf("CountMeetings = %d, err=%v", n, err)
	}

	// The data plane indexed the write-through transcript: searching it there
	// finds the meeting (proves writes go through the indexing service path, not
	// a bare repo write).
	res, err := d.Client().Search(ctx, notoapi.SearchOpts{Query: "quokka"})
	if err != nil {
		t.Fatalf("data-plane Search: %v", err)
	}
	if len(res.Meetings) == 0 {
		t.Error("write-through transcript was not indexed on the data plane (search found nothing)")
	}

	// Missing meeting → not-found sentinel, so the service's existing handling works.
	if _, err := rr.GetMeeting(ctx, uuid.New()); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("GetMeeting(missing) error = %v, want ErrNotFound", err)
	}

	// Delete removes it from the plane and its index.
	if err := rr.DeleteMeeting(ctx, id); err != nil {
		t.Fatalf("DeleteMeeting: %v", err)
	}
	if n, _ := rr.CountMeetings(ctx); n != 0 {
		t.Errorf("after delete CountMeetings = %d, want 0", n)
	}
}
