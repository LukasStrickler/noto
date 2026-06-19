package parakeet

import (
	"fmt"

	"github.com/lukasstrickler/noto/internal/platform/models"
)

// decodePCM decodes arbitrary audio bytes to mono 16 kHz float32 PCM (what the
// transducer expects), using the manager's ffmpeg. The default build never
// reaches this (Transcribe returns earlier when the engine is nil); it is
// implemented alongside the sherpa engine, reusing the ffmpeg decode the
// speaker package already performs.
func decodePCM(_ *models.Manager, _ []byte) ([]float32, int, error) {
	return nil, 0, fmt.Errorf("parakeet-local: PCM decode is implemented with the sherpa engine (-tags sherpa)")
}
