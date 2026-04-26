package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	socketPath   = ".noto/capture.sock"
	defaultAddr  = "127.0.0.1:0"
	spawnTimeout = 5 * time.Second
)

type IPCClient struct {
	mu       sync.Mutex
	socketPath string
	conn     net.Conn
	proc     *exec.Cmd
	procMu   sync.Mutex
}

func NewIPCClient() (*IPCClient, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("could not determine home directory: %w", err)
	}
	
	sockPath := filepath.Join(home, socketPath)
	return &IPCClient{
		socketPath: sockPath,
	}, nil
}

func (c *IPCClient) EnsureHelperRunning(ctx context.Context) error {
	c.procMu.Lock()
	defer c.procMu.Unlock()
	
	if c.isHelperRunning() {
		return nil
	}
	
	if _, err := os.Stat(c.socketPath); err == nil {
		os.Remove(c.socketPath)
	}
	
	swiftPath, err := c.findSwiftHelper()
	if err != nil {
		return fmt.Errorf("could not find Swift helper: %w", err)
	}
	
	cmd := exec.CommandContext(ctx, swiftPath, "-socket", c.socketPath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not start Swift helper: %w", err)
	}
	
	c.proc = cmd
	
	waitCtx, cancel := context.WithTimeout(context.Background(), spawnTimeout)
	defer cancel()
	
	if err := c.waitForSocket(waitCtx); err != nil {
		c.proc.Kill()
		return fmt.Errorf("Swift helper did not start in time: %w", err)
	}
	
	return nil
}

func (c *IPCClient) isHelperRunning() bool {
	c.procMu.Lock()
	defer c.procMu.Unlock()
	
	if c.proc == nil || c.proc.Process == nil {
		return false
	}
	
	if c.proc.ProcessState != nil && c.proc.ProcessState.Exited() {
		return false
	}
	
	return true
}

func (c *IPCClient) waitForSocket(ctx context.Context) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if _, err := os.Stat(c.socketPath); err == nil {
				return nil
			}
		}
	}
}

func (c *IPCClient) findSwiftHelper() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", err
	}
	
	helperPath := filepath.Join(filepath.Dir(execPath), "capture-helper")
	if _, err := os.Stat(helperPath); err == nil {
		return helperPath, nil
	}
	
	helperPath = filepath.Join(filepath.Dir(execPath), "..", "share", "noto", "capture-helper")
	if _, err := os.Stat(helperPath); err == nil {
		return helperPath, nil
	}
	
	swiftPath, err := exec.LookPath("swift")
	if err != nil {
		return "", fmt.Errorf("swift not found in PATH and capture-helper not found: %w", err)
	}
	
	return swiftPath, nil
}

func (c *IPCClient) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if c.conn != nil {
		return nil
	}
	
	if err := c.EnsureHelperRunning(ctx); err != nil {
		return err
	}
	
	var conn net.Conn
	var err error
	
	connectCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	
	err = func() error {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		
		for {
			select {
			case <-connectCtx.Done():
				return connectCtx.Err()
			case <-ticker.C:
				conn, err = net.Dial("unix", c.socketPath)
				if err == nil {
					return nil
				}
			}
		}
	}()
	
	if err != nil {
		return fmt.Errorf("could not connect to capture helper: %w", err)
	}
	
	c.conn = conn
	return nil
}

