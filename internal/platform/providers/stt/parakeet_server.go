package stt

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
)

// parakeetServerEngine is an STTEngine backed by the warm NeMo Parakeet server
// (scripts/parakeet_stt_server.py): the model loads ONCE and every chunk is
// one JSONL request over stdin/stdout, so the model load + CUDA init are paid
// a single time across ALL chunks and ALL meetings — not per meeting — and a
// {"wavs": [...]} request decodes as ONE padded batch (encoder AND TDT decoder
// batched), which is what lifted STT past the sherpa stack's ~132× aggregate
// ceiling (BOTTLENECK.md, 17th pass).
//
// It deliberately reuses the CLI engine's chunking (chunkToWAVs) and word
// reconstruction (sherpaResult.words + stitchWords): the server emits the same
// text/tokens/timestamps/durations fields (one token per word, leading-space
// word starts), so the Go side reconstructs exactly the server's word offsets.
// Model files resolve server-side from the HF cache (NOTO_PARAKEET_MODEL) — no
// local ONNX dir is needed. Python (torch + NeMo) is required only where this
// engine is selected; the default local path keeps the dependency-free CLI
// engine.
//
// Requests are serialized (the GPU work is serial anyway) and a dead process is
// restarted once per call; cancelling the ctx kills the process.
type parakeetServerEngine struct {
	python     string
	script     string
	ffmpeg     string
	provider   string
	chunkBatch int

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	stderr *serverTailBuffer
	nextID int64
}

func init() {
	RegisterSTTEngine("parakeet-server", newParakeetServerEngine)
	RegisterSTTEngine("cuda-parakeet-server", newCUDAParakeetServerEngine)
}

func newCUDAParakeetServerEngine(cfg EngineConfig) (STTEngine, error) {
	cfg.Options = withDefaultOption(cfg.Options, "provider", "cuda")
	return newParakeetServerEngine(cfg)
}

func newParakeetServerEngine(cfg EngineConfig) (STTEngine, error) {
	opt := func(k, env string) string {
		if cfg.Options != nil && cfg.Options[k] != "" {
			return cfg.Options[k]
		}
		return os.Getenv(env)
	}
	python := opt("python", "NOTO_PARAKEET_PYTHON")
	if python == "" {
		python = "python3"
	}
	if _, err := exec.LookPath(python); err != nil {
		return nil, fmt.Errorf("stt/parakeet: python %q not found (set NOTO_PARAKEET_PYTHON): %w", python, err)
	}
	script := opt("script", "NOTO_PARAKEET_SCRIPT")
	if script == "" {
		return nil, fmt.Errorf("stt/parakeet: server script not set (set NOTO_PARAKEET_SCRIPT to scripts/parakeet_stt_server.py)")
	}
	if _, err := os.Stat(script); err != nil {
		return nil, fmt.Errorf("stt/parakeet: server script %q not found", script)
	}
	ffmpeg := opt("ffmpeg", "NOTO_FFMPEG")
	if ffmpeg == "" {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpeg = p
		}
	}
	provider := opt("provider", "NOTO_PARAKEET_PROVIDER")
	if provider == "" {
		provider = "cpu"
	}
	// chunk_batch > 1 sends that many of ONE meeting's 120 s chunks per request,
	// so the warm server decodes them as one padded batch instead of N sequential
	// single-chunk decodes. Default 1 keeps the proven sequential path; the
	// benchmark runner raises it so a whole meeting travels per request. VRAM
	// grows with the group size — size it to the card.
	chunkBatch := 1
	if b := opt("chunk_batch", "NOTO_PARAKEET_CHUNK_BATCH"); b != "" {
		if n, err := strconv.Atoi(b); err == nil && n > 1 {
			chunkBatch = n
		}
	}
	return &parakeetServerEngine{
		python: python, script: script, ffmpeg: ffmpeg,
		provider: provider, chunkBatch: chunkBatch,
	}, nil
}

func (e *parakeetServerEngine) Name() string { return "parakeet-server" }

