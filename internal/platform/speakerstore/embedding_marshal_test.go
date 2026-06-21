package speakerstore

import "testing"

// TestUnmarshalEmbedding pins the voiceprint blob codec, including the fail-safe
// for a corrupt/partial blob: a length that isn't a multiple of 8 must decode to
// nil ("no voiceprint"), not a silently truncated wrong-dimension vector that the
// matcher would then trust.
func TestUnmarshalEmbedding(t *testing.T) {
	t.Run("round-trips a well-formed vector", func(t *testing.T) {
		v := []float64{1, -2, 3.5, 0}
		got := unmarshalEmbedding(marshalEmbedding(v))
		if len(got) != len(v) {
			t.Fatalf("len = %d; want %d", len(got), len(v))
		}
		for i := range v {
			if got[i] != v[i] {
				t.Errorf("got[%d] = %v; want %v", i, got[i], v[i])
			}
		}
	})

	t.Run("empty decodes to nil", func(t *testing.T) {
		if got := unmarshalEmbedding(nil); got != nil {
			t.Errorf("got %v; want nil", got)
		}
	})

	t.Run("malformed (non-multiple-of-8) decodes to nil, not a truncated vector", func(t *testing.T) {
		blob := marshalEmbedding([]float64{1, 2, 3}) // 24 bytes
		blob = blob[:len(blob)-3]                    // 21 bytes — not a multiple of 8
		if got := unmarshalEmbedding(blob); got != nil {
			t.Errorf("malformed blob decoded to len=%d; want nil (a corrupt blob must not become a wrong-dim voiceprint)", len(got))
		}
	})
}
