package stt

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// sherpaEngine is a local STTEngine that shells out to the prebuilt
// `sherpa-onnx-offline` binary (no cgo, no Python). It is the runtime behind the
// local-first STT path: a NeMo/Parakeet transducer producing word timestamps,
// entirely on-device. The binary auto-resamples, so any mono WAV works.
//
// Registered unconditionally (the registration is dependency-free); the factory
// fails with an actionable error when the binary/model aren't installed, so the
// pipeline degrades gracefully and `noto models download` is the fix.
type sherpaEngine struct {
	bin       string // path to sherpa-onnx-offline
	libDir    string // dir added to LD_LIBRARY_PATH (libsherpa-onnx-c-api.so, libonnxruntime.so)
	ffmpeg    string // ffmpeg path/name for decoding non-WAV input ("" = WAV only)
	encoder   string
	decoder   string
	joiner    string
	tokens    string
	modelType string
	provider  string
	threads   int
}

func init() {
	RegisterSTTEngine("sherpa-parakeet", newSherpaEngine)
	RegisterSTTEngine("cuda-parakeet", newCUDASherpaEngine)
}

func newCUDASherpaEngine(cfg EngineConfig) (STTEngine, error) {
	cfg.Options = withDefaultOption(cfg.Options, "provider", "cuda")
	return newSherpaEngine(cfg)
}

// newSherpaEngine resolves the binary, shared-lib dir, and model files. Lookup
// order: EngineConfig.Options, then env (NOTO_SHERPA_BIN / NOTO_SHERPA_LIB), then
// sensible defaults relative to the model dir. Model files are auto-detected in
// ModelDir (encoder/decoder/joiner/tokens, int8 preferred).
func newSherpaEngine(cfg EngineConfig) (STTEngine, error) {
	opt := func(k, env string) string {
		if cfg.Options != nil && cfg.Options[k] != "" {
			return cfg.Options[k]
		}
		return os.Getenv(env)
	}
	bin := opt("bin", "NOTO_SHERPA_BIN")
	if bin == "" {
		if p, err := exec.LookPath("sherpa-onnx-offline"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		return nil, fmt.Errorf("sherpa: sherpa-onnx-offline binary not found (set NOTO_SHERPA_BIN or run: noto models download)")
	}
	if _, err := os.Stat(bin); err != nil {
		return nil, fmt.Errorf("sherpa: binary %q: %w", bin, err)
	}
	libDir := opt("lib", "NOTO_SHERPA_LIB")
	if libDir == "" {
		libDir = filepath.Join(filepath.Dir(filepath.Dir(bin)), "lib")
	}
	ffmpeg := opt("ffmpeg", "NOTO_FFMPEG")
	if ffmpeg == "" {
		if p, err := exec.LookPath("ffmpeg"); err == nil {
			ffmpeg = p
		}
	}

	md := cfg.ModelDir
	if md == "" {
		return nil, fmt.Errorf("sherpa: ModelDir is required (the parakeet encoder/decoder/joiner/tokens dir)")
	}
	enc := pickModelFile(md, "encoder")
	dec := pickModelFile(md, "decoder")
	join := pickModelFile(md, "joiner")
	tok := filepath.Join(md, "tokens.txt")
	if enc == "" || dec == "" || join == "" {
		return nil, fmt.Errorf("sherpa: incomplete transducer model in %s (need encoder/decoder/joiner .onnx)", md)
	}
	if _, err := os.Stat(tok); err != nil {
		return nil, fmt.Errorf("sherpa: tokens.txt not found in %s", md)
	}
	threads := 0
	if t := opt("threads", "NOTO_SHERPA_THREADS"); t != "" {
		if n, err := strconv.Atoi(t); err == nil {
			threads = n
		}
	}
	if threads <= 0 {
		threads = 4
	}
	mt := opt("model_type", "NOTO_SHERPA_MODEL_TYPE")
	if mt == "" {
		mt = "nemo_transducer"
	}
	provider := opt("provider", "NOTO_SHERPA_PROVIDER")
	if provider == "" {
		provider = "cpu"
	}
	return &sherpaEngine{
		bin: bin, libDir: libDir, ffmpeg: ffmpeg,
		encoder: enc, decoder: dec, joiner: join, tokens: tok,
		modelType: mt, provider: provider, threads: threads,
	}, nil
}

func (e *sherpaEngine) Name() string { return "sherpa-parakeet" }

// maxChunkSeconds caps a single decode pass. The transducer encoder's memory
// grows with utterance length and blows up on very long audio (a 16-min single
// pass aborts with an Ort exception), so long audio is split into windows,
// transcribed separately, and stitched back with per-chunk time offsets. 120 s
// is safely under the limit (90 s is proven) and keeps per-chunk memory small.
const maxChunkSeconds = 120

func (e *sherpaEngine) Recognize(ctx context.Context, audio []byte, _ TranscribeOptions) ([]EngineWord, error) {
	files, offsets, cleanups, err := chunkToWAVs(ctx, audio, e.ffmpeg)
	defer runCleanups(cleanups)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, nil
	}

	results, err := e.transcribeFiles(ctx, files)
	if err != nil {
		return nil, err
	}
	// The CLI should emit exactly one result per file. If the counts disagree
	// (an unexpected output shape), fall back to one process per chunk so word
	// offsets can never be misattributed — correctness over the warm-path speedup.
	if len(results) != len(files) {
		results = results[:0]
		for _, f := range files {
			r, ferr := e.transcribeFiles(ctx, []string{f})
			if ferr != nil {
				return nil, ferr
			}
			if len(r) == 0 {
				results = append(results, sherpaResult{})
			} else {
				results = append(results, r[0])
			}
		}
	}
	return stitchWords(results, offsets), nil
}

