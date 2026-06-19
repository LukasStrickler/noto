//go:build !sherpa

package parakeet

import (
	"github.com/lukasstrickler/noto/internal/platform/compute"
	"github.com/lukasstrickler/noto/internal/platform/models"
)

// newEngine returns nil in the default build: the sherpa-onnx transducer runtime
// is a cgo dependency, so it's gated behind the `sherpa` build tag. Without it,
// Parakeet.Transcribe reports the engine is unavailable and the pipeline falls
// back gracefully (with an actionable note) rather than producing a wrong
// transcript. Build with `-tags sherpa` to compile in engine_sherpa.go.
func newEngine(_ *models.Manager, _ compute.Plan) engine { return nil }
