package diarize

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
)

// pyannoteServerEngine is a SegmentEngine backed by the warm pyannote server
// (scripts/pyannote_diar_server.py): ONE process, ONE CUDA context, N worker
// threads inside, each owning its own pipeline replica on its own CUDA stream.
// The pipeline (segmentation + embedding + clustering, GPU-batched by pyannote
// itself) loads once per process, and every Segment call is one JSONL request
// whose response is demultiplexed by id — responses arrive OUT OF ORDER as
// meetings finish.
//
// Measured shape (BOTTLENECK.md, 19th pass): one process with N worker threads
// is throughput-NEUTRAL vs N processes (~130–140× aggregate either way — the
// ceiling is pyannote's GPU compute, not serving topology) but −8 GB peak VRAM
// and one host python/torch stack instead of N, with per-meeting output
// identical. The Go side is shaped so one process serves many meetings:
//
//   - every engine instance with the same config SHARES one process (a
//     package-level registry keyed by config), so a BENCH_DIAR_POOL of N
//     handles feeds one server instead of spawning N;
//   - Segment does NOT serialize on an engine mutex: requests are written
//     (under a small write lock) tagged with unique ids, and a single reader
//     goroutine demultiplexes responses to per-request channels.
//
// Lifecycle: the process starts lazily on the first request; a transport-level
// failure (crash, EOF) fails all in-flight requests, and each caller retries
// once against a freshly started process. A cancelled ctx abandons its own
// wait WITHOUT killing the shared process — other meetings' requests stay in
// flight. The Python deps (pyannote.audio, torch) are required only where this
// engine is selected — the default local path keeps the dependency-free sherpa
// engine. The default pipeline is the UNGATED community mirror of community-1
// (no Hugging Face account or token needed by anyone).
type pyannoteServerEngine struct {
	ffmpeg string
	client *pyannoteClient
}

func init() {
	RegisterSegmentEngine("pyannote-server", newPyannoteServerEngine)
	RegisterSegmentEngine("cuda-pyannote-server", newCUDAPyannoteServerEngine)
}

func newCUDAPyannoteServerEngine(cfg EngineConfig) (SegmentEngine, error) {
	cfg.Options = withDefaultOption(cfg.Options, "device", "cuda")
	return newPyannoteServerEngine(cfg)
}

func newPyannoteServerEngine(cfg EngineConfig) (SegmentEngine, error) {
	opt := func(k, env string) string {
		if cfg.Options != nil && cfg.Options[k] != "" {
			return cfg.Options[k]
		}
		return os.Getenv(env)
	}
	python := opt("python", "NOTO_PYANNOTE_PYTHON")
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return nil, fmt.Errorf("diarize/pyannote: python %q not found (set NOTO_PYANNOTE_PYTHON): %w", python, err)
	}
	script := opt("script", "NOTO_PYANNOTE_SCRIPT")
	if script == "" {
		return nil, fmt.Errorf("diarize/pyannote: server script not set (set NOTO_PYANNOTE_SCRIPT to scripts/pyannote_diar_server.py)")
	}
	if !fileExists(script) {
		return nil, fmt.Errorf("diarize/pyannote: server script %q not found", script)
	}
	ffmpeg := opt("ffmpeg", "NOTO_FFMPEG")
	if ffmpeg == "" {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpeg = p
		}
	}
	return &pyannoteServerEngine{
		ffmpeg: ffmpeg,
		client: pyannoteClientFor(python, script, opt("device", "NOTO_PYANNOTE_DEVICE"), opt("pipeline", "NOTO_PYANNOTE_PIPELINE")),
	}, nil
}

func (e *pyannoteServerEngine) Name() string { return "pyannote-server" }

// pyannoteResp is one protocol line from the server (handshake or result).
type pyannoteResp struct {
	Ready  *bool  `json:"ready,omitempty"`
	Device string `json:"device,omitempty"`
	ID     int64  `json:"id"`
	Error  string `json:"error,omitempty"`
	Turns  []struct {
		Speaker string  `json:"speaker"`
		Start   float64 `json:"start"`
		End     float64 `json:"end"`
	} `json:"turns"`
	Results []pyannoteResp `json:"results,omitempty"`
}

func engineTurnsFromPyannote(resp pyannoteResp) []EngineTurn {
	turns := make([]EngineTurn, 0, len(resp.Turns))
	for _, t := range resp.Turns {
		turns = append(turns, EngineTurn{Speaker: t.Speaker, StartSeconds: t.Start, EndSeconds: t.End})
	}
	return turns
}

