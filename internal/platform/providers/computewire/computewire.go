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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