// RecognizeBatch recognizes several independent audios in ONE warm CLI pass:
// every audio's chunks join a single file list, so the whole batch pays the
// model-load + (on GPU) CUDA-context cost once instead of once per audio. This
// is the cross-meeting analogue of the within-meeting warm batching above —
// the benchmark's biggest per-run fixed-cost saving. Results map back to their
// source audio by position, with per-chunk time offsets preserved.
func (e *sherpaEngine) RecognizeBatch(ctx context.Context, audios [][]byte, opts TranscribeOptions) ([][]EngineWord, error) {
	var (
		allFiles []string
		cleanups []func()
	)
	type span struct {
		start, end int // index range in allFiles
		offsets    []float64
	}
	spans := make([]span, len(audios))
	defer func() { runCleanups(cleanups) }() // closure: the slice grows below
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

	results, err := e.transcribeFiles(ctx, allFiles)
	// Same correctness guard as Recognize: any error or count mismatch falls
	// back to per-audio recognition (which has its own per-chunk fallback), so
	// a surprising output shape can never misattribute words across meetings.
	if err != nil || len(results) != len(allFiles) {
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

	out := make([][]EngineWord, len(audios))
	for i, sp := range spans {
		out[i] = stitchWords(results[sp.start:sp.end], sp.offsets)
	}
	return out, nil
}

// chunkToWAVs normalizes audio to PCM (decoding via ffmpeg if needed) and spills
// it into ≤maxChunkSeconds temp WAVs with their start offsets. The returned
// cleanups must run even on error (callers defer runCleanups immediately).
// Package-level so both the CLI engine and the warm-server engine chunk audio
// identically (same windows, same offsets → same stitched word stream).
func chunkToWAVs(ctx context.Context, audio []byte, ffmpeg string) (files []string, offsets []float64, cleanups []func(), err error) {
	rate, ch, bits, pcm, err := decodePCM(ctx, audio, ffmpeg)
	if err != nil {
		return nil, nil, nil, err
	}
	frame := ch * bits / 8
	if frame <= 0 || rate <= 0 {
		return nil, nil, nil, fmt.Errorf("sherpa: bad audio format (rate=%d ch=%d bits=%d)", rate, ch, bits)
	}
	bytesPerChunk := maxChunkSeconds * rate * frame

	// Write every chunk to a temp WAV up front, then decode them ALL in ONE
	// CLI invocation. The offline binary loads the transducer once and reuses it
	// across the file list, so a multi-chunk meeting pays the model-load +
	// (on GPU) CUDA-context cost a single time instead of once per chunk — the
	// dominant idle cost in the old chunk-per-process loop. Order is preserved:
	// the CLI prints one JSON result per input file, in argument order.
	for off := 0; off < len(pcm); off += bytesPerChunk {
		end := off + bytesPerChunk
		if end > len(pcm) {
			end = len(pcm)
		}
		path, cleanup, werr := writeTempWAV(buildWAV(rate, ch, bits, pcm[off:end]))
		if werr != nil {
			return nil, nil, cleanups, werr
		}
		cleanups = append(cleanups, cleanup)
		files = append(files, path)
		offsets = append(offsets, float64(off/frame)/float64(rate))
	}
	return files, offsets, cleanups, nil
}

// stitchWords flattens per-chunk results back into one time-ordered word stream,
// shifting each chunk's words by its start offset.
func stitchWords(results []sherpaResult, offsets []float64) []EngineWord {
	var words []EngineWord
	for i, res := range results {
		for _, w := range res.words() {
			w.StartSeconds += offsets[i]
			w.EndSeconds += offsets[i]
			words = append(words, w)
		}
	}
	return words
}

func runCleanups(cleanups []func()) {
	for _, c := range cleanups {
		c()
	}
}

// writeTempWAV spills WAV bytes to a temp file, returning its path and a cleanup.
func writeTempWAV(wav []byte) (string, func(), error) {
	noop := func() {}
	f, err := os.CreateTemp("", "noto-sherpa-*.wav")
	if err != nil {
		return "", noop, err
	}
	if _, err := f.Write(wav); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", noop, err
	}
	f.Close()
	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// transcribeFiles runs the offline CLI once over the given WAV paths (model
// loaded a single time) and returns one result per file, in input order.
func (e *sherpaEngine) transcribeFiles(ctx context.Context, files []string) ([]sherpaResult, error) {
	args := []string{
		"--encoder=" + e.encoder, "--decoder=" + e.decoder, "--joiner=" + e.joiner,
		"--tokens=" + e.tokens, "--model-type=" + e.modelType,
		"--provider=" + e.provider,
		"--num-threads=" + strconv.Itoa(e.threads),
	}
	args = append(args, files...)
	cmd := exec.CommandContext(ctx, e.bin, args...)
	if e.libDir != "" {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+e.libDir+":"+os.Getenv("LD_LIBRARY_PATH"))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("sherpa: run: %w (stderr tail: %s)", err, tail(errb.String(), 400))
	}
	res := parseSherpaResults(out.String())
	if len(res) == 0 {
		// Older/edge output shapes interleave the JSON into stderr; recover the
		// single-result case the way the engine always has.
		if r, err := parseSherpaJSON(out.String() + "\n" + errb.String()); err == nil {
			res = []sherpaResult{r}
		}
	}
	return res, nil
}

