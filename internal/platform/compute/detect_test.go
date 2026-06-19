package compute

import (
	"errors"
	"reflect"
	"testing"
)

func TestDetectLadderByOS(t *testing.T) {
	cases := []struct {
		os      string
		wantBE  Backend
		wantEPs []string
	}{
		{"darwin", BackendCoreML, []string{epCoreML, epCPU}},
		{"linux", BackendCUDA, []string{epCUDA, epCPU}},
		{"windows", BackendCUDA, []string{epCUDA, epCPU}},
		{"plan9", BackendCPU, []string{epCPU}},
	}
	for _, c := range cases {
		// No Verify hook → trust the ladder, pick the top candidate.
		got := Detect(Options{OS: c.os})
		if got.Backend != c.wantBE {
			t.Errorf("%s: backend = %s, want %s", c.os, got.Backend, c.wantBE)
		}
		if !reflect.DeepEqual(got.EPList, c.wantEPs) {
			t.Errorf("%s: eps = %v, want %v", c.os, got.EPList, c.wantEPs)
		}
		if got.Verified {
			t.Errorf("%s: Verified should be false without a Verify hook", c.os)
		}
		if got.EPList[len(got.EPList)-1] != epCPU {
			t.Errorf("%s: EP list must end with CPU, got %v", c.os, got.EPList)
		}
	}
}

func TestDetectVerifyFallsBack(t *testing.T) {
	// CoreML "fails" → must fall back to CPU and mark verified.
	fail := func(b Backend) error {
		if b == BackendCoreML {
			return errors.New("no ANE")
		}
		return nil
	}
	got := Detect(Options{OS: "darwin", Verify: fail})
	if got.Backend != BackendCPU {
		t.Fatalf("backend = %s, want cpu (CoreML should have failed verification)", got.Backend)
	}
	if !got.Verified {
		t.Errorf("Verified should be true: CPU passed the hook")
	}
}

func TestDetectVerifyTopWins(t *testing.T) {
	got := Detect(Options{OS: "linux", Verify: func(Backend) error { return nil }})
	if got.Backend != BackendCUDA || !got.Verified {
		t.Fatalf("got %s verified=%v, want cuda verified", got.Backend, got.Verified)
	}
}

func TestDetectAllFailLandsOnCPUUnverified(t *testing.T) {
	got := Detect(Options{OS: "linux", Verify: func(Backend) error { return errors.New("nope") }})
	if got.Backend != BackendCPU {
		t.Fatalf("backend = %s, want cpu", got.Backend)
	}
	if got.Verified {
		t.Errorf("Verified should be false when every candidate (incl. CPU) failed")
	}
}

func TestDetectOverridePinStartsLadderButFallsBack(t *testing.T) {
	// Pin CUDA on a Mac. Ladder becomes [cuda, cpu]; CUDA fails → CPU.
	got := Detect(Options{
		OS:       "darwin",
		Override: BackendCUDA,
		Verify: func(b Backend) error {
			if b == BackendCUDA {
				return errors.New("no gpu")
			}
			return nil
		},
	})
	if got.Backend != BackendCPU {
		t.Fatalf("backend = %s, want cpu after bad CUDA pin", got.Backend)
	}
	if !got.Verified {
		t.Errorf("Verified should be true (CPU passed)")
	}
}

func TestDetectOverrideHonoredWhenAvailable(t *testing.T) {
	got := Detect(Options{OS: "darwin", Override: BackendCPU})
	if got.Backend != BackendCPU || !reflect.DeepEqual(got.EPList, []string{epCPU}) {
		t.Fatalf("got %s eps=%v, want cpu/[cpu]", got.Backend, got.EPList)
	}
}

func TestDetectTierAndThreadsDefaults(t *testing.T) {
	got := Detect(Options{OS: "linux"})
	if got.Tier != TierAccurate {
		t.Errorf("default tier = %s, want accurate", got.Tier)
	}
	if got.Threads <= 0 {
		t.Errorf("threads = %d, want >0 (nproc)", got.Threads)
	}
	fast := Detect(Options{OS: "linux", Tier: TierFast, Threads: 3})
	if fast.Tier != TierFast || fast.Threads != 3 {
		t.Errorf("got tier=%s threads=%d, want fast/3", fast.Tier, fast.Threads)
	}
}

func TestParseBackend(t *testing.T) {
	cases := map[string]Backend{
		"cuda": BackendCUDA, "gpu": BackendCUDA, "NVIDIA": BackendCUDA,
		"coreml": BackendCoreML, "ane": BackendCoreML, "metal": BackendCoreML,
		"cpu": BackendCPU,
		"":    BackendAuto, "auto": BackendAuto, "garbage": BackendAuto,
	}
	for in, want := range cases {
		if got := ParseBackend(in); got != want {
			t.Errorf("ParseBackend(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestParseTier(t *testing.T) {
	if ParseTier("fast") != TierFast {
		t.Error("fast")
	}
	if ParseTier("FAST") != TierFast {
		t.Error("FAST")
	}
	if ParseTier("") != TierAccurate || ParseTier("accurate") != TierAccurate || ParseTier("x") != TierAccurate {
		t.Error("accurate default")
	}
}
