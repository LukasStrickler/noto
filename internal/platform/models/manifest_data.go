package models

// DefaultManifest is the pinned catalog of models, runtimes, and tools noto can
// fetch on demand. It extends the const block that used to live in
// speaker/install.go (the onnxruntime lib, ffmpeg, and the ECAPA model) into a
// general, backend-aware, license-tracked manifest.
//
// Integrity policy: every asset SHOULD carry a SHA256. Entries with an empty
// SHA256 download with a loud "integrity unverified" warning (see fetchAsset)
// and MUST be pinned before the corresponding provider is shipped as a default.
// The sherpa-onnx STT/diarization weights below are PLACEHOLDERS — their exact
// URLs and checksums must be confirmed (and any CC-BY-NC checkpoint kept out)
// before Phase 1 enables the local provider by default.
func DefaultManifest() Manifest {
	const ortVersion = "1.26.0"
	return Manifest{
		SchemaVersion: "models.v1",
		Runtime: map[Backend][]Asset{
			// Linux x64 onnxruntime (CPU + CUDA EPs live in the same build).
			// Extracted from the upstream .tgz; we keep libonnxruntime.so.
			BackendCPU: {{
				Filename: "libonnxruntime.so",
				URL:      "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVersion + "/onnxruntime-linux-x64-" + ortVersion + ".tgz",
				Archive:  "tgz",
				Extract:  "/lib/libonnxruntime.so",
			}},
			BackendCUDA: {{
				Filename: "libonnxruntime.so",
				URL:      "https://github.com/microsoft/onnxruntime/releases/download/v" + ortVersion + "/onnxruntime-linux-x64-gpu-" + ortVersion + ".tgz",
				Archive:  "tgz",
				Extract:  "/lib/libonnxruntime.so",
			}},
		},
		Tools: []Asset{{
			Filename: "ffmpeg",
			URL:      "https://johnvansickle.com/ffmpeg/releases/ffmpeg-release-amd64-static.tar.xz",
			Archive:  "tar.xz",
			Extract:  "ffmpeg",
			Optional: true,
		}},
		Models: []Model{
			{
				ID:      "ecapa512",
				Role:    RoleEmbed,
				License: "Apache-2.0", // WeSpeaker VoxCeleb ECAPA-TDNN512
				Variants: []Variant{{
					Backend: BackendAny,
					Primary: "ecapa512.onnx",
					Assets: []Asset{{
						Filename: "ecapa512.onnx",
						URL:      "https://huggingface.co/Wespeaker/wespeaker-voxceleb-ecapa-tdnn512/resolve/main/voxceleb_ECAPA512.onnx",
						// SHA256 to be pinned; this is the same artifact speaker/install.go fetches today.
					}},
				}},
			},
			{
				// PLACEHOLDER — confirm exact sherpa-onnx NeMo Parakeet TDT export,
				// file names, URLs, checksums, and the CC-BY-4.0 license before use.
				ID:      "parakeet-tdt-0.6b-v3",
				Role:    RoleSTT,
				License: "CC-BY-4.0",
				Variants: []Variant{
					{
						Backend: BackendCPU,
						Tier:    TierFast, // INT8 export
						Primary: "encoder.onnx",
						Assets: []Asset{
							{Filename: "encoder.onnx", URL: "https://example.invalid/parakeet/int8/encoder.onnx"},
							{Filename: "decoder.onnx", URL: "https://example.invalid/parakeet/int8/decoder.onnx"},
							{Filename: "joiner.onnx", URL: "https://example.invalid/parakeet/int8/joiner.onnx"},
							{Filename: "tokens.txt", URL: "https://example.invalid/parakeet/int8/tokens.txt"},
						},
					},
					{
						Backend: BackendCPU,
						Tier:    TierAccurate, // FP16/FP32 export
						Primary: "encoder.onnx",
						Assets: []Asset{
							{Filename: "encoder.onnx", URL: "https://example.invalid/parakeet/fp/encoder.onnx"},
							{Filename: "decoder.onnx", URL: "https://example.invalid/parakeet/fp/decoder.onnx"},
							{Filename: "joiner.onnx", URL: "https://example.invalid/parakeet/fp/joiner.onnx"},
							{Filename: "tokens.txt", URL: "https://example.invalid/parakeet/fp/tokens.txt"},
						},
					},
					// A {Backend: BackendCoreML} variant (FluidAudio .mlmodelc
					// package) is added in Phase 8 — same id, different variant.
				},
			},
			{
				// PLACEHOLDER — sherpa-onnx pyannote-style segmentation +
				// embedding for diarization; confirm export + license before use.
				ID:      "diar-pyannote-community-1",
				Role:    RoleDiar,
				License: "MIT",
				Variants: []Variant{{
					Backend: BackendCPU,
					Tier:    TierAccurate,
					Primary: "segmentation.onnx",
					Assets: []Asset{
						{Filename: "segmentation.onnx", URL: "https://example.invalid/diar/segmentation.onnx"},
						{Filename: "embedding.onnx", URL: "https://example.invalid/diar/embedding.onnx"},
					},
				}},
			},
		},
	}
}
