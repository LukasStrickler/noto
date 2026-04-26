package stt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/artifacts"
)

type mockHTTP struct {
	respStatus int
	respBody   []byte
	respErr    error
}

func (m *mockHTTP) Do(req *http.Request) (*http.Response, error) {
	if m.respErr != nil {
		return nil, m.respErr
	}
	return &http.Response{
		StatusCode: m.respStatus,
		Body:        &mockReadCloser{data: m.respBody},
	}, nil
}

type mockReadCloser struct {
	data []byte
	pos  int
}

func (m *mockReadCloser) Read(p []byte) (n int, err error) {
	if m.pos >= len(m.data) {
		return 0, errors.New("EOF")
	}
	n = copy(p, m.data[m.pos:])
	m.pos += n
	return n, nil
}

func (m *mockReadCloser) Close() error {
	return nil
}

func TestAssemblyAIParseResponse(t *testing.T) {
	adapter := &AssemblyAIAdapter{}

	rawResp := map[string]any{
		"status":         "completed",
		"id":             "test-job-id",
		"language_code":  "en",
		"text":           "Hello world",
		"audio_duration": 5.5,
		"utterances": []map[string]any{
			{
				"text":       "Hello world",
				"start":      0.0,
				"end":        5.5,
				"speaker":    "0",
				"confidence": 0.95,
			},
		},
		"words": []map[string]any{
			{"text": "Hello", "start": 0.0, "end": 0.5, "confidence": 0.95, "speaker": "0"},
			{"text": "world", "start": 0.6, "end": 1.0, "confidence": 0.95, "speaker": "0"},
		},
	}
	raw, _ := json.Marshal(rawResp)

	transcript, err := adapter.parseResponse(raw, "meeting-123")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}

	if transcript.MeetingID != "meeting-123" {
		t.Errorf("MeetingID = %s, want meeting-123", transcript.MeetingID)
	}
	if transcript.Language != "en" {
		t.Errorf("Language = %s, want en", transcript.Language)
	}
	if len(transcript.Speakers) != 1 {
		t.Errorf("len(Speakers) = %d, want 1", len(transcript.Speakers))
	}
	if len(transcript.Segments) != 1 {
		t.Errorf("len(Segments) = %d, want 1", len(transcript.Segments))
	}
	if transcript.Segments[0].Text != "Hello world" {
		t.Errorf("Segments[0].Text = %s, want Hello world", transcript.Segments[0].Text)
	}
	if len(transcript.Words) != 2 {
		t.Errorf("len(Words) = %d, want 2", len(transcript.Words))
	}
}

func TestAssemblyAIParseResponseEmpty(t *testing.T) {
	adapter := &AssemblyAIAdapter{}

	raw, _ := json.Marshal(map[string]any{
		"status": "completed",
		"id":     "test-job-id",
		"text":   "",
	})

	transcript, err := adapter.parseResponse(raw, "meeting-123")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}
	if transcript == nil {
		t.Fatalf("parseResponse returned nil transcript")
	}
}

func TestWhisperParseResponse(t *testing.T) {
	adapter := &WhisperAdapter{}

	rawResp := map[string]any{
		"text":       "Test transcription",
		"language":   "en",
		"duration":   10.5,
		"words": []map[string]any{
			{"text": "Test", "start": 0.0, "end": 0.5, "confidence": 0.9},
			{"text": "transcription", "start": 0.6, "end": 1.0, "confidence": 0.9},
		},
		"segments": []map[string]any{
			{
				"id":          0,
				"text":        "Test transcription",
				"start":       0.0,
				"end":         1.0,
				"confidence":  0.9,
				"speaker_id":  "1",
			},
		},
	}
	raw, _ := json.Marshal(rawResp)

	transcript, err := adapter.parseResponse(raw, "meeting-456")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}

	if transcript.MeetingID != "meeting-456" {
		t.Errorf("MeetingID = %s, want meeting-456", transcript.MeetingID)
	}
	if transcript.Language != "en" {
		t.Errorf("Language = %s, want en", transcript.Language)
	}
	if len(transcript.Segments) != 1 {
		t.Errorf("len(Segments) = %d, want 1", len(transcript.Segments))
	}
}

