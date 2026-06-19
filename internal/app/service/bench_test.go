package service_test

import (
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/config"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

func TestBenchPreflight_RejectsKnobBundleOnGate(t *testing.T) {
	svc := newModalTestSvc(t, config.DefaultConfig(), nil)
	res, err := svc.BenchPreflight(context.Background(), notoapi.BenchPreflightRequest{
		SuiteID:          "gate_ami@2026-06-18",
		Tier:             "gate",
		OperatingMode:    "batch_queue",
		ExecutionProfile: "modal_cuda",
		BudgetUSDCap:     1.5,
		Knobs:            map[string]string{"vad": "on", "jobs": "10"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Blockers) == 0 {
		t.Fatal("expected blockers for knob bundle on gate tier")
	}
}

func TestBenchRun_IntegrationOnly(t *testing.T) {
	svc := newModalTestSvc(t, config.DefaultConfig(), nil)
	res, err := svc.BenchRun(context.Background(), notoapi.BenchRunRequest{
		SuiteID:          "smoke@2026-06-18",
		Tier:             "smoke",
		OperatingMode:    "batch_queue",
		ExecutionProfile: "modal_cuda",
		IntegrationOnly:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.RunID == "" || !res.TraceValid {
		t.Fatalf("result: %+v", res)
	}
}

func TestRunModalBenchmark_DelegatesToBenchRun(t *testing.T) {
	t.Setenv("NOTO_BENCH_INTEGRATION_ONLY", "1")
	svc := newModalTestSvc(t, config.DefaultConfig(), nil)
	res, err := svc.RunModalBenchmark(context.Background(), notoapi.ModalBenchmarkRequest{Suite: "smoke@2026-06-18"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Accepted || res.RunID == "" {
		t.Fatalf("result: %+v", res)
	}
}
