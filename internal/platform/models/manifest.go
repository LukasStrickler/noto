// Package models is noto's general model manager. It generalizes the proven
// speaker/install.go pattern (fetch-if-missing into the data dir, skip what's
// present) into a manifest-driven manager that is:
//
//   - backend-aware — fetches the variant matching the detected accelerator +
//     tier (an ONNX FP16/INT8 file for CPU/CUDA, a CoreML package for ANE),
//   - verified + pinned — every asset carries a SHA256 and a tracked license,
//     so fetches are integrity-checked and reproducible, and non-commercial
//     checkpoints can be kept out of the default manifest,
//   - cached + reused — resolved lazily into <dataDir>/models, shared across
//     runs, fully offline after the first fetch,
//   - progress-reporting — downloads stream byte progress so a JobDownloadModel
//     can surface "1.2/2.0 GB" in the TUI (see the service worker).
//
// The Backend discriminator on each Variant is what makes the
// "Go/sherpa-onnx runtime vs Swift/FluidAudio" fork DATA, not code: the same
// model id carries a cpu/cuda ONNX variant and (later) a coreml package
// variant, and the manager hands back whichever matches the host's plan.
package models

import (
	"path/filepath"

	"github.com/lukasstrickler/noto/internal/platform/compute"
)

// Backend reuses the compute accelerator family, plus "any" for OS-agnostic
// assets (the onnxruntime lib aside, e.g. a model that ships one file for all
// CPU/GPU execution providers).
type (
	Backend = compute.Backend
	Tier    = compute.Tier
)

// BackendAny marks an asset/variant that is not specific to an accelerator.
const BackendAny Backend = "any"

// Re-exported compute constants so manifest entries read without the compute.
// qualifier (the types are aliases, so these are the same values).
const (
	BackendCPU    = compute.BackendCPU
	BackendCUDA   = compute.BackendCUDA
	BackendCoreML = compute.BackendCoreML
	TierFast      = compute.TierFast
	TierAccurate  = compute.TierAccurate
)

// Role categorizes a manifest entry so `noto models status` can group output
// and the manager can resolve the runtime lib / ffmpeg generically.
type Role string

const (
	RoleSTT     Role = "stt"     // speech-to-text weights (e.g. Parakeet)
	RoleDiar    Role = "diar"    // diarization weights (e.g. pyannote/sherpa)
	RoleEmbed   Role = "embed"   // speaker-embedding weights (e.g. ECAPA)
	RoleRuntime Role = "runtime" // onnxruntime / sherpa shared libraries
	RoleTool    Role = "tool"    // helper binaries (ffmpeg)
)

// Asset is one downloadable file. Filename is its local name under the variant
// directory. Archive marks tarballs the manager must extract (Extract names the
// member to pull out, by suffix match). Optional assets log on failure instead
// of aborting (ffmpeg-style).
type Asset struct {
	Filename string // local name under the model/variant dir
	URL      string
	SHA256   string // hex; empty skips verification (with a loud log)
	Size     int64  // expected bytes; drives the idempotent "already present" skip
	Optional bool   // failure logs and continues rather than aborting
	Archive  string // "" | "tgz" | "tar.xz" — extract instead of saving raw
	Extract  string // when Archive != "": member name suffix to extract
}

// Variant is the set of assets for one (Backend, Tier) combination.
type Variant struct {
	Backend Backend
	Tier    Tier
	Assets  []Asset
	// Primary names the asset within Assets that is "the model file"
	// (ResolvePaths.File). Empty means the first non-optional asset.
	Primary string
}

// Model is one logical model with per-backend/tier variants.
type Model struct {
	ID       string
	Role     Role
	License  string // "CC-BY-4.0" | "MIT" | "Apache-2.0" — tracked per the licensing risk
	Variants []Variant
}

// Manifest is the pinned catalog. Runtime holds the onnxruntime/sherpa shared
// libraries per accelerator; Tools holds helper binaries (ffmpeg); Models holds
// the weights.
type Manifest struct {
	SchemaVersion string
	Runtime       map[Backend][]Asset
	Tools         []Asset
	Models        []Model
}

// Find returns the model with the given id.
func (m Manifest) Find(id string) (Model, bool) {
	for _, mdl := range m.Models {
		if mdl.ID == id {
			return mdl, true
		}
	}
	return Model{}, false
}

// selectVariant picks the best variant for (backend, tier): exact match first,
// then same-backend/any-tier, then any-backend/exact-tier, then any/any. This
// lets a backend-agnostic model declare a single {BackendAny, ""} variant.
func selectVariant(mdl Model, b Backend, t Tier) (Variant, bool) {
	type rank struct {
		v Variant
		r int
	}
	best := rank{r: -1}
	for _, v := range mdl.Variants {
		r := -1
		switch {
		case v.Backend == b && v.Tier == t:
			r = 4
		case v.Backend == b && (v.Tier == "" || t == ""):
			r = 3
		case (v.Backend == BackendAny || v.Backend == "") && v.Tier == t:
			r = 2
		case (v.Backend == BackendAny || v.Backend == "") && (v.Tier == "" || t == ""):
			r = 1
		}
		if r > best.r {
			best = rank{v: v, r: r}
		}
	}
	if best.r < 0 {
		return Variant{}, false
	}
	return best.v, true
}

func (v Variant) primaryAsset() (Asset, bool) {
	if v.Primary != "" {
		for _, a := range v.Assets {
			if a.Filename == v.Primary {
				return a, true
			}
		}
	}
	for _, a := range v.Assets {
		if !a.Optional {
			return a, true
		}
	}
	if len(v.Assets) > 0 {
		return v.Assets[0], true
	}
	return Asset{}, false
}

// variantDirName is the on-disk subdirectory for a variant, e.g. "cpu-fast".
func variantDirName(v Variant) string {
	b := string(v.Backend)
	if b == "" {
		b = string(BackendAny)
	}
	t := string(v.Tier)
	if t == "" {
		return b
	}
	return b + "-" + t
}

// ModelPaths is the resolved on-disk layout for a model variant.
type ModelPaths struct {
	ModelID string
	Dir     string            // <dataDir>/models/<id>/<variant>
	File    string            // the primary model file
	Files   map[string]string // every asset filename -> absolute path
	assets  []Asset           // retained for Verify
}

func (m *Manager) modelsRoot() string { return filepath.Join(m.dataDir, "models") }