func withDefaultOption(in map[string]string, key, value string) map[string]string {
	out := make(map[string]string, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	if out[key] == "" {
		out[key] = value
	}
	return out
}

// decodePCM returns 16-bit PCM samples + format. RIFF/WAVE input is parsed
// directly; anything else is decoded to 16 kHz mono s16 WAV via ffmpeg first.
func decodePCM(ctx context.Context, audio []byte, ffmpeg string) (rate, ch, bits int, pcm []byte, err error) {
	if isWAVBytes(audio) {
		return parseWAV(audio)
	}
	if ffmpeg == "" {
		return 0, 0, 0, nil, fmt.Errorf("sherpa: input is not WAV and no ffmpeg available to decode (set NOTO_FFMPEG or run: noto models download)")
	}
	in, err := os.CreateTemp("", "noto-sherpa-in-*")
	if err != nil {
		return 0, 0, 0, nil, err
	}
	defer os.Remove(in.Name())
	if _, err := in.Write(audio); err != nil {
		in.Close()
		return 0, 0, 0, nil, err
	}
	in.Close()
	outPath := in.Name() + ".wav"
	defer os.Remove(outPath)
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", in.Name(), "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "wav", "-y", outPath)
	if errOut, rErr := cmd.CombinedOutput(); rErr != nil {
		return 0, 0, 0, nil, fmt.Errorf("sherpa: ffmpeg decode: %w (%s)", rErr, tail(string(errOut), 300))
	}
	wav, err := os.ReadFile(outPath)
	if err != nil {
		return 0, 0, 0, nil, err
	}
	return parseWAV(wav)
}

func isWAVBytes(b []byte) bool {
	return len(b) >= 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WAVE"
}

// parseWAV reads a PCM WAV: it walks the RIFF chunks for "fmt " (format) and
// "data" (samples), tolerating extra chunks (LIST/fact) some encoders insert.
func parseWAV(b []byte) (rate, ch, bits int, pcm []byte, err error) {
	if !isWAVBytes(b) {
		return 0, 0, 0, nil, fmt.Errorf("sherpa: not a RIFF/WAVE file")
	}
	pos := 12
	for pos+8 <= len(b) {
		id := string(b[pos : pos+4])
		size := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		body := pos + 8
		if body+size > len(b) {
			size = len(b) - body
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				ch = int(binary.LittleEndian.Uint16(b[body+2 : body+4]))
				rate = int(binary.LittleEndian.Uint32(b[body+4 : body+8]))
				bits = int(binary.LittleEndian.Uint16(b[body+14 : body+16]))
			}
		case "data":
			pcm = b[body : body+size]
		}
		pos = body + size
		if size%2 == 1 {
			pos++ // chunks are word-aligned
		}
	}
	if rate == 0 || ch == 0 || bits == 0 || pcm == nil {
		return 0, 0, 0, nil, fmt.Errorf("sherpa: incomplete WAV (rate=%d ch=%d bits=%d data=%d)", rate, ch, bits, len(pcm))
	}
	return rate, ch, bits, pcm, nil
}

