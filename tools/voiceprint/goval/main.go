// Command goval validates the Go ECAPA frontend+embedder against the Python reference
// dumped to /tmp/ref (wav.f32, fbank.f32, emb.f32, shape.txt). Not part of the product;
// a de-risking harness for the pure-Go fbank port.
package main

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"math"
	"os"

	"github.com/lukasstrickler/noto/internal/platform/providers/speaker"
)

func readF32(path string, n int) []float32 {
	f, err := os.Open(path)
	must(err)
	defer f.Close()
	out := make([]float32, n)
	must(binary.Read(bufio.NewReader(f), binary.LittleEndian, out))
	return out
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	return dot / (math.Sqrt(na)*math.Sqrt(nb) + 1e-12)
}

func main() {
	var nSamp, T, F, D int
	sf, err := os.Open("/tmp/ref/shape.txt")
	must(err)
	fmt.Fscanf(sf, "%d %d %d %d", &nSamp, &T, &F, &D)
	sf.Close()
	wav := readF32("/tmp/ref/wav.f32", nSamp)
	refFb := readF32("/tmp/ref/fbank.f32", T*F)
	refEmb := readF32("/tmp/ref/emb.f32", D)
	fmt.Printf("ref: wav=%d fbank=%dx%d emb=%d\n", nSamp, T, F, D)

	model := "tools/voiceprint/models/ecapa512.onnx"
	lib := "tools/voiceprint/onnxruntime/lib/libonnxruntime.so"
	if p := os.Getenv("ECAPA_MODEL"); p != "" {
		model = p
	}
	if p := os.Getenv("ORT_LIB"); p != "" {
		lib = p
	}
	eng, err := speaker.NewECAPA(model, lib)
	must(err)
	defer eng.Close()

	// (1) fbank parity: Go fbank vs ref fbank
	goFb := speaker.Fbank(wav)
	var maxAbs, sumAbs float64
	for t := 0; t < T; t++ {
		for b := 0; b < F; b++ {
			d := math.Abs(float64(goFb[t][b]) - float64(refFb[t*F+b]))
			if d > maxAbs {
				maxAbs = d
			}
			sumAbs += d
		}
	}
	fmt.Printf("(1) fbank parity   : max|Δ|=%.6f  mean|Δ|=%.6f  (frames go=%d ref=%d)\n",
		maxAbs, sumAbs/float64(T*F), len(goFb), T)

	// (2) ONNX isolation: run model on REF fbank -> compare to ref emb
	refFb2 := make([][]float32, T)
	for t := 0; t < T; t++ {
		refFb2[t] = refFb[t*F : (t+1)*F]
	}
	embFromRef, err := eng.EmbedFeats(refFb2)
	must(err)
	fmt.Printf("(2) onnx vs python : cosine=%.6f  (model on identical features)\n",
		cosine(embFromRef, refEmb))

	// (3) end-to-end: Go fbank -> model -> compare to ref emb
	embE2E, err := eng.Embed(wav)
	must(err)
	fmt.Printf("(3) end-to-end     : cosine=%.6f  (Go fbank + Go onnx vs Python)\n",
		cosine(embE2E, refEmb))
}
