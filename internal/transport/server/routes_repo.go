package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/lukasstrickler/noto/internal/core/artifacts"
	"github.com/lukasstrickler/noto/internal/platform/repo"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// The /v1/repo/* API is the artifact store over HTTP: a RemoteArtifactRepository
// on an edge node (compute local, data off-site) writes its pipeline output
// here and reads it back. Writes route through service methods that ALSO index,
// so this data plane stays the queryable source of truth. See repo.RemoteArtifactRepository.

func (s *Server) handleRepoMeetings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		meetings, err := s.svc.RepoListMeetings(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, meetings)
	case http.MethodPost:
		var req repo.RepoCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
			return
		}
		if err := s.svc.RepoCreateMeeting(r.Context(), req.ID, req.Opts); err != nil {
			writeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
	}
}

func (s *Server) handleRepoMeetingByID(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/v1/repo/meetings/")
	parts := strings.SplitN(path, "/", 2)
	head := parts[0]
	if head == "" {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "missing meeting id", nil))
		return
	}
	if head == "count" {
		n, err := s.svc.RepoCountMeetings(r.Context())
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, repo.RepoCountResponse{Count: n})
		return
	}
	id, err := uuid.Parse(head)
	if err != nil {
		writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "meeting id is not a valid UUID", nil))
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			sm, err := s.svc.RepoGetMeeting(r.Context(), id)
			if err != nil {
				writeRepoError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, sm)
		case http.MethodDelete:
			if err := s.svc.RepoDeleteMeeting(r.Context(), id); err != nil {
				writeRepoError(w, err)
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
		switch r.Method {
		case http.MethodGet:
			t, err := s.svc.RepoLoadTranscript(r.Context(), id)
			if err != nil {
				writeRepoError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, t)
		case http.MethodPut:
			var t artifacts.Transcript
			if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
				writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
				return
			}
			if err := s.svc.RepoSaveTranscript(r.Context(), id, &t); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
	case "summary":
		switch r.Method {
		case http.MethodGet:
			md, sum, err := s.svc.RepoLoadSummary(r.Context(), id)
			if err != nil {
				writeRepoError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, repo.RepoSummaryBody{Markdown: md, Summary: sum})
		case http.MethodPut:
			var body repo.RepoSummaryBody
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "invalid JSON", nil))
				return
			}
			if err := s.svc.RepoSaveSummary(r.Context(), id, body.Markdown, body.Summary); err != nil {
				writeError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		default:
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
		}
	case "verify":
		if r.Method != http.MethodPost {
			writeError(w, notoapi.NewError(notoapi.CodeInvalidRequest, "method not allowed", nil))
			return
		}
		if err := s.svc.RepoVerify(r.Context(), id); err != nil {
			writeRepoError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "unknown sub-resource", map[string]any{"resource": parts[1]}))
	}
}

// writeRepoError maps repo.ErrNotFound onto a 404 so RemoteArtifactRepository
// can translate it back to the not-found sentinel; everything else flows through
// the standard envelope.
func writeRepoError(w http.ResponseWriter, err error) {
	if errors.Is(err, repo.ErrNotFound) {
		writeError(w, notoapi.NewError(notoapi.CodeNotFound, "meeting not found", nil))
		return
	}
	writeError(w, err)
}
