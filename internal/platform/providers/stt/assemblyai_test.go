package stt

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lukasstrickler/noto/internal/core/notoerr"
)

// routeDoer fakes the AssemblyAI HTTP API: it answers upload/submit/poll by
// URL, captures the submit payload, and walks pollSeq one response per poll
// (repeating the last entry once exhausted).
type routeDoer struct {
	submitBody []byte
	pollSeq    []string
	pollIdx    int
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func (d *routeDoer) Do(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	switch {
	case strings.HasSuffix(path, "/v2/upload"):
		return jsonResp(200, `{"upload_url":"https://cdn.example/abc"}`), nil
	case strings.HasSuffix(path, "/v2/transcript") && req.Method == http.MethodPost:
		if req.Body != nil {
			d.submitBody, _ = io.ReadAll(req.Body)
		}
		return jsonResp(200, `{"id":"job_1"}`), nil
	case strings.Contains(path, "/v2/transcript/"):
		body := d.pollSeq[d.pollIdx]
		if d.pollIdx < len(d.pollSeq)-1 {
			d.pollIdx++
		}
		return jsonResp(200, body), nil
	}
	return jsonResp(404, `{}`), nil
}

func sttErrCode(err error) string {
	var ne *notoerr.Error
	if errors.As(err, &ne) {
		return ne.Code
	}
	return ""
}

func TestTranscribe_HappyPathAndKeytermsArray(t *testing.T) {
	completed := `{"status":"completed","id":"job_1","language_code":"en","audio_duration":12.0,
		"utterances":[{"text":"Hello there.","start":0,"end":5,"speaker":"A","confidence":0.95}]}`
	// First poll is still processing, then completed — exercises the poll
	// loop (and the advisory body-close handling) across iterations.
	d := &routeDoer{pollSeq: []string{`{"status":"processing"}`, completed}}
	a := &AssemblyAIAdapter{APIKey: "key", HTTP: d, PollInterval: time.Millisecond, MaxPolls: 10}

	tr, err := a.Transcribe(context.Background(), []byte("audiobytes"), TranscribeOptions{
		MeetingID:   "mtg_1",
		ContextBias: []string{"Kubernetes", "Postgres"},
	})
	if err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	if tr.MeetingID != "mtg_1" || len(tr.Segments) != 1 {
		t.Fatalf("unexpected transcript: %+v", tr)
	}
	if tr.Segments[0].Text != "Hello there." {
		t.Errorf("segment text = %q", tr.Segments[0].Text)
	}

	// keyterms_prompt must be sent as an array of distinct terms, not as a
	// single comma-joined string.
	var payload map[string]any
	if err := json.Unmarshal(d.submitBody, &payload); err != nil {
		t.Fatalf("submit body is not valid JSON: %v", err)
	}
	kt, ok := payload["keyterms_prompt"].([]any)
	if !ok {
		t.Fatalf("keyterms_prompt is %T, want JSON array", payload["keyterms_prompt"])
	}
	if len(kt) != 2 || kt[0] != "Kubernetes" || kt[1] != "Postgres" {
		t.Errorf("keyterms_prompt = %v, want [Kubernetes Postgres]", kt)
	}
}

func TestTranscribe_NoContextBiasOmitsKeyterms(t *testing.T) {
	completed := `{"status":"completed","id":"j","utterances":[{"text":"Hi.","start":0,"end":1,"speaker":"A","confidence":0.9}]}`
	d := &routeDoer{pollSeq: []string{completed}}
	a := &AssemblyAIAdapter{APIKey: "key", HTTP: d, PollInterval: time.Millisecond, MaxPolls: 5}

	if _, err := a.Transcribe(context.Background(), []byte("x"), TranscribeOptions{MeetingID: "m"}); err != nil {
		t.Fatalf("Transcribe returned error: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(d.submitBody, &payload); err != nil {
		t.Fatalf("submit body is not valid JSON: %v", err)
	}
	if _, ok := payload["keyterms_prompt"]; ok {
		t.Error("keyterms_prompt should be absent when no context bias is supplied")
	}
}

func TestTranscribe_PollErrorStatus(t *testing.T) {
	d := &routeDoer{pollSeq: []string{`{"status":"error","error":"bad audio"}`}}
	a := &AssemblyAIAdapter{APIKey: "key", HTTP: d, PollInterval: time.Millisecond, MaxPolls: 5}

	_, err := a.Transcribe(context.Background(), []byte("x"), TranscribeOptions{MeetingID: "mtg_1"})
	if code := sttErrCode(err); code != "provider_failed" {
		t.Errorf("error code = %q, want provider_failed", code)
	}
}

func TestTranscribe_MissingAPIKey(t *testing.T) {
	a := &AssemblyAIAdapter{HTTP: &routeDoer{}, PollInterval: time.Millisecond}
	_, err := a.Transcribe(context.Background(), []byte("x"), TranscribeOptions{MeetingID: "mtg_1"})
	if code := sttErrCode(err); code != "missing_credential" {
		t.Errorf("error code = %q, want missing_credential", code)
	}
}
