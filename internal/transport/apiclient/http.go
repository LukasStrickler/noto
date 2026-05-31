package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// HTTPOptions configure a client that talks to a noto server over HTTP.
type HTTPOptions struct {
	// BaseURL like "http://unix/v1" (for UDS) or "http://host:port" (TCP).
	BaseURL string
	// SocketPath set when talking to a UDS server. When empty, BaseURL
	// is used directly.
	SocketPath string
	// Token bearer token for remote TCP servers.
	Token string
	// Timeout for non-streaming requests.
	Timeout time.Duration
}

// Compile-time proof that httpClient satisfies the full Client contract.
var _ notoapi.Client = (*httpClient)(nil)

// NewHTTP returns a notoapi.Client that talks HTTP/JSON+SSE.
func NewHTTP(opts HTTPOptions) notoapi.Client {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	transport := &http.Transport{}
	if opts.SocketPath != "" {
		sp := opts.SocketPath
		transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sp)
		}
		if opts.BaseURL == "" {
			opts.BaseURL = "http://unix"
		}
	}
	hc := &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
	// For SSE we need a client with no deadline.
	stream := &http.Client{Transport: transport}

	return &httpClient{
		base:             strings.TrimRight(opts.BaseURL, "/"),
		token:            opts.Token,
		client:           hc,
		stream:           stream,
		heartbeatTimeout: defaultHeartbeatTimeout,
	}
}

type httpClient struct {
	base             string
	token            string
	client           *http.Client
	stream           *http.Client
	heartbeatTimeout time.Duration
}

// do issues a JSON request and unmarshals the response into out.
func (c *httpClient) do(ctx context.Context, method, path string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var env notoapi.ErrorEnvelope
		_ = json.NewDecoder(resp.Body).Decode(&env)
		if env.Error != nil {
			return env.Error
		}
		return fmt.Errorf("noto api: %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---- Meetings ----

func (c *httpClient) ListMeetings(ctx context.Context, opts notoapi.ListMeetingsOpts) (notoapi.ListMeetingsResult, error) {
	q := url.Values{}
	if opts.Query != "" {
		q.Set("q", opts.Query)
	}
	if opts.Since != "" {
		q.Set("since", opts.Since)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out notoapi.ListMeetingsResult
	path := "/v1/meetings"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *httpClient) GetMeeting(ctx context.Context, id string) (notoapi.Meeting, error) {
	var out notoapi.Meeting
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+id, nil, &out)
	return out, err
}
func (c *httpClient) GetTranscript(ctx context.Context, id string) (notoapi.Transcript, error) {
	var out notoapi.Transcript
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+id+"/transcript", nil, &out)
	return out, err
}
func (c *httpClient) GetSummary(ctx context.Context, id string) (notoapi.Summary, error) {
	var out notoapi.Summary
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+id+"/summary", nil, &out)
	return out, err
}
func (c *httpClient) GetMeetingFiles(ctx context.Context, id string) (notoapi.MeetingFiles, error) {
	var out notoapi.MeetingFiles
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+id+"/files", nil, &out)
	return out, err
}
func (c *httpClient) GetAgentHandoff(ctx context.Context, id string) (notoapi.AgentHandoff, error) {
	var out notoapi.AgentHandoff
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+id+"/agent", nil, &out)
	return out, err
}
func (c *httpClient) DeleteMeeting(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/meetings/"+id, nil, nil)
}
func (c *httpClient) VerifyMeeting(ctx context.Context, id string) (notoapi.Job, error) {
	var out notoapi.Job
	err := c.do(ctx, http.MethodPost, "/v1/meetings/"+id+"/verify", nil, &out)
	return out, err
}

func (c *httpClient) UpdateSpeakerName(ctx context.Context, meetingID, speakerID, displayName string) error {
	body := struct {
		DisplayName string `json:"display_name"`
	}{DisplayName: displayName}
	return c.do(ctx, http.MethodPost, "/v1/meetings/"+meetingID+"/speakers/"+speakerID, body, nil)
}