func (c *IPCClient) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	var errs []error
	
	if c.conn != nil {
		if err := c.conn.Close(); err != nil {
			errs = append(errs, err)
		}
		c.conn = nil
	}
	
	c.procMu.Lock()
	defer c.procMu.Unlock()
	
	if c.proc != nil && c.proc.Process != nil {
		if err := c.proc.Process.Kill(); err != nil {
			errs = append(errs, err)
		}
		c.proc = nil
	}
	
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (c *IPCClient) call(ctx context.Context, method string, params, result interface{}) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("could not connect: %w", err)
	}
	
	reqID := int(time.Now().UnixNano())
	
	var paramsJSON json.RawMessage
	if params != nil {
		var err error
		paramsJSON, err = json.Marshal(params)
		if err != nil {
			return fmt.Errorf("could not marshal params: %w", err)
		}
	}
	
	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  method,
		Params:  paramsJSON,
	}
	
	reqData, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("could not marshal request: %w", err)
	}
	
	if err := c.conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("could not set write deadline: %w", err)
	}
	
	if _, err := c.conn.Write(append(reqData, '\n')); err != nil {
		return fmt.Errorf("could not write request: %w", err)
	}
	
	if err := c.conn.SetReadDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return fmt.Errorf("could not set read deadline: %w", err)
	}
	
	var respBuf bytes.Buffer
	buf := make([]byte, 65536)
	for {
		n, err := c.conn.Read(buf)
		if err != nil && err != io.EOF {
			return fmt.Errorf("could not read response: %w", err)
		}
		respBuf.Write(buf[:n])
		if err == io.EOF || n == 0 {
			break
		}
		
		if respBuf.Len() > 0 && respBuf.Bytes()[respBuf.Len()-1] == '\n' {
			break
		}
	}
	
	var resp jsonRPCResponse
	if err := json.Unmarshal(respBuf.Bytes(), &resp); err != nil {
		return fmt.Errorf("could not unmarshal response: %w", err)
	}
	
	if resp.Error != nil {
		return fmt.Errorf("RPC error %d: %s", resp.Error.Code, resp.Error.Message)
	}
	
	if resp.ID != reqID {
		return fmt.Errorf("response ID mismatch: expected %d, got %d", reqID, resp.ID)
	}
	
	if resp.Result != nil && result != nil {
		if err := json.Unmarshal(resp.Result, result); err != nil {
			return fmt.Errorf("could not unmarshal result: %w", err)
		}
	}
	
	return nil
}

type StartParams struct {
	Sources    []string `json:"sources"`
	SampleRate int      `json:"sampleRate"`
}

type StartResult struct {
	Status     string              `json:"status"`
	OutputPath string              `json:"output_path"`
	Sources    []map[string]any   `json:"sources"`
}

func (c *IPCClient) Start(ctx context.Context, sources []string, sampleRate int) (*StartResult, error) {
	params := StartParams{
		Sources:    sources,
		SampleRate: sampleRate,
	}
	
	var result StartResult
	if err := c.call(ctx, "start", params, &result); err != nil {
		return nil, err
	}
	
	return &result, nil
}

type StopResult struct {
	Status         string  `json:"status"`
	OutputPath     string  `json:"output_path"`
	DurationSecs   float64 `json:"duration_seconds"`
	SampleRateHz   int     `json:"sample_rate_hz"`
	Channels       int     `json:"channels"`
	Format         string  `json:"format"`
	Codec          string  `json:"codec"`
	SizeBytes      int64   `json:"size_bytes"`
}

func (c *IPCClient) Stop(ctx context.Context) (*StopResult, error) {
	var result StopResult
	if err := c.call(ctx, "stop", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *IPCClient) Pause(ctx context.Context) error {
	var result map[string]any
	return c.call(ctx, "pause", nil, &result)
}

func (c *IPCClient) Resume(ctx context.Context) error {
	var result map[string]any
	return c.call(ctx, "resume", nil, &result)
}

type AudioLevelResult struct {
	Left    float32 `json:"left"`
	Right   float32 `json:"right"`
	Ambient float32 `json:"ambient"`
}

func (c *IPCClient) GetAudioLevel(ctx context.Context) (*AudioLevelResult, error) {
	var result AudioLevelResult
	if err := c.call(ctx, "getAudioLevel", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

type CapturedAudioResult struct {
	Data  string `json:"data"`
	Size  int    `json:"size"`
	Format string `json:"format"`
}

func (c *IPCClient) GetCapturedAudio(ctx context.Context) (*CapturedAudioResult, error) {
	var result CapturedAudioResult
	if err := c.call(ctx, "getCapturedAudio", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
