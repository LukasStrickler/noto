package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/lukasstrickler/noto/internal/platform/providers/computewire"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
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

	// Speaker profiles
	mux.HandleFunc("/v1/speaker-profiles", s.handleSpeakerProfiles)
	mux.HandleFunc("/v1/speaker-profiles/", s.handleSpeakerProfileByID)

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

	// System (backend capabilities / "connected to" discovery)
	mux.HandleFunc("/v1/system", s.handleSystem)

	// Compute — stateless STT/diarize offload (this `noto serve` as a compute backend)
	mux.HandleFunc(computewire.TranscribePath, s.handleComputeTranscribe)
	mux.HandleFunc(computewire.DiarizePath, s.handleComputeDiarize)
	mux.HandleFunc("/v1/compute/modal/status", s.handleModalComputeStatus)
	mux.HandleFunc("/v1/compute/modal/setup", s.handleModalComputeSetup)
	mux.HandleFunc("/v1/compute/modal/benchmark", s.handleModalBenchmark)

	// Benchmark measurement spine (Program A)
	mux.HandleFunc("/v1/bench/estimate", s.handleBenchEstimate)
	mux.HandleFunc("/v1/bench/preflight", s.handleBenchPreflight)
	mux.HandleFunc("/v1/bench/run", s.handleBenchRun)
	mux.HandleFunc("/v1/bench/compare", s.handleBenchCompare)
	mux.HandleFunc("/v1/bench/ledger/winners", s.handleBenchLedgerWinners)
	mux.HandleFunc("/v1/bench/ledger/append", s.handleBenchLedgerAppend)
	mux.HandleFunc("/v1/bench/dataset/list", s.handleBenchDatasetList)
	mux.HandleFunc("/v1/bench/audit", s.handleBenchAudit)
	mux.HandleFunc("/v1/bench/retrace", s.handleBenchRetrace)
	mux.HandleFunc("/v1/bench/scale", s.handleBenchScale)
	mux.HandleFunc("/v1/bench/insights", s.handleBenchInsights)

	// Repo — artifact store over HTTP (this `noto serve` as a remote data plane)
	mux.HandleFunc("/v1/repo/meetings", s.handleRepoMeetings)
	mux.HandleFunc("/v1/repo/meetings/", s.handleRepoMeetingByID)

	// Storage
	mux.HandleFunc("/v1/storage", s.handleStorage)
	mux.HandleFunc("/v1/storage/verify", s.handleStorageVerify)
	mux.HandleFunc("/v1/storage/reindex", s.handleStorageReindex)

	// Agent API — optimised for AI agent consumption
	mux.HandleFunc("/v1/agent/meetings", s.handleAgentMeetings)
	mux.HandleFunc("/v1/agent/meetings/", s.handleAgentMeetingByID)
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
	// Re-split to handle sub-resources with an ID segment, e.g.
	// "speakers/spk_0" → sub="speakers", subID="spk_0".
	sub := parts[1]
	subID := ""
	if i := strings.IndexByte(sub, '/'); i >= 0 {
		subID = sub[i+1:]
		sub = sub[:i]
	}

	switch sub {
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
	case "speaker-mappings":
		switch r.Method {
		case http.MethodGet:
			mappings, err := s.svc.GetMeetingSpeakerMappings(r.Context(), id)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, mappings)
		case http.MethodPatch:
			var patch notoapi.MeetingSpeakerMappingsPatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
				return
			}
			mappings, err := s.svc.PatchMeetingSpeakerMappings(r.Context(), id, patch)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, mappings)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
	case "speakers":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		if subID == "" {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "speaker id is required", nil))
			return
		}
		var body struct {
			DisplayName string `json:"display_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
			return
		}
		if err := s.svc.UpdateSpeakerName(r.Context(), id, subID, body.DisplayName); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown sub-resource", map[string]any{"resource": sub}))
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

// --- Speaker Profiles ---

func (s *Server) handleSpeakerProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		profiles, err := s.svc.ListSpeakerProfiles(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, notoapi.ListSpeakerProfilesResult{Profiles: profiles, Total: len(profiles)})
	case http.MethodPost:
		var req notoapi.CreateSpeakerProfileRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
			return
		}
		p, err := s.svc.CreateSpeakerProfile(r.Context(), req)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
	}
}

func (s *Server) handleSpeakerProfileByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/speaker-profiles/")
	parts := strings.SplitN(path, "/", 2)
	if parts[0] == "" {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "missing profile id", nil))
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			p, err := s.svc.GetSpeakerProfile(r.Context(), id)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, p)
		case http.MethodPatch:
			var patch notoapi.SpeakerProfilePatch
			if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
				writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
				return
			}
			p, err := s.svc.PatchSpeakerProfile(r.Context(), id, patch)
			if err != nil {
				writeError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, p)
		case http.MethodDelete:
			if err := s.svc.DeleteSpeakerProfile(r.Context(), id); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
		return
	}
	if len(parts) > 1 && parts[1] == "merge" {
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		var req notoapi.MergeSpeakerProfilesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
			return
		}
		p, err := s.svc.MergeSpeakerProfiles(r.Context(), id, req.SourceProfileID)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, p)
		return
	}
	writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown sub-resource", map[string]any{"resource": parts[1]}))
}

// --- Imports ---

// maxUploadBytes caps a remote audio upload (a long meeting is well under this).
const maxUploadBytes = 4 << 30 // 4 GiB

func (s *Server) handleImportAudio(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	// Remote clients upload bytes (octet-stream); local/UDS clients send a
	// path (JSON). Branch on Content-Type so one endpoint serves both.
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/octet-stream") {
		title := r.Header.Get("X-Noto-Title")
		filename := r.Header.Get("X-Noto-Filename")
		body := http.MaxBytesReader(w, r.Body, maxUploadBytes)
		m, job, err := s.svc.ImportAudioStream(r.Context(), body, filename, title)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, notoapi.ImportAudioResult{Meeting: m, Job: job})
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
