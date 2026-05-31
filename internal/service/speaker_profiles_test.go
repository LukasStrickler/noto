package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/data"
	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/notohost"
)

func TestSpeakerProfileCRUD(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	profiles, err := client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles, got %d", len(profiles))
	}

	p, err := client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "Alice",
		Email:       "alice@example.com",
		Pronouns:    "she/her",
	})
	if err != nil {
		t.Fatalf("CreateSpeakerProfile: %v", err)
	}
	if p.ID == "" {
		t.Fatal("missing profile id")
	}
	if p.DisplayName != "Alice" {
		t.Errorf("expected DisplayName Alice, got %q", p.DisplayName)
	}
	if p.Email != "alice@example.com" {
		t.Errorf("expected email, got %q", p.Email)
	}
	if p.Pronouns != "she/her" {
		t.Errorf("expected pronouns, got %q", p.Pronouns)
	}

	p2, err := client.GetSpeakerProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetSpeakerProfile: %v", err)
	}
	if p2.ID != p.ID {
		t.Errorf("expected id %s, got %s", p.ID, p2.ID)
	}

	displayName := "Alice Updated"
	p3, err := client.PatchSpeakerProfile(ctx, p.ID, notoapi.SpeakerProfilePatch{
		DisplayName: &displayName,
	})
	if err != nil {
		t.Fatalf("PatchSpeakerProfile: %v", err)
	}
	if p3.DisplayName != "Alice Updated" {
		t.Errorf("expected updated name, got %q", p3.DisplayName)
	}

	profiles, err = client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles after create: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile, got %d", len(profiles))
	}

	err = client.DeleteSpeakerProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("DeleteSpeakerProfile: %v", err)
	}

	profiles, err = client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles after delete: %v", err)
	}
	if len(profiles) != 0 {
		t.Fatalf("expected 0 profiles after delete, got %d", len(profiles))
	}
}

func TestSpeakerProfileMerge(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	p1, err := client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "Alice",
	})
	if err != nil {
		t.Fatalf("CreateSpeakerProfile p1: %v", err)
	}

	p2, err := client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "Bob",
		Pronouns:    "she/her",
	})
	if err != nil {
		t.Fatalf("CreateSpeakerProfile p2: %v", err)
	}

	merged, err := client.MergeSpeakerProfiles(ctx, p1.ID, p2.ID)
	if err != nil {
		t.Fatalf("MergeSpeakerProfiles: %v", err)
	}
	if merged.DisplayName != "Alice" {
		t.Errorf("expected DisplayName Alice (not overwritten), got %q", merged.DisplayName)
	}
	if merged.Pronouns != "she/her" {
		t.Errorf("expected pronouns from p2, got %q", merged.Pronouns)
	}

	profiles, err := client.ListSpeakerProfiles(ctx)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles after merge: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("expected 1 profile after merge, got %d", len(profiles))
	}
}

func TestSpeakerProfileMergeSameID(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	p, err := client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "Bob",
	})
	if err != nil {
		t.Fatalf("CreateSpeakerProfile: %v", err)
	}

	_, err = client.MergeSpeakerProfiles(ctx, p.ID, p.ID)
	if err == nil {
		t.Fatal("expected error when merging same profile")
	}
}

func TestSpeakerProfileInvalidID(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	_, err = client.GetSpeakerProfile(ctx, "not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}

	_, err = client.PatchSpeakerProfile(ctx, "not-a-uuid", notoapi.SpeakerProfilePatch{})
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}

	err = client.DeleteSpeakerProfile(ctx, "not-a-uuid")
	if err == nil {
		t.Fatal("expected error for invalid UUID")
	}
}

func TestSpeakerProfileCreateEmptyName(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	_, err = client.CreateSpeakerProfile(ctx, notoapi.CreateSpeakerProfileRequest{
		DisplayName: "",
	})
	if err == nil {
		t.Fatal("expected error for empty display name")
	}
}

func TestMeetingSpeakerMappingsEmpty(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	mappings, err := client.GetMeetingSpeakerMappings(ctx, "meeting-without-mappings")
	if err != nil {
		t.Fatalf("GetMeetingSpeakerMappings: %v", err)
	}
	if mappings.MeetingID != "meeting-without-mappings" {
		t.Errorf("expected meeting id, got %q", mappings.MeetingID)
	}
	if len(mappings.Mappings) != 0 {
		t.Fatalf("expected 0 mappings, got %d", len(mappings.Mappings))
	}
}

func TestMeetingSpeakerMappingsPatchNotFound(t *testing.T) {
	t.Setenv("NOTO_CONFIG_DIR", t.TempDir())
	t.Setenv("NOTO_ARTIFACT_ROOT", t.TempDir())
	sock := filepath.Join(t.TempDir(), "noto.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	host, err := notohost.Start(ctx, notohost.Options{Address: sock})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer host.Close()
	client := host.Client()

	profileID := "test-profile-id"
	_, err = client.PatchMeetingSpeakerMappings(ctx, "some-meeting", notoapi.MeetingSpeakerMappingsPatch{
		Mappings: []notoapi.MeetingSpeakerMappingPatchEntry{
			{
				MeetingSpeakerID: "A",
				ProfileID:        &profileID,
			},
		},
	})
	if err == nil {
		t.Fatal("expected error when mapping not found")
	}
}

var _ = data.DB{}
var _ = os.MkdirAll
