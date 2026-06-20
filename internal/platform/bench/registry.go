package bench

import (
	"strconv"
	"strings"

	"github.com/lukasstrickler/noto/internal/platform/config"
)

// TierCapUSD is max spend per tier (§8.4).
var TierCapUSD = map[string]float64{
	"preflight":         0,
	"smoke":             0.25,
	"gate":              1.5,
	"confirm":           5.0,
	"baseline_variance": 15.0,
	"integration-only":  5.0,
}

// EffectiveBudget returns min(tier cap, override) when override > 0.
func EffectiveBudget(tier string, override float64) float64 {
	cap, ok := TierCapUSD[tier]
	if !ok {
		cap = TierCapUSD["gate"]
	}
	if override > 0 && override < cap {
		return override
	}
	return cap
}

// SuiteSpec describes how a suite_id maps to modal_benchmark.py flags.
type SuiteSpec struct {
	ID          string  `json:"id"`
	ModalSuite  string  `json:"modal_suite"`
	Quick       bool    `json:"quick"`
	Hours       float64 `json:"hours,omitempty"`
	Description string  `json:"description,omitempty"`
}

// AMIAnchorAudioHours is the processed-audio-hour denominator of the full AMI
// anchor corpus (§3.1, 30 meetings). Used to project anchor cost when a suite
// runs whole-corpus (Hours == 0).
const AMIAnchorAudioHours = 11.44

// AMIQuickAudioHours is the ~2h subsample a --quick AMI run processes (the gate
// slice). A quick AMI suite with no explicit Hours is projected against this, not
// the full corpus, so the estimate matches what actually runs.
const AMIQuickAudioHours = 2

// KnownSuites is the registry agents query via dataset list (A8 subset).
//
// gate_ami caps to ~2h of audio (Hours: 2) so a gate iteration scores a
// reproducible 4–5 meeting subsample on the real GPU — cheap to repeat while
// optimizing — while anchor_ami stays whole-corpus (Hours: 0) for final MDE /
// confirm validation. The cap is also what `bench estimate` projects against.
var KnownSuites = []SuiteSpec{
	{ID: "smoke@2026-06-18", ModalSuite: "synthetic", Quick: true, Description: "protocol / crash smoke"},
	{ID: "gate_ami@2026-06-18", ModalSuite: "ami", Quick: true, Hours: 2, Description: "AMI gate slice (~4-5 meetings) for H1+ comparisons"},
	{ID: "anchor_ami@2026-06-18", ModalSuite: "ami", Quick: false, Description: "anchor corpus for MDE / confirm"},
}

// SuiteAudioHours returns the processed-audio-hour denominator a suite feeds the
// GPU, for cost estimation. A capped suite reports its cap; the uncapped AMI
// anchor reports the full corpus; synthetic/unknown suites report 0.
func SuiteAudioHours(suiteID string) float64 {
	s := ResolveSuite(suiteID)
	if s.Hours > 0 {
		return s.Hours
	}
	if s.ModalSuite == "ami" {
		// A quick AMI run with no explicit Hours (e.g. an unknown suite id) runs a
		// subsample, not the whole corpus — project against the subsample so the
		// estimate matches the run. Only the explicit non-quick anchor uses 11.44h.
		if s.Quick {
			return AMIQuickAudioHours
		}
		return AMIAnchorAudioHours
	}
	return 0
}

// ResolveSuite maps suite_id (with optional @date) to modal flags.
func ResolveSuite(suiteID string) SuiteSpec {
	base := strings.SplitN(suiteID, "@", 2)[0]
	for _, s := range KnownSuites {
		if strings.SplitN(s.ID, "@", 2)[0] == base {
			out := s
			if strings.Contains(suiteID, "@") {
				out.ID = suiteID
			}
			return out
		}
	}
	switch base {
	case "smoke":
		return SuiteSpec{ID: suiteID, ModalSuite: "synthetic", Quick: true}
	default:
		return SuiteSpec{ID: suiteID, ModalSuite: "ami", Quick: true, Description: "AMI benchmark"}
	}
}

// ModalProfile maps operating_mode to modal_benchmark profile name.
func ModalProfile(mode string) string {
	if mode == "single_meeting" {
		return "prod"
	}
	return "batch"
}

// TargetConcurrency resolves jobs for comparability manifest.
func TargetConcurrency(mode string, knobs map[string]string) int {
	if knobs != nil {
		if j, ok := knobs["jobs"]; ok {
			if n, err := strconv.Atoi(j); err == nil && n > 0 {
				return n
			}
		}
	}
	if mode == "single_meeting" {
		return 1
	}
	return 10
}

// DefaultGPU returns configured Modal GPU or L40S default.
func DefaultGPU(cfgGPU string) string {
	if strings.TrimSpace(cfgGPU) != "" {
		return cfgGPU
	}
	return config.DefaultModalGPU
}

// ListSuites returns registered suite specs (copy).
func ListSuites() []SuiteSpec {
	out := make([]SuiteSpec, len(KnownSuites))
	copy(out, KnownSuites)
	return out
}
