package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/notoapi"
	"github.com/lukasstrickler/noto/internal/notohost"
)

func TestSpeakerProfileRoutes(t *testing.T) {
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

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}

	baseURL := "http://unix"
	prof1 := createSpeakerProfile(t, httpClient, baseURL, "Alice", "alice@example.com")
	prof2 := createSpeakerProfile(t, httpClient, baseURL, "Bob", "bob@example.com")

	listProfiles(t, httpClient, baseURL, 2)

	getProfile(t, httpClient, baseURL, prof1.ID, "Alice")

	displayName := "Alice Updated"
	patchProfile(t, httpClient, baseURL, prof1.ID, displayName, "alice@example.com")

	deleteProfile(t, httpClient, baseURL, prof2.ID)

	listProfiles(t, httpClient, baseURL, 1)
}

func createSpeakerProfile(t *testing.T, hc *http.Client, baseURL, name, email string) notoapi.SpeakerProfile {
	body, _ := json.Marshal(notoapi.CreateSpeakerProfileRequest{
		DisplayName: name,
		Email:       email,
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/speaker-profiles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("CreateSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("expected 201, got %d", resp.StatusCode)
	}
	var p notoapi.SpeakerProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

func listProfiles(t *testing.T, hc *http.Client, baseURL string, expect int) {
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/speaker-profiles", nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("ListSpeakerProfiles request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var r notoapi.ListSpeakerProfilesResult
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if r.Total != expect {
		t.Errorf("expected %d profiles, got %d", expect, r.Total)
	}
}

func getProfile(t *testing.T, hc *http.Client, baseURL, id, expectName string) {
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/speaker-profiles/"+id, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("GetSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var p notoapi.SpeakerProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if p.DisplayName != expectName {
		t.Errorf("expected %q, got %q", expectName, p.DisplayName)
	}
}

func patchProfile(t *testing.T, hc *http.Client, baseURL, id string, displayName, email string) notoapi.SpeakerProfile {
	patch := notoapi.SpeakerProfilePatch{}
	if displayName != "" {
		patch.DisplayName = &displayName
	}
	if email != "" {
		patch.Email = &email
	}
	body, _ := json.Marshal(patch)
	req, _ := http.NewRequest(http.MethodPatch, baseURL+"/v1/speaker-profiles/"+id, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("PatchSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var p notoapi.SpeakerProfile
	if err := json.NewDecoder(resp.Body).Decode(&p); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return p
}

func deleteProfile(t *testing.T, hc *http.Client, baseURL, id string) {
	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/v1/speaker-profiles/"+id, nil)
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("DeleteSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", resp.StatusCode)
	}
}

func TestSpeakerProfileRouteNotFound(t *testing.T) {
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

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	baseURL := "http://unix"

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/speaker-profiles/nonexistent-id", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GetSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid UUID, got %d", resp.StatusCode)
	}
}

func TestSpeakerProfileMergeRoute(t *testing.T) {
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

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	baseURL := "http://unix"

	p1 := createSpeakerProfile(t, httpClient, baseURL, "Alice", "alice@example.com")
	p2 := createSpeakerProfile(t, httpClient, baseURL, "Bob", "")

	body, _ := json.Marshal(notoapi.MergeSpeakerProfilesRequest{
		TargetProfileID: p1.ID,
		SourceProfileID: p2.ID,
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/speaker-profiles/"+p1.ID+"/merge", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("MergeSpeakerProfiles request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var merged notoapi.SpeakerProfile
	if err := json.NewDecoder(resp.Body).Decode(&merged); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if merged.ID != p1.ID {
		t.Errorf("expected target id %s, got %s", p1.ID, merged.ID)
	}
}

func TestMeetingSpeakerMappingsRoute(t *testing.T) {
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

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	baseURL := "http://unix"

	req, _ := http.NewRequest(http.MethodGet, baseURL+"/v1/meetings/test-meeting-id/speaker-mappings", nil)
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("GetMeetingSpeakerMappings request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var mappings notoapi.MeetingSpeakerMappings
	if err := json.NewDecoder(resp.Body).Decode(&mappings); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if mappings.MeetingID != "test-meeting-id" {
		t.Errorf("expected meeting id, got %q", mappings.MeetingID)
	}
}

func TestSpeakerProfileCreateValidation(t *testing.T) {
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

	httpClient := &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, "unix", sock)
			},
		},
	}
	baseURL := "http://unix"

	body, _ := json.Marshal(notoapi.CreateSpeakerProfileRequest{
		DisplayName: "",
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/v1/speaker-profiles", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("CreateSpeakerProfile request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", resp.StatusCode)
	}
}

var _ = os.MkdirAll
