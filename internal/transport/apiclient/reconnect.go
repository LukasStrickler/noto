package apiclient

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

const (
	defaultHeartbeatTimeout = 30 * time.Second
	defaultMaxRetries       = 10
)

type backoff struct {
	base     time.Duration
	maxDelay time.Duration
	cur      time.Duration
}

func newBackoff(base time.Duration) *backoff {
	return &backoff{
		base:     base,
		maxDelay: 60 * time.Second,
		cur:      0,
	}
}

func (b *backoff) Duration() time.Duration {
	if b.cur == 0 {
		b.cur = b.base
	}
	return b.cur
}

func (b *backoff) Next() {
	if b.cur == 0 {
		b.cur = b.base
	} else {
		b.cur = b.cur * 2
		if b.cur > b.maxDelay {
			b.cur = b.maxDelay
		}
	}
}

func (b *backoff) Reset() {
	b.cur = 0
}

type sseParser struct {
	ctx context.Context
	r   io.Reader
}

func (p *sseParser) parse() (<-chan notoapi.Event, <-chan error) {
	out := make(chan notoapi.Event, 64)
	errCh := make(chan error, 1)
	go func() {
		defer close(out)
		reader := bufio.NewReader(p.r)
		var dataBuf string
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err != io.EOF {
					select {
					case errCh <- err:
					default:
					}
				}
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if dataBuf == "" {
					continue
				}
				var ev notoapi.Event
				if jerr := json.Unmarshal([]byte(dataBuf), &ev); jerr == nil {
					select {
					case out <- ev:
					case <-p.ctx.Done():
						return
					}
				}
				dataBuf = ""
			case strings.HasPrefix(line, "event:"):
				_ = strings.TrimSpace(line[len("event:"):])
			case strings.HasPrefix(line, "data:"):
				dataBuf += strings.TrimSpace(line[len("data:"):])
			}
		}
	}()
	return out, errCh
}

func (c *httpClient) StreamEventsWithReconnect(ctx context.Context, path string) (<-chan notoapi.Event, error) {
	return c.streamEventsWithReconnect(ctx, path, defaultMaxRetries)
}

func (c *httpClient) streamEventsWithReconnect(ctx context.Context, path string, maxRetries int) (<-chan notoapi.Event, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	out := make(chan notoapi.Event, 64)
	bo := newBackoff(1 * time.Second)

	var reconnectMu sync.Mutex
	var forceReconnect bool

	go func() {
		defer close(out)

		retries := 0
		for {
			if ctx.Err() != nil {
				return
			}

			reconnectMu.Lock()
			forceReconnect = false
			reconnectMu.Unlock()

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
			if err != nil {
				return
			}
			req.Header.Set("Accept", "text/event-stream")
			req.Header.Set("Cache-Control", "no-cache")
			if c.token != "" {
				req.Header.Set("Authorization", "Bearer "+c.token)
			}

			resp, err := c.stream.Do(req)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				if retries >= maxRetries {
					return
				}
				retries++
				waitDuration := bo.Duration()
				bo.Next()

				select {
				case <-ctx.Done():
					return
				case <-time.After(waitDuration):
					continue
				}
			}

			if resp.StatusCode >= 400 {
				resp.Body.Close()
				if ctx.Err() != nil {
					return
				}
				if retries >= maxRetries {
					return
				}
				retries++
				waitDuration := bo.Duration()
				bo.Next()

				select {
				case <-ctx.Done():
					return
				case <-time.After(waitDuration):
					continue
				}
			}

			retries = 0
			bo.Reset()

			parser := &sseParser{ctx: ctx, r: resp.Body}
			events, parseErrCh := parser.parse()

			heartbeat := time.NewTimer(c.heartbeatTimeout)
		streamLoop:
			for {
				select {
				case <-ctx.Done():
					heartbeat.Stop()
					return
				case ev, ok := <-events:
					heartbeat.Stop()
					if !ok {
						reconnectMu.Lock()
						shouldReconnect := forceReconnect
						reconnectMu.Unlock()

						if !shouldReconnect {
							if parseErrCh != nil {
								select {
								case err := <-parseErrCh:
									if err != nil && ctx.Err() == nil {
										if retries >= maxRetries {
											return
										}
										retries++
										waitDuration := bo.Duration()
										bo.Next()
										select {
										case <-ctx.Done():
											return
										case <-time.After(waitDuration):
											break streamLoop
										}
									}
								default:
								}
							}
							return
						}
						if retries >= maxRetries {
							return
						}
						retries++
						waitDuration := bo.Duration()
						bo.Next()

						select {
						case <-ctx.Done():
							return
						case <-time.After(waitDuration):
							break streamLoop
						}
					}

					reconnectMu.Lock()
					forceReconnect = false
					reconnectMu.Unlock()
					retries = 0
					bo.Reset()

					select {
					case out <- ev:
					case <-ctx.Done():
						return
					}

					heartbeat = time.NewTimer(c.heartbeatTimeout)
				case <-heartbeat.C:
					reconnectMu.Lock()
					forceReconnect = true
					reconnectMu.Unlock()
					resp.Body.Close()
					if retries >= maxRetries {
						return
					}
					retries++
					waitDuration := bo.Duration()
					bo.Next()

					select {
					case <-ctx.Done():
						return
					case <-time.After(waitDuration):
						break streamLoop
					}
				}
			}
		}
	}()

	return out, nil
}
