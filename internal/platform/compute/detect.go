// Package compute resolves which hardware accelerator the local model
// runtime should use. It encodes the plan's "two independent knobs":
//
//   - Accelerator — free, automatic. Same model, same accuracy, fastest
//     engine present (CoreML on Apple Silicon, CUDA on an NVIDIA box, else
//     CPU). Detection picks the candidate ladder for the OS, verifies it
//     (try it, fall back on failure — never crash), and yields an ORT
//     execution-provider priority list the providers consume verbatim.
//     Provider code never branches on GOOS; it just reads Plan.EPList.
//
//   - Model / precision tier — an accuracy decision, NOT an accelerator one.
//     Plan.Tier is recorded here but does not change the EP list; it selects
//     which model *variant* the model manager fetches (FP16 vs INT8).
//
// The detection runs once at backend startup (host.Start) on whichever
// machine runs the backend — the Mac in local mode, the Linux server in
// remote mode — and is cached on the Service, then surfaced via /v1/system.
package compute

import (
	"os"
	"runtime"
	"strconv"
	"strings"
)

// Backend is the chosen accelerator family.
type Backend string

const (
	BackendAuto   Backend = "auto" // sentinel for "decide for me" (override only)
	BackendCUDA   Backend = "cuda"
	BackendCoreML Backend = "coreml"
	BackendCPU    Backend = "cpu"
)

// Tier is the model/precision tier (an accuracy knob, not an accelerator one).
type Tier string

const (
	TierAccurate Tier = "accurate"
	TierFast     Tier = "fast"
)

// ORT execution-provider names, as onnxruntime expects them.
const (
	epCUDA   = "CUDAExecutionProvider"
	epCoreML = "CoreMLExecutionProvider"
	epCPU    = "CPUExecutionProvider"
)

// Logf is an optional structured-ish progress/diagnostic sink.
type Logf func(format string, args ...any)

// Plan is the resolved accelerator decision. It is produced once and injected
// into the local STT / diarizer / embedder constructors, which translate
// EPList into onnxruntime session options.
type Plan struct {
	Backend  Backend  // the chosen (and, if a Verify hook was supplied, verified) winner
	EPList   []string // ORT execution-provider priority, highest first; always ends with CPU
	Tier     Tier
	Threads  int    // intra-op thread count for the CPU EP (nproc)
	Verified bool   // true only when a Verify hook actually confirmed the backend
	OS       string // GOOS the plan was resolved for
	Arch     string // GOARCH the plan was resolved for
}

// Options control detection. The zero value resolves for the current OS/arch
// with auto accelerator selection, the accurate tier, and no live verification.
type Options struct {
	// Override pins the starting accelerator (from NOTO_COMPUTE). Empty or
	// BackendAuto means "use the OS ladder". A pinned backend is still
	// verified and still falls back to CPU on failure — it never crashes.
	Override Backend
	// Tier is the model/precision tier (from NOTO_MODEL_TIER). Defaults to
	// TierAccurate.
	Tier Tier
	// OS / Arch default to runtime.GOOS / runtime.GOARCH. Set them in tests.
	OS, Arch string
	// Verify, if non-nil, is called with each candidate backend in priority
	// order; the first that returns nil wins (and Plan.Verified is set). A
	// candidate that returns an error is logged and skipped. If every
	// candidate fails (or Verify is nil), detection falls through to CPU.
	Verify func(Backend) error
	// Threads overrides the CPU intra-op thread count; <=0 means runtime.NumCPU().
	Threads int
	// Log is an optional diagnostic sink.
	Log Logf
}