// parakeetResp is one protocol line (handshake or per-chunk result). The result
// fields mirror the CLI's JSON so sherpaResult.words reconstructs words identically.
type parakeetResp struct {
	Ready       *bool          `json:"ready,omitempty"`
	Provider    string         `json:"provider,omitempty"`
	ID          int64          `json:"id"`
	Error       string         `json:"error,omitempty"`
	Text        string         `json:"text"`
	Tokens      []string       `json:"tokens"`
	Timestamps  []float64      `json:"timestamps"`
	Durations   []float64      `json:"durations"`
	Confidences []float64      `json:"confidences,omitempty"`
	Results     []parakeetResp `json:"results,omitempty"`
}

func (e *parakeetServerEngine) Recognize(ctx context.Context, audio []byte, _ TranscribeOptions) ([]EngineWord, error) {
	// Reuse the CLI engine's chunker so windows + offsets are identical.
	files, offsets, cleanups, err := chunkToWAVs(ctx, audio, e.ffmpeg)
	defer runCleanups(cleanups)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	results := make([]sherpaResult, len(files))
	group := e.chunkBatch
	if group < 1 {
		group = 1
	}
	for start := 0; start < len(files); start += group {
		end := min(start+group, len(files))
		// Batch a group of this meeting's chunks through ONE decode_streams
		// request (one padded encoder pass) when chunk_batch > 1; otherwise the
		// proven per-chunk path. A failed/short batch falls back to per-chunk for
		// that group, so batching can never lose a meeting.
		if end-start > 1 {
			resps, rerr := e.requestBatchLocked(ctx, files[start:end])
			if rerr == nil && len(resps) == end-start {
				for i, resp := range resps {
					if resp.Error != "" {
						return nil, fmt.Errorf("stt/parakeet: %s", resp.Error)
					}
					results[start+i] = sherpaFromParakeet(resp)
				}
				continue
			}
			e.stopLocked()
		}
		for i := start; i < end; i++ {
			resp, rerr := e.requestLocked(ctx, files[i])
			if rerr != nil {
				// One restart + retry, mirroring the diarizer: a crashed warm process
				// costs a reload, not the meeting.
				e.stopLocked()
				resp, rerr = e.requestLocked(ctx, files[i])
				if rerr != nil {
					return nil, rerr
				}
			}
			if resp.Error != "" {
				return nil, fmt.Errorf("stt/parakeet: %s", resp.Error)
			}
			results[i] = sherpaFromParakeet(*resp)
		}
	}
	return stitchWords(results, offsets), nil
}

func (e *parakeetServerEngine) RecognizeBatch(ctx context.Context, audios [][]byte, opts TranscribeOptions) ([][]EngineWord, error) {
	var (
		allFiles []string
		cleanups []func()
	)
	type span struct {
		start, end int
		offsets    []float64
	}
	spans := make([]span, len(audios))
	defer func() { runCleanups(cleanups) }()
	for i, audio := range audios {
		files, offsets, cl, err := chunkToWAVs(ctx, audio, e.ffmpeg)
		cleanups = append(cleanups, cl...)
		if err != nil {
			return nil, err
		}
		spans[i] = span{start: len(allFiles), end: len(allFiles) + len(files), offsets: offsets}
		allFiles = append(allFiles, files...)
	}
	if len(allFiles) == 0 {
		return make([][]EngineWord, len(audios)), nil
	}

	e.mu.Lock()
	resps, err := e.requestBatchLocked(ctx, allFiles)
	if err != nil || len(resps) != len(allFiles) {
		e.stopLocked()
		e.mu.Unlock()
		out := make([][]EngineWord, len(audios))
		for i, audio := range audios {
			w, ferr := e.Recognize(ctx, audio, opts)
			if ferr != nil {
				return nil, ferr
			}
			out[i] = w
		}
		return out, nil
	}
	e.mu.Unlock()
	results := make([]sherpaResult, len(resps))
	for i, resp := range resps {
		if resp.Error != "" {
			return nil, fmt.Errorf("stt/parakeet: %s", resp.Error)
		}
		results[i] = sherpaFromParakeet(resp)
	}
	out := make([][]EngineWord, len(audios))
	for i, sp := range spans {
		out[i] = stitchWords(results[sp.start:sp.end], sp.offsets)
	}
	return out, nil
}

