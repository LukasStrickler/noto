package diarize

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// sherpaSegmentEngine is a local diarization SegmentEngine that shells out to
// the prebuilt `sherpa-onnx-offline-speaker-diarization` binary (no cgo): a
// pyannote segmentation model + a speaker-embedding model + fast clustering. It
// emits speaker turns ("who spoke when"), entirely on-device. The pyannote
// segmenter windows internally, so it handles long meetings without the
// single-pass blowup the STT transducer has.
//
// Registered unconditionally; the factory fails with an actionable error when
// the binary/models aren't installed, so the pipeline degrades gracefully.
type sherpaSegmentEngine struct {
	bin         string
	libDir      string
	ffmpeg      string
	segModel    string
	embModel    string
	segProvider string
	embProvider string
	threshold   float64
	threads     int
}

func init() {
	RegisterSegmentEngine("sherpa-pyannote", newSherpaSegmentEngine)
	RegisterSegmentEngine("cuda-pyannote", newCUDASherpaSegmentEngine)
}

func newCUDASherpaSegmentEngine(cfg EngineConfig) (SegmentEngine, error) {
	cfg.Options = withDefaultOption(cfg.Options, "seg_provider", "cuda")
	cfg.Options = withDefaultOption(cfg.Options, "emb_provider", "cuda")
	return newSherpaSegmentEngine(cfg)
}

func newSherpaSegmentEngine(cfg EngineConfig) (SegmentEngine, error) {
	opt := func(k, env string) string {
		if cfg.Options != nil && cfg.Options[k] != "" {
			return cfg.Options[k]
		}
		return os.Getenv(env)
	}
	bin := opt("bin", "NOTO_SHERPA_DIAR_BIN")
	if bin == "" {
		// Derive from the STT binary's dir (same sherpa bin/ holds both).
		if stt := os.Getenv("NOTO_SHERPA_BIN"); stt != "" {
			bin = filepath.Join(filepath.Dir(stt), "sherpa-onnx-offline-speaker-diarization")
		}
	}
	if bin == "" {
		if p, err := exec.LookPath("sherpa-onnx-offline-speaker-diarization"); err == nil {
			bin = p
		}
	}
	if bin == "" || !fileExists(bin) {
		return nil, fmt.Errorf("diarize/sherpa: diarization binary not found (set NOTO_SHERPA_DIAR_BIN or run: noto models download)")
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
	seg := opt("seg", "NOTO_SHERPA_SEG_MODEL")
	emb := opt("emb", "NOTO_SHERPA_EMB_MODEL")
	if cfg.ModelDir != "" {
		if seg == "" {
			seg = firstExisting(
				filepath.Join(cfg.ModelDir, "sherpa-onnx-pyannote-segmentation-3-0", "model.onnx"),
				filepath.Join(cfg.ModelDir, "segmentation.onnx"))
		}
		if emb == "" {
			emb = firstExisting(filepath.Join(cfg.ModelDir, "embedding.onnx"))
		}
	}
	if seg == "" || !fileExists(seg) {
		return nil, fmt.Errorf("diarize/sherpa: segmentation model not found (set NOTO_SHERPA_SEG_MODEL)")
	}
	if emb == "" || !fileExists(emb) {
		return nil, fmt.Errorf("diarize/sherpa: embedding model not found (set NOTO_SHERPA_EMB_MODEL)")
	}
	segProvider := opt("seg_provider", "NOTO_SHERPA_SEG_PROVIDER")
	if segProvider == "" {
		segProvider = opt("provider", "NOTO_SHERPA_PROVIDER")
	}
	if segProvider == "" {
		segProvider = "cpu"
	}
	embProvider := opt("emb_provider", "NOTO_SHERPA_EMB_PROVIDER")
	if embProvider == "" {
		embProvider = opt("provider", "NOTO_SHERPA_PROVIDER")
	}
	if embProvider == "" {
		embProvider = "cpu"
	}
	threshold := 0.5
	if t := opt("threshold", "NOTO_SHERPA_DIAR_THRESHOLD"); t != "" {
		if v, err := strconv.ParseFloat(t, 64); err == nil {
			threshold = v
		}
	}
	threads := 4
	if t := opt("threads", "NOTO_SHERPA_THREADS"); t != "" {
		if n, err := strconv.Atoi(t); err == nil && n > 0 {
			threads = n
		}
	}
	return &sherpaSegmentEngine{
		bin: bin, libDir: libDir, ffmpeg: ffmpeg,
		segModel: seg, embModel: emb,
		segProvider: segProvider, embProvider: embProvider,
		threshold: threshold, threads: threads,
	}, nil
}

func (e *sherpaSegmentEngine) Name() string { return "sherpa-pyannote" }

var diarLineRe = regexp.MustCompile(`^\s*([0-9.]+)\s*--\s*([0-9.]+)\s+(\S+)`)

func (e *sherpaSegmentEngine) Segment(ctx context.Context, audio []byte, opts DiarizeOptions) ([]EngineTurn, error) {
	wavPath, cleanup, err := e.ensureWAV(ctx, audio)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	args := []string{
		"--segmentation.pyannote-model=" + e.segModel,
		"--embedding.model=" + e.embModel,
		"--segmentation.provider=" + e.segProvider,
		"--embedding.provider=" + e.embProvider,
		"--segmentation.num-threads=" + strconv.Itoa(e.threads),
		"--embedding.num-threads=" + strconv.Itoa(e.threads),
	}
	// Known speaker count → exact clusters (better DER); else distance threshold.
	if opts.NumSpeakers > 0 {
		args = append(args, "--clustering.num-clusters="+strconv.Itoa(opts.NumSpeakers))
	} else {
		args = append(args, fmt.Sprintf("--clustering.cluster-threshold=%g", e.threshold))
	}
	args = append(args, wavPath)

	cmd := exec.CommandContext(ctx, e.bin, args...)
	if e.libDir != "" {
		cmd.Env = append(os.Environ(), "LD_LIBRARY_PATH="+e.libDir+":"+os.Getenv("LD_LIBRARY_PATH"))
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("diarize/sherpa: run: %w (stderr tail: %s)", err, tailStr(errb.String(), 400))
	}
	return parseDiarLines(out.String() + "\n" + errb.String()), nil
}

// parseDiarLines extracts "START -- END speaker_NN" turn lines from the CLI
// output (which also prints config/progress noise).
func parseDiarLines(s string) []EngineTurn {
	var turns []EngineTurn
	sc := bufio.NewScanner(strings.NewReader(s))
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		m := diarLineRe.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		start, e1 := strconv.ParseFloat(m[1], 64)
		end, e2 := strconv.ParseFloat(m[2], 64)
		if e1 != nil || e2 != nil || end <= start {
			continue
		}
		turns = append(turns, EngineTurn{Speaker: m[3], StartSeconds: start, EndSeconds: end})
	}
	return turns
}

