package e2e

import "testing"

// TestDiarHintSpeakers verifies the BENCH_DIAR_SPEAKERS override: the bench
// defaults to the oracle count, and only the documented "auto"/"0" values flip
// it to the product-realistic auto-detect (NumSpeakers:0). A pure unit test —
// no AMI data or GPU.
func TestDiarHintSpeakers(t *testing.T) {
	cases := []struct {
		env    string
		oracle int
		want   int
	}{
		{"", 4, 4},        // unset → oracle (ledger baseline)
		{"oracle", 4, 4},  // explicit non-auto → oracle
		{"auto", 4, 0},    // product-realistic auto-detect
		{"AUTO", 3, 0},    // case-insensitive
		{" auto ", 3, 0},  // trimmed
		{"0", 5, 0},       // 0 is an alias for auto
		{"garbage", 2, 2}, // unrecognized → oracle (safe default)
	}
	for _, c := range cases {
		t.Setenv("BENCH_DIAR_SPEAKERS", c.env)
		if got := diarHintSpeakers(c.oracle); got != c.want {
			t.Errorf("diarHintSpeakers(%d) with BENCH_DIAR_SPEAKERS=%q = %d; want %d",
				c.oracle, c.env, got, c.want)
		}
	}
}