func (e *pyannoteServerEngine) Segment(ctx context.Context, audio []byte, opts DiarizeOptions) ([]EngineTurn, error) {
	wavPath, cleanup, err := ensureWAVFile(ctx, audio, e.ffmpeg)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	resp, err := e.client.request(ctx, wavPath, opts.NumSpeakers)
	if err != nil {
		// One retry against a restarted process: a crashed warm server should
		// cost a reload, not the meeting. A second failure is the real error.
		if ctx.Err() != nil {
			return nil, err
		}
		resp, err = e.client.request(ctx, wavPath, opts.NumSpeakers)
		if err != nil {
			return nil, err
		}
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("diarize/pyannote: %s", resp.Error)
	}
	return engineTurnsFromPyannote(*resp), nil
}

// SegmentBatch fans the audios out as CONCURRENT single-wav requests — with
// one shared server, cross-meeting concurrency (the server's worker threads
// overlapping in one CUDA context) replaces an in-order {"wavs": [...]}
// loop, so a batch finishes in max(meeting) instead of sum(meetings).
func (e *pyannoteServerEngine) SegmentBatch(ctx context.Context, audios [][]byte, opts []DiarizeOptions) ([][]EngineTurn, error) {
	if len(opts) != len(audios) {
		return nil, fmt.Errorf("diarize/pyannote: SegmentBatch needs one DiarizeOptions per audio (%d audios, %d opts)", len(audios), len(opts))
	}
	out := make([][]EngineTurn, len(audios))
	errs := make([]error, len(audios))
	var wg sync.WaitGroup
	for i := range audios {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			out[i], errs[i] = e.Segment(ctx, audios[i], opts[i])
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// --- shared warm-process client ---------------------------------------------

var (
	pyannoteClientsMu sync.Mutex
	pyannoteClients   = map[string]*pyannoteClient{}
)

// pyannoteClientFor returns the ONE client (= one warm server process) for a
// given config, creating it on first use. Engines are cheap handles onto it,
// so a benchmark pool of N engines shares one CUDA context instead of holding N.
func pyannoteClientFor(python, script, device, pipeline string) *pyannoteClient {
	key := python + "\x00" + script + "\x00" + device + "\x00" + pipeline
	pyannoteClientsMu.Lock()
	defer pyannoteClientsMu.Unlock()
	if c, ok := pyannoteClients[key]; ok {
		return c
	}
	c := &pyannoteClient{python: python, script: script, device: device, pipeline: pipeline}
	pyannoteClients[key] = c
	return c
}

// pyannoteClient owns one warm server process and demultiplexes concurrent
// requests onto it. The zero point of the design: writers never block on each
// other's GPU work — only on the (instant) stdin write — and the single reader
// goroutine routes each response line to whichever caller registered its id.
type pyannoteClient struct {
	python, script, device, pipeline string

	mu     sync.Mutex // guards proc start/clear + nextID
	proc   *pyannoteProc
	nextID int64
}

// pyannoteProc is one server-process incarnation. A new incarnation gets fresh
// waiter bookkeeping, so a late line from a dead predecessor can't cross over.
type pyannoteProc struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr *tailBuffer

	writeMu sync.Mutex // serializes request lines onto stdin

	waitMu  sync.Mutex
	waiters map[int64]chan *pyannoteResp

	done chan struct{} // closed by the reader when the process is gone
}

func (c *pyannoteClient) request(ctx context.Context, wavPath string, numSpeakers int) (*pyannoteResp, error) {
	p, id, err := c.ensureStartedAndClaimID()
	if err != nil {
		return nil, err
	}

	ch := make(chan *pyannoteResp, 1) // buffered: the reader never blocks on a slow caller
	p.waitMu.Lock()
	p.waiters[id] = ch
	p.waitMu.Unlock()
	abandon := func() {
		p.waitMu.Lock()
		delete(p.waiters, id)
		p.waitMu.Unlock()
	}

	req, err := json.Marshal(map[string]any{"id": id, "wav": wavPath, "num_speakers": numSpeakers})
	if err != nil {
		abandon()
		return nil, err
	}
	p.writeMu.Lock()
	_, werr := p.stdin.Write(append(req, '\n'))
	p.writeMu.Unlock()
	if werr != nil {
		abandon()
		c.clear(p)
		return nil, fmt.Errorf("diarize/pyannote: write request: %w (stderr tail: %s)", werr, p.stderr.tail())
	}

	select {
	case resp := <-ch:
		return resp, nil
	case <-p.done:
		abandon()
		return nil, fmt.Errorf("diarize/pyannote: server exited mid-request (stderr tail: %s)", p.stderr.tail())
	case <-ctx.Done():
		// Abandon only OUR wait: the process is shared, so a caller's
		// cancellation must not kill other meetings' in-flight requests. The
		// server's eventual response finds no waiter and is dropped.
		abandon()
		return nil, ctx.Err()
	}
}

// ensureStartedAndClaimID starts the server if needed (blocking on its ready
// handshake — model load + CUDA init, paid once per process) and reserves a
// request id on the running incarnation, atomically under c.mu so an id is
// never claimed against a process another caller just declared dead.
func (c *pyannoteClient) ensureStartedAndClaimID() (*pyannoteProc, int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.proc == nil {
		p, err := c.start()
		if err != nil {
			return nil, 0, err
		}
		c.proc = p
	}
	c.nextID++
	return c.proc, c.nextID, nil
}

// clear forgets a dead incarnation so the next request restarts the server.
func (c *pyannoteClient) clear(p *pyannoteProc) {
	c.mu.Lock()
	if c.proc == p {
		c.proc = nil
	}
	c.mu.Unlock()
}

func (c *pyannoteClient) start() (*pyannoteProc, error) {
	cmd := exec.Command(c.python, c.script)
	env := os.Environ()
	if c.device != "" {
		env = append(env, "NOTO_PYANNOTE_DEVICE="+c.device)
	}
	if c.pipeline != "" {
		env = append(env, "NOTO_PYANNOTE_PIPELINE="+c.pipeline)
	}
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &tailBuffer{max: 4096}
	// The client keeps the last few KB of stderr for error messages. Under
	// NOTO_PYANNOTE_SERVER_STDERR=1 it ALSO streams to the parent's stderr so
	// the per-request stage timing (load/seg/emb/cluster) is visible in
	// benchmark output for profiling; off by default so normal runs stay quiet.
	if os.Getenv("NOTO_PYANNOTE_SERVER_STDERR") == "1" {
		cmd.Stderr = io.MultiWriter(stderr, os.Stderr)
	} else {
		cmd.Stderr = stderr
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("diarize/pyannote: start %s %s: %w", c.python, c.script, err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	ready := false
	for sc.Scan() {
		var resp pyannoteResp
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.Ready == nil {
			continue // not a protocol line; ignore defensively
		}
		if !*resp.Ready {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("diarize/pyannote: server not ready: %s", resp.Error)
		}
		ready = true
		break
	}
	if !ready {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("diarize/pyannote: server exited before ready handshake (stderr tail: %s)", stderr.tail())
	}

	p := &pyannoteProc{
		cmd:     cmd,
		stdin:   stdin,
		stderr:  stderr,
		waiters: map[int64]chan *pyannoteResp{},
		done:    make(chan struct{}),
	}
	go c.readLoop(p, sc)
	return p, nil
}

// readLoop is the per-incarnation response demultiplexer: it routes each id
// line to its registered waiter and, when the stream ends (crash/EOF), wakes
// every remaining waiter via done and retires the incarnation.
func (c *pyannoteClient) readLoop(p *pyannoteProc, sc *bufio.Scanner) {
	for sc.Scan() {
		var resp pyannoteResp
		if json.Unmarshal(sc.Bytes(), &resp) != nil {
			continue
		}
		p.waitMu.Lock()
		ch, ok := p.waiters[resp.ID]
		if ok {
			delete(p.waiters, resp.ID)
		}
		p.waitMu.Unlock()
		if ok {
			ch <- &resp // cap 1, never blocks
		}
	}
	close(p.done)
	c.clear(p)
	_ = p.stdin.Close()
	if p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
	_ = p.cmd.Wait()
}

// tailBuffer keeps the last max bytes written — enough stderr for an error
// message without growing unboundedly under a chatty long-lived process.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *tailBuffer) tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}

var (
	_ SegmentEngine      = (*pyannoteServerEngine)(nil)
	_ BatchSegmentEngine = (*pyannoteServerEngine)(nil)
)