// Detect resolves a Plan from explicit options. It is pure given its inputs
// (and the injected Verify hook), so it is fully unit-testable.
func Detect(opts Options) Plan {
	log := opts.Log
	if log == nil {
		log = func(string, ...any) {}
	}
	goos := opts.OS
	if goos == "" {
		goos = runtime.GOOS
	}
	arch := opts.Arch
	if arch == "" {
		arch = runtime.GOARCH
	}
	tier := opts.Tier
	if tier != TierFast {
		tier = TierAccurate
	}
	threads := opts.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	candidates := candidateLadder(goos, opts.Override)
	if opts.Override != "" && opts.Override != BackendAuto {
		log("compute: NOTO_COMPUTE=%s pinned; candidates=%v", opts.Override, candidates)
	}

	chosen := BackendCPU
	verified := false
	for _, cand := range candidates {
		if opts.Verify == nil {
			chosen = cand
			break
		}
		if err := opts.Verify(cand); err != nil {
			log("compute: backend %s unavailable (%v); falling back", cand, err)
			continue
		}
		chosen = cand
		verified = true
		break
	}

	plan := Plan{
		Backend:  chosen,
		EPList:   epList(chosen),
		Tier:     tier,
		Threads:  threads,
		Verified: verified,
		OS:       goos,
		Arch:     arch,
	}
	log("compute: backend=%s tier=%s eps=%v verified=%v (%s/%s)",
		plan.Backend, plan.Tier, plan.EPList, plan.Verified, plan.OS, plan.Arch)
	return plan
}

// DetectFromEnv reads NOTO_COMPUTE / NOTO_MODEL_TIER / NOTO_COMPUTE_THREADS and
// runs Detect. verify may be nil (trust the ladder; full verification then
// happens at provider Open).
func DetectFromEnv(verify func(Backend) error, log Logf) Plan {
	return Detect(Options{
		Override: ParseBackend(os.Getenv("NOTO_COMPUTE")),
		Tier:     ParseTier(os.Getenv("NOTO_MODEL_TIER")),
		Threads:  envInt("NOTO_COMPUTE_THREADS"),
		Verify:   verify,
		Log:      log,
	})
}

// candidateLadder returns the accelerator priority for an OS, honoring a pin.
// CPU is always the final fallback so detection can never come up empty.
func candidateLadder(goos string, override Backend) []Backend {
	var base []Backend
	switch goos {
	case "darwin":
		base = []Backend{BackendCoreML, BackendCPU}
	case "linux", "windows":
		base = []Backend{BackendCUDA, BackendCPU}
	default:
		base = []Backend{BackendCPU}
	}
	if override != "" && override != BackendAuto {
		// A pin starts the ladder at the pinned backend but still allows the
		// CPU fallback beneath it, so a bad pin degrades instead of crashing.
		return dedupBackends(append([]Backend{override}, BackendCPU))
	}
	return dedupBackends(base)
}

func dedupBackends(in []Backend) []Backend {
	seen := map[Backend]bool{}
	out := make([]Backend, 0, len(in))
	for _, b := range in {
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		out = append(out, b)
	}
	return out
}

// epList maps a backend to its ORT execution-provider priority list. Every
// list ends with the CPU EP so ORT always has a working fallback at run time.
func epList(b Backend) []string {
	switch b {
	case BackendCUDA:
		return []string{epCUDA, epCPU}
	case BackendCoreML:
		return []string{epCoreML, epCPU}
	default:
		return []string{epCPU}
	}
}

// ParseBackend normalizes a NOTO_COMPUTE value. Unknown / empty → BackendAuto.
func ParseBackend(s string) Backend {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cuda", "gpu", "nvidia":
		return BackendCUDA
	case "coreml", "ane", "metal", "mps":
		return BackendCoreML
	case "cpu":
		return BackendCPU
	case "auto", "":
		return BackendAuto
	default:
		return BackendAuto
	}
}

// ParseTier normalizes a NOTO_MODEL_TIER value. Unknown / empty → TierAccurate.
func ParseTier(s string) Tier {
	if strings.EqualFold(strings.TrimSpace(s), string(TierFast)) {
		return TierFast
	}
	return TierAccurate
}

func envInt(key string) int {
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return 0
}