// ensureWAV writes WAV input straight to a temp file; non-WAV is decoded to
// 16 kHz mono WAV via ffmpeg.
func (e *sherpaSegmentEngine) ensureWAV(ctx context.Context, audio []byte) (string, func(), error) {
	return ensureWAVFile(ctx, audio, e.ffmpeg)
}

// ensureWAVFile stages audio bytes as a WAV file on disk for engines that take
// a path: WAV input is spilled as-is; anything else is decoded to 16 kHz mono
// WAV via ffmpeg ("" = WAV only). Shared by the sherpa and pyannote engines.
func ensureWAVFile(ctx context.Context, audio []byte, ffmpeg string) (string, func(), error) {
	noop := func() {}
	if len(audio) >= 12 && string(audio[0:4]) == "RIFF" && string(audio[8:12]) == "WAVE" {
		f, err := os.CreateTemp("", "noto-diar-*.wav")
		if err != nil {
			return "", noop, err
		}
		if _, err := f.Write(audio); err != nil {
			f.Close()
			os.Remove(f.Name())
			return "", noop, err
		}
		f.Close()
		return f.Name(), func() { os.Remove(f.Name()) }, nil
	}
	if ffmpeg == "" {
		return "", noop, fmt.Errorf("diarize: input is not WAV and no ffmpeg available (set NOTO_FFMPEG)")
	}
	in, err := os.CreateTemp("", "noto-diar-in-*")
	if err != nil {
		return "", noop, err
	}
	if _, err := in.Write(audio); err != nil {
		in.Close()
		os.Remove(in.Name())
		return "", noop, err
	}
	in.Close()
	out := in.Name() + ".wav"
	cleanup := func() { os.Remove(in.Name()); os.Remove(out) }
	cmd := exec.CommandContext(ctx, ffmpeg, "-nostdin", "-hide_banner", "-loglevel", "error",
		"-i", in.Name(), "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", "-f", "wav", "-y", out)
	if errOut, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", noop, fmt.Errorf("diarize: ffmpeg decode: %w (%s)", err, tailStr(string(errOut), 300))
	}
	return out, cleanup, nil
}

func fileExists(p string) bool { fi, err := os.Stat(p); return err == nil && !fi.IsDir() }

func firstExisting(paths ...string) string {
	for _, p := range paths {
		if fileExists(p) {
			return p
		}
	}
	return ""
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
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
