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

// maxBiasHeaderBytes bounds the JSON-encoded context-bias header so it stays well
// under common serverless/proxy per-header limits (~8KB), leaving room for the
// other X-Noto-* headers + auth.
const maxBiasHeaderBytes = 6144

// marshalBoundedBias ASCII-JSON-encodes terms, keeping the LARGEST leading prefix
// whose encoding fits within limit bytes (the builder orders the most relevant
// first). Returns "" only if even a single term can't fit — then no bias header is
// sent, since a rejected request is worse than an unbiased decode.
func marshalBoundedBias(terms []string, limit int) string {
	if s := asciiJSONMarshal(terms); s != "" && len(s) <= limit {
		return s
	}
	lo, hi, best := 1, len(terms)-1, ""
	for lo <= hi {
		mid := (lo + hi) / 2
		if s := asciiJSONMarshal(terms[:mid]); s != "" && len(s) <= limit {
			best = s
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	return best
}

// asciiJSONMarshal JSON-encodes v and \u-escapes every non-ASCII rune, so the result
// is PURE ASCII — still valid JSON that any Unmarshal/json.loads decodes back to the
// same strings, but safe in an HTTP header. A raw UTF-8 byte in a header value is
// RFC-7230-noncompliant, and a proxy fronting a hosted compute endpoint may strip the
// whole header — silently dropping international participant names ("Réunion", "会議")
// from the bias glossary so they never get biased/repaired. No server change is
// needed: \uXXXX is the standard JSON string escape. A no-op for an all-ASCII
// glossary. Returns "" on a marshal error.
func asciiJSONMarshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	const hexDigits = "0123456789abcdef"
	var sb strings.Builder
	appendU := func(code uint16) {
		sb.WriteString(`\u`)
		sb.WriteByte(hexDigits[code>>12&0xf])
		sb.WriteByte(hexDigits[code>>8&0xf])
		sb.WriteByte(hexDigits[code>>4&0xf])
		sb.WriteByte(hexDigits[code&0xf])
	}
	for _, r := range string(b) {
		switch {
		case r < 0x80:
			sb.WriteRune(r)
		case r <= 0xffff:
			appendU(uint16(r))
		default: // astral plane → UTF-16 surrogate pair
			r -= 0x10000
			appendU(uint16(0xd800 + (r >> 10)))
			appendU(uint16(0xdc00 + (r & 0x3ff)))
		}
	}
	return sb.String()
}

func (r *RemoteSTT) Transcribe(ctx context.Context, audio []byte, opts TranscribeOptions) (*artifacts.Transcript, error) {
	if r == nil || r.BaseURL == "" {
		return nil, notoerr.New("remote_stt_unconfigured", "Remote STT endpoint is not configured.", nil)
	}
	newReq := func() (*http.Request, error) {
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
			// The bias glossary spans EVERY known profile (participants are unknown at
			// transcribe time), so on a large deployment it can grow past serverless
			// gateway header limits and get the whole request rejected. Bound the
			// HEADER to a safe size — the full glossary still drives CPU entity-repair
			// downstream; this only trims what the remote decoder is biased toward.
			if b := marshalBoundedBias(opts.ContextBias, maxBiasHeaderBytes); b != "" {
				req.Header.Set(computewire.HeaderContextBias, b)
			}
		}
		if r.Token != "" {
			req.Header.Set("Authorization", "Bearer "+r.Token)
		}
		return req, nil
	}
	client := r.HTTP
	if client == nil {
		// A long meeting transcribes in seconds at batch speeds, but a cold
		// serverless GPU can take a minute to warm — allow generous headroom.
		client = &http.Client{Timeout: 30 * time.Minute}
	}
	// Retry transient cold-start/autoscale failures: transcription is idempotent, so
	// a blip recovers without failing the job (see computewire.DoWithRetry).
	resp, err := computewire.DoWithRetry(ctx, client, newReq)
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
