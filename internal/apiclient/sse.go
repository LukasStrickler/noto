package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/lukasstrickler/noto/internal/notoapi"
)

// streamSSE opens an SSE stream against the given path and forwards
// parsed Event values on the returned channel until ctx is canceled or
// the server closes the stream.
func (c *httpClient) streamSSE(ctx context.Context, path string) (<-chan notoapi.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		body, _ := bufio.NewReader(resp.Body).ReadString('\n')
		resp.Body.Close()
		return nil, fmt.Errorf("sse %s: %s", resp.Status, strings.TrimSpace(body))
	}

	out := make(chan notoapi.Event, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		reader := bufio.NewReader(resp.Body)
		var eventType, dataBuf string
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if dataBuf == "" {
					eventType = ""
					continue
				}
				var ev notoapi.Event
				if jerr := json.Unmarshal([]byte(dataBuf), &ev); jerr == nil {
					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}
				}
				eventType = ""
				dataBuf = ""
			case strings.HasPrefix(line, "event:"):
				eventType = strings.TrimSpace(line[len("event:"):])
				_ = eventType // kind also lives inside the JSON payload
			case strings.HasPrefix(line, "data:"):
				dataBuf += strings.TrimSpace(line[len("data:"):])
			}
		}
	}()
	return out, nil
}
