package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// This file holds the administrative / agent-facing surface: provider
// configuration, server config + paths, storage maintenance, and the
// read-only agent API. The core meeting/recording/job lifecycle handlers
// live in routes.go.

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

// --- System ---

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request) {
	sys, err := s.svc.GetSystem(r.Context())
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sys)
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

// --- Agent API ---

// GET /v1/agent/meetings?limit=N&after=RFC3339&before=RFC3339
func (s *Server) handleAgentMeetings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	opts := notoapi.AgentListOpts{}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		opts.Limit = n
	}
	if v := r.URL.Query().Get("after"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			opts.After = t
		}
	}
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			opts.Before = t
		}
	}
	res, err := s.svc.AgentListMeetings(r.Context(), opts)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// GET /v1/agent/meetings/{id}
func (s *Server) handleAgentMeetingByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/agent/meetings/")
	if id == "" {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "missing meeting id", nil))
		return
	}
	result, err := s.svc.AgentGetMeeting(r.Context(), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
