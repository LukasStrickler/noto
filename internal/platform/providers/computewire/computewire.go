// Package computewire defines the wire contract shared by noto's remote compute
// transport: the HTTP paths and headers a RemoteSTT / RemoteDiarizer client
// sends and the /v1/compute/* server handlers parse. Keeping the contract in one
// place means the client and server can never drift on a header name or path —
// the same single-definition discipline the rest of noto follows.
//
// The design rule: audio is ALWAYS the raw request body (Content-Type
// application/octet-stream), streamed, never base64-wrapped in a JSON envelope.
// A one-hour meeting is tens of MB; base64 would inflate it ~33% and force the
// whole thing into memory on both ends. The transcribe/diarize options are small
// and ride in X-Noto-* headers instead, keeping the body a clean byte stream.
package computewire

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

// Endpoint paths, relative to a compute backend's base URL. A `noto serve`
// exposes both; a serverless function implements the same contract.
const (
	TranscribePath = "/v1/compute/transcribe"
	DiarizePath    = "/v1/compute/diarize"
)

// Request headers carrying the options that would otherwise sit in a JSON body.
const (
	HeaderLanguage    = "X-Noto-Language"
	HeaderMeetingID   = "X-Noto-Meeting-Id"
	HeaderNumSpeakers = "X-Noto-Num-Speakers"
	HeaderModel       = "X-Noto-Model"
	HeaderContextBias = "X-Noto-Context-Bias" // JSON-encoded []string
)

// maxComputeAttempts bounds how many times a compute request is sent (1 initial +
// retries). retryBaseDelay is the first backoff; each further attempt doubles it. A
// var, not a const, so tests can shrink it — production keeps a cold-start-friendly
// base.
const maxComputeAttempts = 3

var retryBaseDelay = 250 * time.Millisecond

// retryableStatus reports whether a compute response status is worth retrying: a
// transient infra condition (serverless cold-start 502, autoscale 503, gateway
// 504), NOT a client error (4xx won't change on retry) or a deterministic 500.
func retryableStatus(code int) bool {
	switch code {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// DoWithRetry sends a compute request with bounded retry on TRANSIENT failures — a
// network error, or a 502/503/504. transcribe and diarize are idempotent (the
// compute node keeps no state), so a cold-start or autoscale blip recovers
// transparently instead of failing the whole job and discarding the GPU work the
// earlier pipeline stages already produced — exactly the kind of wasted utilization
// a hosted deployment must avoid. newReq rebuilds the request each attempt so the
// audio body can be re-read; a build error, a client error (4xx), and success all
// return immediately. Backoff is exponential with jitter and honors ctx between
// attempts. The returned response's body is the caller's to close; bodies of
// retried responses are drained and closed here.
func DoWithRetry(ctx context.Context, client *http.Client, newReq func() (*http.Request, error)) (*http.Response, error) {
	var lastErr error
	for attempt := 0; attempt < maxComputeAttempts; attempt++ {
		if attempt > 0 {
			delay := retryBaseDelay << (attempt - 1)                // 1x, 2x, 4x, …
			delay += time.Duration(rand.Int64N(int64(delay/2) + 1)) // up to +50% jitter
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
		req, err := newReq()
		if err != nil {
			return nil, err // a malformed request can't be fixed by retrying
		}
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
			if ctx.Err() != nil {
				return nil, err // caller cancelled / deadline hit — don't retry
			}
			continue
		}
		if retryableStatus(resp.StatusCode) && attempt < maxComputeAttempts-1 {
			// Drain+close so the connection can be reused, then back off and retry.
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8<<10))
			_ = resp.Body.Close()
			lastErr = fmt.Errorf("compute endpoint returned status %d", resp.StatusCode)
			continue
		}
		return resp, nil
	}
	return nil, lastErr
}

// RemoteError converts a non-2xx compute response into a notoerr, preferring the
// remote's structured error envelope ({"error":{"code","message"}}) when present
// so the original cause surfaces instead of a bare status code.
func RemoteError(code, fallback string, resp *http.Response) error {
	var env struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<10))
	_ = json.Unmarshal(body, &env)
	if env.Error != nil && env.Error.Message != "" {
		return notoerr.New(code, env.Error.Message, map[string]any{
			"status_code": resp.StatusCode,
			"remote_code": env.Error.Code,
		})
	}
	return notoerr.New(code, fmt.Sprintf("%s (status %d)", fallback, resp.StatusCode), map[string]any{
		"status_code": resp.StatusCode,
	})
}
