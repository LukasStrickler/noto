package repo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
)

// RemoteArtifactRepository writes meeting artifacts through to a remote noto data
// plane over the /v1/repo/* API, satisfying ArtifactRepository. It is the "store
// off-site" backend: the pipeline runs locally (capture + compute), but its
// output — transcript, summary, meeting metadata — lands on the remote backend,
// which stays the single source of truth and the queryable index.
//
// Audio is the deliberate exception: it stays on THIS device, in a local staging
// dir, rather than being shipped up with every recording. That keeps the most
// sensitive, largest artifact private and off the wire by default (a later
// toggle can opt into uploading it for cold archive). So the artifact methods go
// remote; PrepareAudio / AudioPath are local; FilePaths is nil (this backend
// exposes no canonical local filesystem bundle, per the interface contract).
type RemoteArtifactRepository struct {
	base     string
	token    string
	HTTP     *http.Client
	audioDir string
}

var _ ArtifactRepository = (*RemoteArtifactRepository)(nil)

// NewRemote builds a remote artifact repository. baseURL is the data plane's
// root (e.g. https://noto.example.com:8731); token is its bearer token (may be
// empty for a trusted-network endpoint); audioStagingDir is the local directory
// where recorded/imported audio lives while the pipeline runs.
func NewRemote(baseURL, token, audioStagingDir string) *RemoteArtifactRepository {
	return &RemoteArtifactRepository{
		base:     strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		token:    token,
		HTTP:     &http.Client{Timeout: 60 * time.Second},
		audioDir: audioStagingDir,
	}
}

// Wire DTOs for the /v1/repo/* API. Shared with the server handlers so the two
// sides never drift on a field name.

// RepoCreateRequest is the POST /v1/repo/meetings body.
type RepoCreateRequest struct {
	ID   uuid.UUID         `json:"id"`
	Opts CreateMeetingOpts `json:"opts"`
}

// RepoCountResponse is the GET /v1/repo/meetings/count body.
type RepoCountResponse struct {
	Count int `json:"count"`
}

// RepoSummaryBody is the PUT/GET /v1/repo/meetings/{id}/summary body — the
// rendered markdown plus the structured summary (which may be nil).
type RepoSummaryBody struct {
	Markdown string             `json:"markdown"`
	Summary  *artifacts.Summary `json:"summary"`
}

// --- artifact methods: remote ---

func (r *RemoteArtifactRepository) CreateMeeting(ctx context.Context, id uuid.UUID, opts CreateMeetingOpts) error {
	return r.do(ctx, http.MethodPost, "/v1/repo/meetings", RepoCreateRequest{ID: id, Opts: opts}, nil)
}

func (r *RemoteArtifactRepository) GetMeeting(ctx context.Context, id uuid.UUID) (*StoredMeeting, error) {
	var sm StoredMeeting
	if err := r.do(ctx, http.MethodGet, "/v1/repo/meetings/"+id.String(), nil, &sm); err != nil {
		return nil, err
	}
	return &sm, nil
}

func (r *RemoteArtifactRepository) ListMeetings(ctx context.Context) ([]*StoredMeeting, error) {
	var out []*StoredMeeting
	err := r.do(ctx, http.MethodGet, "/v1/repo/meetings", nil, &out)
	return out, err
}

func (r *RemoteArtifactRepository) CountMeetings(ctx context.Context) (int, error) {
	var out RepoCountResponse
	err := r.do(ctx, http.MethodGet, "/v1/repo/meetings/count", nil, &out)
	return out.Count, err
}

func (r *RemoteArtifactRepository) DeleteMeeting(ctx context.Context, id uuid.UUID) error {
	return r.do(ctx, http.MethodDelete, "/v1/repo/meetings/"+id.String(), nil, nil)
}

func (r *RemoteArtifactRepository) SaveTranscript(ctx context.Context, id uuid.UUID, t *artifacts.Transcript) error {
	return r.do(ctx, http.MethodPut, "/v1/repo/meetings/"+id.String()+"/transcript", t, nil)
}

func (r *RemoteArtifactRepository) LoadTranscript(ctx context.Context, id uuid.UUID) (*artifacts.Transcript, error) {
	var t artifacts.Transcript
	if err := r.do(ctx, http.MethodGet, "/v1/repo/meetings/"+id.String()+"/transcript", nil, &t); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *RemoteArtifactRepository) SaveSummary(ctx context.Context, id uuid.UUID, md string, summary *artifacts.Summary) error {
	return r.do(ctx, http.MethodPut, "/v1/repo/meetings/"+id.String()+"/summary", RepoSummaryBody{Markdown: md, Summary: summary}, nil)
}

func (r *RemoteArtifactRepository) LoadSummary(ctx context.Context, id uuid.UUID) (string, *artifacts.Summary, error) {
	var b RepoSummaryBody
	if err := r.do(ctx, http.MethodGet, "/v1/repo/meetings/"+id.String()+"/summary", nil, &b); err != nil {
		return "", nil, err
	}
	return b.Markdown, b.Summary, nil
}

func (r *RemoteArtifactRepository) VerifyIntegrity(ctx context.Context, id uuid.UUID) error {
	return r.do(ctx, http.MethodPost, "/v1/repo/meetings/"+id.String()+"/verify", nil, nil)
}

// --- audio methods: local staging (audio stays on-device) ---

func (r *RemoteArtifactRepository) PrepareAudio(_ context.Context, id uuid.UUID, ext string) (string, error) {
	if ext == "" {
		ext = ".m4a"
	}
	dir := filepath.Join(r.audioDir, id.String())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("repo: prepare audio staging dir: %w", err)
	}
	return filepath.Join(dir, "audio"+ext), nil
}

func (r *RemoteArtifactRepository) AudioPath(id uuid.UUID) (string, bool) {
	dir := filepath.Join(r.audioDir, id.String())
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audio") {
			return filepath.Join(dir, e.Name()), true
		}
	}
	return "", false
}

// FilePaths returns nil: a remote-backed store exposes no canonical local
// filesystem bundle (the interface contract for non-filesystem backends).
func (r *RemoteArtifactRepository) FilePaths(_ uuid.UUID) *MeetingFilePaths { return nil }

// --- transport ---

func (r *RemoteArtifactRepository) do(ctx context.Context, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("repo: encode %s %s: %w", method, path, err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, r.base+path, rdr)
	if err != nil {
		return fmt.Errorf("repo: build %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.token != "" {
		req.Header.Set("Authorization", "Bearer "+r.token)
	}
	resp, err := r.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("repo: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Map the data plane's 404 onto the interface's not-found sentinel so
		// the service's existing not-found handling works unchanged.
		return ErrNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return remoteRepoError(method, path, resp)
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("repo: decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

func remoteRepoError(method, path string, resp *http.Response) error {
	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(body, &env)
	if env.Error != nil && env.Error.Message != "" {
		return fmt.Errorf("repo: %s %s: remote %s: %s", method, path, env.Error.Code, env.Error.Message)
	}
	return fmt.Errorf("repo: %s %s: remote status %d", method, path, resp.StatusCode)
}
