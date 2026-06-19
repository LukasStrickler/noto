package stt

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/core/notoerr"
	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
)

// RemoteSTT offloads transcription to a noto compute endpoint over HTTP. It
// satisfies STTProvider, so the pipeline treats it exactly like the in-process
// provider — the only difference is WHERE the decode runs. The audio streams as
// the request body; options ride in X-Noto-* headers (see computewire). The
// endpoint is a `noto serve` exposing /v1/compute/transcribe — a Linux GPU box
// or a serverless function with the same contract.
type RemoteSTT struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// Tag identifies the route in transcript provenance / logs. Defaults to
	// "remote-stt".
	Tag string
}

// NewRemoteSTT builds a remote STT provider for a compute endpoint. token may be
// empty for an unauthenticated (e.g. UDS-fronted or trusted-network) endpoint.
func NewRemoteSTT(baseURL, token string) *RemoteSTT {
	return &RemoteSTT{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: token}
}

func (r *RemoteSTT) ProviderID() string {
	if strings.TrimSpace(r.Tag) != "" {
		return r.Tag
	}
	return "remote-stt"
}

func (r *RemoteSTT) FeatureMap() ProviderFeatures {
	return ProviderFeatures{
		ProviderID: r.ProviderID(),
		Features:   []Feature{FeatureTranscribe, FeatureWordTimestamps},
		SpeedTier:  "accurate",
		IsLocal:    false,
	}
}

func (r *RemoteSTT) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	if r == nil || r.BaseURL == "" {
		return nil, notoerr.New("remote_stt_unconfigured", "Remote STT endpoint is not configured.", nil)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+computewire.TranscribePath, bytes.NewReader(audio))
	if err != nil {
		return nil, notoerr.Wrap("remote_stt_request_failed", "Could not create remote STT request.", err)
	}
	req.ContentLength = int64(len(audio))
	req.Header.Set("Content-Type", "application/octet-stream")
	if opts.Language != "" {
		req.Header.Set(computewire.HeaderLanguage, opts.Language)
	}
	if opts.MeetingID != "" {
		req.Header.Set(computewire.HeaderMeetingID, opts.MeetingID)
	}
	if opts.NumSpeakers > 0 {
		req.Header.Set(computewire.HeaderNumSpeakers, strconv.Itoa(opts.NumSpeakers))
	}
	if opts.Model != "" {
		req.Header.Set(computewire.HeaderModel, opts.Model)
	}
	if len(opts.ContextBias) > 0 {
		if b, mErr := json.Marshal(opts.ContextBias); mErr == nil {
			req.Header.Set(computewire.HeaderContextBias, string(b))
		}
	}
	if r.Token != "" {
		req.Header.Set("Authorization", "Bearer "+r.Token)
	}
	client := r.HTTP
	if client == nil {
		// A long meeting transcribes in seconds at batch speeds, but a cold
		// serverless GPU can take a minute to warm — allow generous headroom.
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, notoerr.Wrap("remote_stt_remote_error", "Remote STT request failed.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, computewire.RemoteError("remote_stt_failed", "Remote STT endpoint returned an error.", resp)
	}
	var t artifacts.Transcript
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return nil, notoerr.Wrap("remote_stt_response_invalid", "Could not decode remote STT response.", err)
	}
	return &t, nil
}
