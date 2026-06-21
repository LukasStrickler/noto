package diarize

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
)

// RemoteDiarizer offloads diarization to a noto compute endpoint over HTTP,
// mirroring stt.RemoteSTT. It satisfies Diarizer, so the pipeline is unchanged.
// Audio streams as the request body; options ride in X-Noto-* headers. The
// endpoint is a `noto serve` exposing /v1/compute/diarize.
type RemoteDiarizer struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	Tag     string
}

// NewRemoteDiarizer builds a remote diarizer for a compute endpoint.
func NewRemoteDiarizer(baseURL, token string) *RemoteDiarizer {
	return &RemoteDiarizer{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), Token: token}
}

func (r *RemoteDiarizer) ProviderID() string {
	if strings.TrimSpace(r.Tag) != "" {
		return r.Tag
	}
	return "remote-diar"
}

func (r *RemoteDiarizer) Diarize(ctx context.Context, audio []byte, opts DiarizeOptions) ([]Turn, error) {
	if r == nil || r.BaseURL == "" {
		return nil, notoerr.New("remote_diarize_unconfigured", "Remote diarizer endpoint is not configured.", nil)
	}
	newReq := func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.BaseURL+computewire.DiarizePath, bytes.NewReader(audio))
		if err != nil {
			return nil, notoerr.Wrap("remote_diarize_request_failed", "Could not create remote diarize request.", err)
		}
		req.ContentLength = int64(len(audio))
		req.Header.Set("Content-Type", "application/octet-stream")
		if opts.MeetingID != "" {
			req.Header.Set(computewire.HeaderMeetingID, opts.MeetingID)
		}
		if opts.NumSpeakers > 0 {
			req.Header.Set(computewire.HeaderNumSpeakers, strconv.Itoa(opts.NumSpeakers))
		}
		if r.Token != "" {
			req.Header.Set("Authorization", "Bearer "+r.Token)
		}
		return req, nil
	}
	client := r.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	// Retry transient cold-start/autoscale failures: diarization is idempotent, so
	// a blip recovers without failing the job (see computewire.DoWithRetry).
	resp, err := computewire.DoWithRetry(ctx, client, newReq)
	if err != nil {
		return nil, notoerr.Wrap("remote_diarize_remote_error", "Remote diarize request failed.", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, computewire.RemoteError("remote_diarize_failed", "Remote diarizer endpoint returned an error.", resp)
	}
	var out TurnsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, notoerr.Wrap("remote_diarize_response_invalid", "Could not decode remote diarize response.", err)
	}
	return out.Turns, nil
}
