package speaker

import (
	"fmt"
	"math"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	ortOnce sync.Once
	ortErr  error
)

// initRuntime points onnxruntime_go at the downloaded shared library and initializes
// the global ORT environment exactly once per process.
func initRuntime(libPath string) error {
	ortOnce.Do(func() {
		if libPath != "" {
			ort.SetSharedLibraryPath(libPath)
		}
		ortErr = ort.InitializeEnvironment()
	})
	return ortErr
}

// ECAPA is the in-process ECAPA-TDNN-512 embedder (192-d). It owns one ORT session and
// is safe for sequential use; callers should serialize Run (one session, one meeting).
type ECAPA struct {
	sess *ort.DynamicAdvancedSession
	dim  int
}

// NewECAPA loads the ONNX model from modelPath using the ORT shared library at libPath.
func NewECAPA(modelPath, libPath string) (*ECAPA, error) {
	if err := initRuntime(libPath); err != nil {
		return nil, fmt.Errorf("speaker: init onnxruntime: %w", err)
	}
	sess, err := ort.NewDynamicAdvancedSession(modelPath,
		[]string{"feats"}, []string{"embs"}, nil)
	if err != nil {
		return nil, fmt.Errorf("speaker: load model %s: %w", modelPath, err)
	}
	return &ECAPA{sess: sess, dim: 192}, nil
}

func (e *ECAPA) Dim() int { return e.dim }

func (e *ECAPA) Close() error {
	if e.sess != nil {
		return e.sess.Destroy()
	}
	return nil
}

// EmbedFeats runs the ONNX model on precomputed (T x 80) features and returns the
// L2-normalized 192-d embedding. Exposed so the frontend can be validated independently.
func (e *ECAPA) EmbedFeats(feats [][]float32) ([]float32, error) {
	if len(feats) == 0 {
		return nil, fmt.Errorf("speaker: empty features")
	}
	T := len(feats)
	flat := make([]float32, T*numMel)
	for t := 0; t < T; t++ {
		copy(flat[t*numMel:(t+1)*numMel], feats[t])
	}
	inT, err := ort.NewTensor(ort.NewShape(1, int64(T), int64(numMel)), flat)
	if err != nil {
		return nil, err
	}
	defer inT.Destroy()
	outT, err := ort.NewEmptyTensor[float32](ort.NewShape(1, int64(e.dim)))
	if err != nil {
		return nil, err
	}
	defer outT.Destroy()
	if err := e.sess.Run([]ort.Value{inT}, []ort.Value{outT}); err != nil {
		return nil, err
	}
	raw := outT.GetData()
	out := make([]float32, len(raw))
	copy(out, raw)
	return l2norm(out), nil
}

// Embed computes fbank features for a mono 16 kHz window and returns its embedding.
func (e *ECAPA) Embed(wav []float32) ([]float32, error) {
	feats := Fbank(wav)
	if feats == nil {
		return nil, fmt.Errorf("speaker: clip too short (%d samples)", len(wav))
	}
	return e.EmbedFeats(feats)
}

func l2norm(v []float32) []float32 {
	var n float64
	for _, x := range v {
		n += float64(x) * float64(x)
	}
	n = math.Sqrt(n) + 1e-9
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
	return v
}