// buildWAV wraps PCM samples in a canonical 44-byte WAV header.
func buildWAV(rate, ch, bits int, pcm []byte) []byte {
	byteRate := rate * ch * bits / 8
	blockAlign := ch * bits / 8
	buf := make([]byte, 44+len(pcm))
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+len(pcm)))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], uint16(ch))
	binary.LittleEndian.PutUint32(buf[24:28], uint32(rate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(byteRate))
	binary.LittleEndian.PutUint16(buf[32:34], uint16(blockAlign))
	binary.LittleEndian.PutUint16(buf[34:36], uint16(bits))
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(len(pcm)))
	copy(buf[44:], pcm)
	return buf
}

// sherpaResult is the JSON the offline CLI prints for a recognition.
type sherpaResult struct {
	Text       string    `json:"text"`
	Timestamps []float64 `json:"timestamps"`
	Durations  []float64 `json:"durations"`
	Tokens     []string  `json:"tokens"`
	// Confidences is one P(correct) per token, parallel to Tokens, when the
	// engine emits word/token confidence (NeMo on the parakeet-server path,
	// §B2.6). Absent for the sherpa CLI and the fake backend — words() then
	// leaves EngineWord.Confidence at 0 (→ nil downstream), so calibration is
	// simply skipped for that run rather than scoring fabricated values.
	Confidences []float64 `json:"confidences,omitempty"`
}

// words reconstructs word-level output from subword tokens: a token beginning
// with a space starts a new word; the rest append. Word timing spans its tokens.
// Falls back to splitting Text on whitespace (even spacing) if tokens are absent.
func (r sherpaResult) words() []EngineWord {
	if len(r.Tokens) == 0 || len(r.Timestamps) != len(r.Tokens) {
		return splitTextEven(r.Text)
	}
	var words []EngineWord
	for i, tok := range r.Tokens {
		start := r.Timestamps[i]
		end := start
		if i < len(r.Durations) {
			end = start + r.Durations[i]
		}
		hasConf := i < len(r.Confidences)
		var tokConf float64
		if hasConf {
			tokConf = r.Confidences[i]
		}
		cleaned := strings.TrimPrefix(tok, " ")
		newWord := strings.HasPrefix(tok, " ") || len(words) == 0
		if newWord {
			if strings.TrimSpace(cleaned) == "" {
				continue // leading-space-only token; skip
			}
			words = append(words, EngineWord{Text: cleaned, StartSeconds: start, EndSeconds: end, Confidence: tokConf})
			continue
		}
		w := &words[len(words)-1]
		w.Text += cleaned
		if end > w.EndSeconds {
			w.EndSeconds = end
		}
		// A word's confidence is the least-confident of its tokens — a word is
		// only as trustworthy as its weakest subword.
		if hasConf && tokConf < w.Confidence {
			w.Confidence = tokConf
		}
	}
	return words
}

func splitTextEven(text string) []EngineWord {
	fields := strings.Fields(text)
	out := make([]EngineWord, len(fields))
	for i, f := range fields {
		out[i] = EngineWord{Text: f, StartSeconds: float64(i), EndSeconds: float64(i) + 1}
	}
	return out
}

// parseSherpaResults collects every recognition JSON object from the CLI's
// stdout, in order — one per input file when the binary is given a file list.
func parseSherpaResults(s string) []sherpaResult {
	var out []sherpaResult
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") || !strings.Contains(ln, "\"text\"") {
			continue
		}
		var r sherpaResult
		if json.Unmarshal([]byte(ln), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// parseSherpaJSON finds the last JSON object line in the CLI output.
func parseSherpaJSON(s string) (sherpaResult, error) {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		ln := strings.TrimSpace(lines[i])
		if strings.HasPrefix(ln, "{") && strings.Contains(ln, "\"text\"") {
			var r sherpaResult
			if err := json.Unmarshal([]byte(ln), &r); err == nil {
				return r, nil
			}
		}
	}
	return sherpaResult{}, fmt.Errorf("no recognition JSON in output")
}

// pickModelFile finds a model file in dir by role, preferring an int8 variant.
func pickModelFile(dir, role string) string {
	cands := []string{role + ".int8.onnx", role + ".onnx", role + ".fp16.onnx"}
	for _, c := range cands {
		p := filepath.Join(dir, c)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	// Fallback: any file matching role*.onnx.
	matches, _ := filepath.Glob(filepath.Join(dir, role+"*.onnx"))
	if len(matches) > 0 {
		return matches[0]
	}
	return ""
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
