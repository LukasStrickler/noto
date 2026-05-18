package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// registerRoutes wires every endpoint. Each handler delegates to a
// Service method via the wrapper; we keep handlers small and parsing
// concerns local.
func (s *Server) registerRoutes(mux *http.ServeMux) {
	// Health + lifecycle
	mux.HandleFunc("/v1/healthz", s.handleHealthz)
	mux.HandleFunc("/v1/shutdown", s.handleShutdown)

	// Meetings
	mux.HandleFunc("/v1/meetings", s.handleMeetings)
	mux.HandleFunc("/v1/meetings/", s.handleMeetingByID)

	// Search
	mux.HandleFunc("/v1/search", s.handleSearch)

	// Recording
	mux.HandleFunc("/v1/recording", s.handleRecording)
	mux.HandleFunc("/v1/recording/", s.handleRecordingVerb)

	// Imports
	mux.HandleFunc("/v1/imports/audio", s.handleImportAudio)

	// Jobs
	mux.HandleFunc("/v1/jobs", s.handleJobs)
	mux.HandleFunc("/v1/jobs/", s.handleJobByIDOrEvents)

	// Providers
	mux.HandleFunc("/v1/providers", s.handleProviders)
	mux.HandleFunc("/v1/providers/", s.handleProviderByID)

	// Config
	mux.HandleFunc("/v1/config", s.handleConfig)
	mux.HandleFunc("/v1/config/paths", s.handleConfigPaths)

	// Storage
	mux.HandleFunc("/v1/storage", s.handleStorage)
	mux.HandleFunc("/v1/storage/verify", s.handleStorageVerify)
	mux.HandleFunc("/v1/storage/reindex", s.handleStorageReindex)
}

// --- Health ---

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	h, err := s.svc.Health(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	// Only callable on UDS. TCP would let any token holder kill the daemon.
	if s.opts.Network != "unix" {
		writeError(w, notoapi.NewError(notoapi.CodePermissionDenied, "shutdown only available on UDS", nil))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting_down"})
	go func() { _ = s.Close() }()
}

// --- Meetings ---

func (s *Server) handleMeetings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	opts := notoapi.ListMeetingsOpts{
		Query: r.URL.Query().Get("q"),
		Since: r.URL.Query().Get("since"),
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		opts.Limit = n
	}
	res, err := s.svc.ListMeetings(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// /v1/meetings/{id} or /v1/meetings/{id}/{sub}
func (s *Server) handleMeetingByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/meetings/")
	parts := strings.SplitN(path, "/", 2)
	if parts[0] == "" {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "missing meeting id", nil))
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			m, err := s.svc.GetMeeting(r.Context(), id)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, m)
		case http.MethodDelete:
			if err := s.svc.DeleteMeeting(r.Context(), id); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
		return
	}
	switch parts[1] {
	case "transcript":
		t, err := s.svc.GetTranscript(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	case "summary":
		sum, err := s.svc.GetSummary(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, sum)
	case "files":
		f, err := s.svc.GetMeetingFiles(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, f)
	case "agent":
		a, err := s.svc.GetAgentHandoff(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, a)
	case "verify":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		job, err := s.svc.VerifyMeeting(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown sub-resource", map[string]any{"resource": parts[1]}))
	}
}

// --- Search ---

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	opts := notoapi.SearchOpts{
		Query:   r.URL.Query().Get("q"),
		Speaker: r.URL.Query().Get("speaker"),
		Scope:   r.URL.Query().Get("scope"),
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		opts.Limit = n
	}
	res, err := s.svc.Search(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// --- Imports ---

func (s *Server) handleImportAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	var body notoapi.ImportAudioOpts
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
		return
	}
	m, job, err := s.svc.ImportAudio(r.Context(), body.Path, body.Title)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, notoapi.ImportAudioResult{Meeting: m, Job: job})
}

// --- Recording ---

func (s *Server) handleRecording(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	state, err := s.svc.GetRecording(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleRecordingVerb(w http.ResponseWriter, r *http.Request) {
	verb := strings.TrimPrefix(r.URL.Path, "/v1/recording/")
	switch verb {
	case "start":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		var opts notoapi.StartRecordingOpts
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil && err.Error() != "EOF" {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON body", nil))
			return
		}
		res, err := s.svc.StartRecording(r.Context(), opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)

	case "stop":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		var opts notoapi.StopRecordingOpts
		_ = json.NewDecoder(r.Body).Decode(&opts)
		res, err := s.svc.StopRecording(r.Context(), opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)

	case "pause":
		if err := s.svc.PauseRecording(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case "resume":
		if err := s.svc.ResumeRecording(r.Context()); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case "marker":
		var body struct {
			Label string `json:"label"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if err := s.svc.AddMarker(r.Context(), body.Label); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)

	case "preflight":
		res, err := s.svc.PreflightRecording(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)

	case "meters":
		s.handleSSE(w, r, true)

	default:
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown verb", map[string]any{"verb": verb}))
	}
}

// --- Jobs ---

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		opts := notoapi.ListJobsOpts{
			Status:    notoapi.JobStatus(r.URL.Query().Get("status")),
			MeetingID: r.URL.Query().Get("meeting_id"),
		}
		if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
			opts.Limit = n
		}
		jobs, err := s.svc.ListJobs(r.Context(), opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, jobs)
	case http.MethodPost:
		var opts notoapi.CreateJobOpts
		if err := json.NewDecoder(r.Body).Decode(&opts); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON body", nil))
			return
		}
		job, err := s.svc.CreateJob(r.Context(), opts)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
	}
}

func (s *Server) handleJobByIDOrEvents(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/jobs/")
	if rest == "events" {
		s.handleSSE(w, r, false)
		return
	}
	id := rest
	switch r.Method {
	case http.MethodGet:
		job, err := s.svc.GetJob(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, job)
	case http.MethodDelete:
		if err := s.svc.CancelJob(r.Context(), id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
	}
}

// --- Providers ---

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	list, err := s.svc.ListProviders(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/providers/")
	parts := strings.SplitN(path, "/", 2)
	if parts[0] == "" {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "missing provider id", nil))
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "no method on /v1/providers/{id}", nil))
		return
	}
	switch parts[1] {
	case "key":
		switch r.Method {
		case http.MethodPost:
			var body struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
				return
			}
			if err := s.svc.SetProviderKey(r.Context(), id, body.Value); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case http.MethodDelete:
			if err := s.svc.DeleteProviderKey(r.Context(), id); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
	case "test":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		res, err := s.svc.TestProvider(r.Context(), id)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	case "active-speech":
		if r.Method != http.MethodPut {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		if err := s.svc.SetActiveSpeech(r.Context(), id); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown sub-resource", map[string]any{"resource": parts[1]}))
	}
}

// --- Config ---

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		cfg, err := s.svc.GetConfig(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	case http.MethodPatch:
		var patch notoapi.ConfigPatch
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
			return
		}
		cfg, err := s.svc.PatchConfig(r.Context(), patch)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cfg)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
	}
}

func (s *Server) handleConfigPaths(w http.ResponseWriter, r *http.Request) {
	p, err := s.svc.GetPaths(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, p)
}

// --- Storage ---

func (s *Server) handleStorage(w http.ResponseWriter, r *http.Request) {
	st, err := s.svc.GetStorage(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleStorageVerify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	j, err := s.svc.VerifyStorage(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}

func (s *Server) handleStorageReindex(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	j, err := s.svc.ReindexStorage(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, j)
}