func TestWhisperParseResponseNoSegments(t *testing.T) {
	adapter := &WhisperAdapter{}

	rawResp := map[string]any{
		"text":     "Just text no segments",
		"language": "en",
		"duration": 5.0,
	}
	raw, _ := json.Marshal(rawResp)

	transcript, err := adapter.parseResponse(raw, "meeting-789")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}

	if len(transcript.Segments) != 1 {
		t.Errorf("len(Segments) = %d, want 1 (fallback to single segment)", len(transcript.Segments))
	}
	if transcript.Segments[0].Text != "Just text no segments" {
		t.Errorf("Segments[0].Text = %s, want Just text no segments", transcript.Segments[0].Text)
	}
}

func TestSpeechmaticsParseResponse(t *testing.T) {
	adapter := &SpeechmaticsAdapter{}

	rawResp := map[string]any{
		"job_id":    "sm-job-123",
		"text":      "Speechmatics test",
		"language":  "en",
		"duration":  8.0,
		"utterances": []map[string]any{
			{
				"text":       "Speechmatics test",
				"start":      0.0,
				"end":        8.0,
				"speaker":    "A",
				"confidence": 0.88,
			},
		},
		"words": []map[string]any{
			{"text": "Speechmatics", "start": 0.0, "end": 1.0, "confidence": 0.88},
			{"text": "test", "start": 1.1, "end": 2.0, "confidence": 0.88},
		},
	}
	raw, _ := json.Marshal(rawResp)

	transcript, err := adapter.parseResponse(raw, "meeting-spm")
	if err != nil {
		t.Fatalf("parseResponse returned error: %v", err)
	}

	if transcript.MeetingID != "meeting-spm" {
		t.Errorf("MeetingID = %s, want meeting-spm", transcript.MeetingID)
	}
	if len(transcript.Speakers) != 1 {
		t.Errorf("len(Speakers) = %d, want 1", len(transcript.Speakers))
	}
	if len(transcript.Segments) != 1 {
		t.Errorf("len(Segments) = %d, want 1", len(transcript.Segments))
	}
}

func TestTranscribeOptions(t *testing.T) {
	opts := TranscribeOptions{
		Language:    "en-US",
		ContextBias: []string{"Noto", "meeting"},
		NumSpeakers: 2,
		MeetingID:   "meeting-test",
	}

	if opts.Language != "en-US" {
		t.Errorf("Language = %s, want en-US", opts.Language)
	}
	if len(opts.ContextBias) != 2 {
		t.Errorf("len(ContextBias) = %d, want 2", len(opts.ContextBias))
	}
	if opts.NumSpeakers != 2 {
		t.Errorf("NumSpeakers = %d, want 2", opts.NumSpeakers)
	}
	if opts.MeetingID != "meeting-test" {
		t.Errorf("MeetingID = %s, want meeting-test", opts.MeetingID)
	}
}

type failingHTTP struct {
	status int
	body   string
}

func (f *failingHTTP) Do(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.status,
		Body:       &mockReadCloser{data: []byte(f.body)},
	}, nil
}

func TestAssemblyAIUploadError(t *testing.T) {
	adapter := &AssemblyAIAdapter{
		HTTP: &failingHTTP{status: 400, body: "bad request"},
	}

	_, err := adapter.upload(context.Background(), adapter.HTTP, "https://api.assemblyai.com", []byte("test audio"))
	if err == nil {
		t.Error("upload should have returned error for 400 status")
	}
}

func TestSegmentID(t *testing.T) {
	tests := []struct {
		i      int
		expect string
	}{
		{0, "seg_000001"},
		{4, "seg_000005"},
		{99, "seg_000100"},
	}

	for _, tt := range tests {
		result := segmentID(tt.i)
		if result != tt.expect {
			t.Errorf("segmentID(%d) = %s, want %s", tt.i, result, tt.expect)
		}
	}
}

func TestMultipartWriter(t *testing.T) {
	fields := map[string]string{
		"model": "base",
	}
	audio := []byte("fake audio data")

	body, contentType, err := multipartWriter(fields, audio, "test.mp3")
	if err != nil {
		t.Fatalf("multipartWriter returned error: %v", err)
	}

	if !strings.Contains(contentType, "multipart/form-data") {
		t.Errorf("contentType = %s, want multipart/form-data", contentType)
	}

	bs, ok := body.(*strings.Reader)
	if !ok {
		t.Fatalf("body type = %T, want *strings.Reader", body)
	}
	if bs.Len() == 0 {
		t.Error("body.Len() = 0, want non-zero")
	}
}