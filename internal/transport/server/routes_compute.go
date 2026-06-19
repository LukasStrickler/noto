package server

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
	"github.com/lukasstrickler/noto/internal/platform/providers/diarize"
	"github.com/lukasstrickler/noto/internal/platform/providers/stt"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// Compute endpoints turn a `noto serve` into a stateless compute backend: a
// RemoteSTT / RemoteDiarizer on another node streams audio here, the heavy model
// runs in-process, and the artifact streams back. The node keeps nothing — it is
// a pure function, never a source of truth (see DRAFT_LOCAL_FIRST_PIVOT.md).
// Audio arrives as the raw body; options arrive in X-Noto-* headers.

func (s *Server) handleComputeTranscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "read audio: "+err.Error(), nil))
		return
	}
	opts := stt.TranscribeOptions{
		Language:  r.Header.Get(computewire.HeaderLanguage),
		MeetingID: r.Header.Get(computewire.HeaderMeetingID),
		Model:     r.Header.Get(computewire.HeaderModel),
	}
	if n, convErr := strconv.Atoi(r.Header.Get(computewire.HeaderNumSpeakers)); convErr == nil {
		opts.NumSpeakers = n
	}
	if cb := r.Header.Get(computewire.HeaderContextBias); cb != "" {
		_ = json.Unmarshal([]byte(cb), &opts.ContextBias)
	}
	t, err := s.svc.ComputeTranscribe(r.Context(), audio, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleComputeDiarize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	audio, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUploadBytes))
	if err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "read audio: "+err.Error(), nil))
		return
	}
	opts := diarize.DiarizeOptions{MeetingID: r.Header.Get(computewire.HeaderMeetingID)}
	if n, convErr := strconv.Atoi(r.Header.Get(computewire.HeaderNumSpeakers)); convErr == nil {
		opts.NumSpeakers = n
	}
	turns, err := s.svc.ComputeDiarize(r.Context(), audio, opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, diarize.TurnsResponse{Turns: turns})
}

func (s *Server) handleModalComputeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	status, err := s.svc.GetModalComputeStatus(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleModalComputeSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	var req notoapi.ModalSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
		return
	}
	status, err := s.svc.SetupModalCompute(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleModalBenchmark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	var req notoapi.ModalBenchmarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
		return
	}
	res, err := s.svc.RunModalBenchmark(r.Context(), req)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, res)
}