// ---- Search ----

func (c *httpClient) Search(ctx context.Context, opts notoapi.SearchOpts) (notoapi.SearchResult, error) {
	q := url.Values{}
	q.Set("q", opts.Query)
	if opts.Speaker != "" {
		q.Set("speaker", opts.Speaker)
	}
	if opts.Scope != "" {
		q.Set("scope", opts.Scope)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	var out notoapi.SearchResult
	err := c.do(ctx, http.MethodGet, "/v1/search?"+q.Encode(), nil, &out)
	return out, err
}

// ---- Recording ----

func (c *httpClient) ImportAudio(ctx context.Context, opts notoapi.ImportAudioOpts) (notoapi.ImportAudioResult, error) {
	var out notoapi.ImportAudioResult
	err := c.do(ctx, http.MethodPost, "/v1/imports/audio", opts, &out)
	return out, err
}
func (c *httpClient) StartRecording(ctx context.Context, opts notoapi.StartRecordingOpts) (notoapi.StartRecordingResult, error) {
	var out notoapi.StartRecordingResult
	err := c.do(ctx, http.MethodPost, "/v1/recording/start", opts, &out)
	return out, err
}
func (c *httpClient) StopRecording(ctx context.Context, opts notoapi.StopRecordingOpts) (notoapi.StopRecordingResult, error) {
	var out notoapi.StopRecordingResult
	err := c.do(ctx, http.MethodPost, "/v1/recording/stop", opts, &out)
	return out, err
}
func (c *httpClient) PauseRecording(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/recording/pause", nil, nil)
}
func (c *httpClient) ResumeRecording(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/v1/recording/resume", nil, nil)
}
func (c *httpClient) AddMarker(ctx context.Context, label string) error {
	return c.do(ctx, http.MethodPost, "/v1/recording/marker", map[string]string{"label": label}, nil)
}
func (c *httpClient) PreflightRecording(ctx context.Context) (notoapi.PreflightResult, error) {
	var out notoapi.PreflightResult
	err := c.do(ctx, http.MethodPost, "/v1/recording/preflight", nil, &out)
	return out, err
}
func (c *httpClient) GetRecording(ctx context.Context) (notoapi.RecordingState, error) {
	var out notoapi.RecordingState
	err := c.do(ctx, http.MethodGet, "/v1/recording", nil, &out)
	return out, err
}
func (c *httpClient) StreamMeters(ctx context.Context) (<-chan notoapi.MeterEvent, error) {
	events, err := c.streamSSE(ctx, "/v1/recording/meters")
	if err != nil {
		return nil, err
	}
	out := make(chan notoapi.MeterEvent, 16)
	go func() {
		defer close(out)
		for ev := range events {
			if ev.Kind == notoapi.EventMeter && ev.Meter != nil {
				select {
				case out <- *ev.Meter:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out, nil
}

// ---- Jobs ----

func (c *httpClient) CreateJob(ctx context.Context, opts notoapi.CreateJobOpts) (notoapi.Job, error) {
	var out notoapi.Job
	err := c.do(ctx, http.MethodPost, "/v1/jobs", opts, &out)
	return out, err
}
func (c *httpClient) ListJobs(ctx context.Context, opts notoapi.ListJobsOpts) ([]notoapi.Job, error) {
	q := url.Values{}
	if opts.Status != "" {
		q.Set("status", string(opts.Status))
	}
	if opts.MeetingID != "" {
		q.Set("meeting_id", opts.MeetingID)
	}
	if opts.Limit > 0 {
		q.Set("limit", strconv.Itoa(opts.Limit))
	}
	path := "/v1/jobs"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var out []notoapi.Job
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}
func (c *httpClient) GetJob(ctx context.Context, id string) (notoapi.Job, error) {
	var out notoapi.Job
	err := c.do(ctx, http.MethodGet, "/v1/jobs/"+id, nil, &out)
	return out, err
}
func (c *httpClient) CancelJob(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/jobs/"+id, nil, nil)
}
func (c *httpClient) StreamEvents(ctx context.Context) (<-chan notoapi.Event, error) {
	return c.streamSSE(ctx, "/v1/jobs/events")
}

// ---- Providers ----

func (c *httpClient) ListProviders(ctx context.Context) ([]notoapi.ProviderInfo, error) {
	var out []notoapi.ProviderInfo
	err := c.do(ctx, http.MethodGet, "/v1/providers", nil, &out)
	return out, err
}
func (c *httpClient) SetProviderKey(ctx context.Context, providerID, value string) error {
	return c.do(ctx, http.MethodPost, "/v1/providers/"+providerID+"/key", map[string]string{"value": value}, nil)
}
func (c *httpClient) DeleteProviderKey(ctx context.Context, providerID string) error {
	return c.do(ctx, http.MethodDelete, "/v1/providers/"+providerID+"/key", nil, nil)
}
func (c *httpClient) TestProvider(ctx context.Context, providerID string) (notoapi.TestProviderResult, error) {
	var out notoapi.TestProviderResult
	err := c.do(ctx, http.MethodPost, "/v1/providers/"+providerID+"/test", nil, &out)
	return out, err
}
func (c *httpClient) SetActiveSpeech(ctx context.Context, providerID string) error {
	return c.do(ctx, http.MethodPut, "/v1/providers/"+providerID+"/active-speech", nil, nil)
}
func (c *httpClient) SetActiveLLMModel(ctx context.Context, modelID string) error {
	// Match the direct client: reject an empty model id up front rather than
	// PATCHing config, where a blank LLMModel is silently dropped as a no-op.
	if strings.TrimSpace(modelID) == "" {
		return notoapi.NewError(notoapi.CodeInvalidRequest, "model id is empty", nil)
	}
	patch := notoapi.ConfigPatch{Routing: &notoapi.ConfigRouting{LLMProvider: "openrouter", LLMModel: modelID}}
	return c.do(ctx, http.MethodPatch, "/v1/config", patch, nil)
}

// ---- Config / Storage / Health ----

func (c *httpClient) GetConfig(ctx context.Context) (notoapi.Config, error) {
	var out notoapi.Config
	err := c.do(ctx, http.MethodGet, "/v1/config", nil, &out)
	return out, err
}
func (c *httpClient) PatchConfig(ctx context.Context, patch notoapi.ConfigPatch) (notoapi.Config, error) {
	var out notoapi.Config
	err := c.do(ctx, http.MethodPatch, "/v1/config", patch, &out)
	return out, err
}
func (c *httpClient) GetPaths(ctx context.Context) (notoapi.Paths, error) {
	var out notoapi.Paths
	err := c.do(ctx, http.MethodGet, "/v1/config/paths", nil, &out)
	return out, err
}

func (c *httpClient) GetStorage(ctx context.Context) (notoapi.Storage, error) {
	var out notoapi.Storage
	err := c.do(ctx, http.MethodGet, "/v1/storage", nil, &out)
	return out, err
}
func (c *httpClient) VerifyStorage(ctx context.Context) (notoapi.Job, error) {
	var out notoapi.Job
	err := c.do(ctx, http.MethodPost, "/v1/storage/verify", nil, &out)
	return out, err
}
func (c *httpClient) ReindexStorage(ctx context.Context) (notoapi.Job, error) {
	var out notoapi.Job
	err := c.do(ctx, http.MethodPost, "/v1/storage/reindex", nil, &out)
	return out, err
}
func (c *httpClient) Health(ctx context.Context) (notoapi.Health, error) {
	var out notoapi.Health
	err := c.do(ctx, http.MethodGet, "/v1/healthz", nil, &out)
	return out, err
}

func (c *httpClient) Close() error { return nil }

// ---- Speaker Profiles ----

func (c *httpClient) ListSpeakerProfiles(ctx context.Context) ([]notoapi.SpeakerProfile, error) {
	var out notoapi.ListSpeakerProfilesResult
	err := c.do(ctx, http.MethodGet, "/v1/speaker-profiles", nil, &out)
	return out.Profiles, err
}

func (c *httpClient) GetSpeakerProfile(ctx context.Context, id string) (notoapi.SpeakerProfile, error) {
	var out notoapi.SpeakerProfile
	err := c.do(ctx, http.MethodGet, "/v1/speaker-profiles/"+id, nil, &out)
	return out, err
}

func (c *httpClient) CreateSpeakerProfile(ctx context.Context, req notoapi.CreateSpeakerProfileRequest) (notoapi.SpeakerProfile, error) {
	var out notoapi.SpeakerProfile
	err := c.do(ctx, http.MethodPost, "/v1/speaker-profiles", req, &out)
	return out, err
}

func (c *httpClient) PatchSpeakerProfile(ctx context.Context, id string, patch notoapi.SpeakerProfilePatch) (notoapi.SpeakerProfile, error) {
	var out notoapi.SpeakerProfile
	err := c.do(ctx, http.MethodPatch, "/v1/speaker-profiles/"+id, patch, &out)
	return out, err
}

func (c *httpClient) DeleteSpeakerProfile(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/v1/speaker-profiles/"+id, nil, nil)
}

func (c *httpClient) MergeSpeakerProfiles(ctx context.Context, targetID, sourceID string) (notoapi.SpeakerProfile, error) {
	var out notoapi.SpeakerProfile
	req := notoapi.MergeSpeakerProfilesRequest{TargetProfileID: targetID, SourceProfileID: sourceID}
	err := c.do(ctx, http.MethodPost, "/v1/speaker-profiles/"+targetID+"/merge", req, &out)
	return out, err
}

// ---- Meeting Speaker Mappings ----

func (c *httpClient) GetMeetingSpeakerMappings(ctx context.Context, meetingID string) (notoapi.MeetingSpeakerMappings, error) {
	var out notoapi.MeetingSpeakerMappings
	err := c.do(ctx, http.MethodGet, "/v1/meetings/"+meetingID+"/speaker-mappings", nil, &out)
	return out, err
}

func (c *httpClient) PatchMeetingSpeakerMappings(ctx context.Context, meetingID string, patch notoapi.MeetingSpeakerMappingsPatch) (notoapi.MeetingSpeakerMappings, error) {
	var out notoapi.MeetingSpeakerMappings
	err := c.do(ctx, http.MethodPatch, "/v1/meetings/"+meetingID+"/speaker-mappings", patch, &out)
	return out, err
}

// ---- Agent API ----

func (c *httpClient) AgentListMeetings(ctx context.Context, opts notoapi.AgentListOpts) (notoapi.AgentListResult, error) {
	var out notoapi.AgentListResult
	path := "/v1/agent/meetings"
	if opts.Limit > 0 {
		path += "?limit=" + strconv.Itoa(opts.Limit)
	}
	if !opts.After.IsZero() {
		sep := "?"
		if opts.Limit > 0 {
			sep = "&"
		}
		path += sep + "after=" + opts.After.UTC().Format(time.RFC3339Nano)
	}
	if !opts.Before.IsZero() {
		sep := "?"
		if opts.Limit > 0 || !opts.After.IsZero() {
			sep = "&"
		}
		path += sep + "before=" + opts.Before.UTC().Format(time.RFC3339Nano)
	}
	err := c.do(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *httpClient) AgentGetMeeting(ctx context.Context, id string) (notoapi.AgentMeetingContext, error) {
	var out notoapi.AgentMeetingContext
	err := c.do(ctx, http.MethodGet, "/v1/agent/meetings/"+id, nil, &out)
	return out, err
}