func sherpaFromParakeet(resp parakeetResp) sherpaResult {
	return sherpaResult{
		Text:        resp.Text,
		Tokens:      resp.Tokens,
		Timestamps:  resp.Timestamps,
		Durations:   resp.Durations,
		Confidences: resp.Confidences,
	}
}

// requestLocked sends one chunk to the (started-on-demand) server and reads its
// matching response. Caller holds e.mu.
func (e *parakeetServerEngine) requestLocked(ctx context.Context, wavPath string) (*parakeetResp, error) {
	if err := e.startLocked(); err != nil {
		return nil, err
	}
	cmd := e.cmd
	stop := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer stop()

	e.nextID++
	req, err := json.Marshal(map[string]any{"id": e.nextID, "wav": wavPath})
	if err != nil {
		return nil, err
	}
	if _, err := e.stdin.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("stt/parakeet: write request: %w (stderr tail: %s)", err, e.stderr.tail())
	}
	for e.stdout.Scan() {
		var resp parakeetResp
		if json.Unmarshal(e.stdout.Bytes(), &resp) != nil {
			continue // not a protocol line; ignore defensively
		}
		if resp.ID == e.nextID {
			return &resp, nil
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return nil, fmt.Errorf("stt/parakeet: server exited mid-request (stderr tail: %s)", e.stderr.tail())
}

func (e *parakeetServerEngine) requestBatchLocked(ctx context.Context, wavPaths []string) ([]parakeetResp, error) {
	if err := e.startLocked(); err != nil {
		return nil, err
	}
	cmd := e.cmd
	stop := context.AfterFunc(ctx, func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})
	defer stop()

	e.nextID++
	req, err := json.Marshal(map[string]any{"id": e.nextID, "wavs": wavPaths})
	if err != nil {
		return nil, err
	}
	if _, err := e.stdin.Write(append(req, '\n')); err != nil {
		return nil, fmt.Errorf("stt/parakeet: write batch request: %w (stderr tail: %s)", err, e.stderr.tail())
	}
	for e.stdout.Scan() {
		var resp parakeetResp
		if json.Unmarshal(e.stdout.Bytes(), &resp) != nil {
			continue
		}
		if resp.ID != e.nextID {
			continue
		}
		if resp.Error != "" {
			return nil, fmt.Errorf("stt/parakeet: %s", resp.Error)
		}
		return resp.Results, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, ctxErr
	}
	return nil, fmt.Errorf("stt/parakeet: server exited mid-batch request (stderr tail: %s)", e.stderr.tail())
}

// startLocked spawns the server if it isn't running and waits for its ready
// handshake (where model load + CUDA init happen — once per process). Caller
// holds e.mu.
func (e *parakeetServerEngine) startLocked() error {
	if e.cmd != nil {
		return nil
	}
	cmd := exec.Command(e.python, e.script)
	cmd.Env = append(os.Environ(),
		"NOTO_PARAKEET_PROVIDER="+e.provider,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr := &serverTailBuffer{max: 4096}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("stt/parakeet: start %s %s: %w", e.python, e.script, err)
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var resp parakeetResp
		if json.Unmarshal(sc.Bytes(), &resp) != nil || resp.Ready == nil {
			continue
		}
		if !*resp.Ready {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("stt/parakeet: server not ready: %s", resp.Error)
		}
		e.cmd = cmd
		e.stdin = stdin
		e.stdout = sc
		e.stderr = stderr
		return nil
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return fmt.Errorf("stt/parakeet: server exited before ready handshake (stderr tail: %s)", stderr.tail())
}

// stopLocked tears the server down so the next request restarts it. Caller holds e.mu.
func (e *parakeetServerEngine) stopLocked() {
	if e.cmd == nil {
		return
	}
	if e.stdin != nil {
		_ = e.stdin.Close()
	}
	if e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
	}
	_ = e.cmd.Wait()
	e.cmd, e.stdin, e.stdout = nil, nil, nil
}

// serverTailBuffer keeps the last max bytes of stderr — enough for an error
// message without growing unboundedly under a long-lived process.
type serverTailBuffer struct {
	mu  sync.Mutex
	buf []byte
	max int
}

func (t *serverTailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.max {
		t.buf = t.buf[len(t.buf)-t.max:]
	}
	return len(p), nil
}

func (t *serverTailBuffer) tail() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(t.buf)
}
