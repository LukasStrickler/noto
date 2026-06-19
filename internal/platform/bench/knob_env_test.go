package bench_test

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestKnobEnv_VADMapsToLauncherVar(t *testing.T) {
	// The plan-documented `--knob vad=on` must reach modal_benchmark.py via its
	// launcher var BENCH_VAD (which the Python forwards into the container as
	// NOTO_VAD). A bare NOTO_VAD on the launcher would be ignored.
	env := bench.KnobEnv(map[string]string{"vad": "on"})
	if len(env) != 1 || env[0] != "BENCH_VAD=on" {
		t.Fatalf("vad knob env=%v want [BENCH_VAD=on]", env)
	}
}

func TestKnobEnv_EmbBatchMapsToLauncherVar(t *testing.T) {
	// `--knob emb_batch=64` is the H6 diar-embedding-batch lever; it must reach
	// the launcher as BENCH_PYANNOTE_EMB_BATCH (forwarded to NOTO_PYANNOTE_EMB_BATCH,
	// which configure_batching reads). A bare NOTO_EMB_BATCH would be ignored.
	env := bench.KnobEnv(map[string]string{"emb_batch": "64"})
	if len(env) != 1 || env[0] != "BENCH_PYANNOTE_EMB_BATCH=64" {
		t.Fatalf("emb_batch knob env=%v want [BENCH_PYANNOTE_EMB_BATCH=64]", env)
	}
}

func TestKnobEnv_UnknownKnobKeepsNotoPrefix(t *testing.T) {
	env := bench.KnobEnv(map[string]string{"custom_flag": "1"})
	if len(env) != 1 || env[0] != "NOTO_CUSTOM_FLAG=1" {
		t.Fatalf("env=%v want [NOTO_CUSTOM_FLAG=1]", env)
	}
}

func TestKnobEnv_ExplicitBenchPrefixPreserved(t *testing.T) {
	env := bench.KnobEnv(map[string]string{"BENCH_DIAR_POOL": "4"})
	if len(env) != 1 || env[0] != "BENCH_DIAR_POOL=4" {
		t.Fatalf("env=%v want [BENCH_DIAR_POOL=4]", env)
	}
}
