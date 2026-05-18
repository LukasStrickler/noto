package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// handleSSE streams events from the service.eventHub to the client.
// metersOnly limits emissions to meter events (used by
// /v1/recording/meters); otherwise everything is forwarded.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request, metersOnly bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, notoapi.NewError(notoapi.CodeInternal, "streaming not supported", nil))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	sub, unsubscribe := s.svc.Subscribe()
	defer unsubscribe()

	// Send an initial "hello" so clients know the stream is alive.
	fmt.Fprintf(w, ": ready\n\n")
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-sub:
			if !ok {
				return
			}
			if metersOnly && ev.Kind != notoapi.EventMeter {
				continue
			}
			payload, _ := json.Marshal(ev)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, payload)
			flusher.Flush()
		}
	}
}
