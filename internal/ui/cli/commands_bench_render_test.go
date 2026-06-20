package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// On the incomparable path the service returns before computing cost, so
// delta_pct is a Go zero value, not a measured 0%. The CLI must surface the
// reason rather than print a fake "delta_pct: 0.0%" that reads like a real
// no-change measurement.
func TestRunBenchCompare_IncomparablePrintsReasonNotZeroDelta(t *testing.T) {
	fc := testutil.NewFakeClient()
	fc.BenchCompareValue = &notoapi.BenchCompareResult{
		SchemaVersion:      "compare.v1",
		Decision:           "retry",
		Comparable:         false,
		IncomparableReason: "suite_mismatch",
	}
	var out, errOut bytes.Buffer
	a := &app{
		out:    &out,
		errOut: &errOut,
		connectFn: func(context.Context) (notoapi.Client, func(), int) {
			return fc, func() {}, 0
		},
	}

	if code := a.runBenchCompare([]string{"--baseline", "b", "--candidate", "c"}); code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "reason: suite_mismatch") {
		t.Errorf("incomparable compare must print the reason; got:\n%s", got)
	}
	if strings.Contains(got, "delta_pct") {
		t.Errorf("incomparable compare must NOT print a meaningless delta_pct; got:\n%s", got)
	}
}

// The comparable path still shows delta_pct (the real cost change).
func TestRunBenchCompare_ComparableShowsDeltaPct(t *testing.T) {
	fc := testutil.NewFakeClient()
	fc.BenchCompareValue = &notoapi.BenchCompareResult{
		SchemaVersion: "compare.v1",
		Decision:      "adopt",
		Comparable:    true,
		Cost:          notoapi.BenchCompareCost{DeltaPct: -3.2},
	}
	var out, errOut bytes.Buffer
	a := &app{
		out:    &out,
		errOut: &errOut,
		connectFn: func(context.Context) (notoapi.Client, func(), int) {
			return fc, func() {}, 0
		},
	}

	if code := a.runBenchCompare([]string{"--baseline", "b", "--candidate", "c"}); code != 0 {
		t.Fatalf("exit %d; stderr=%s", code, errOut.String())
	}
	if got := out.String(); !strings.Contains(got, "delta_pct: -3.2%") {
		t.Errorf("comparable compare must show delta_pct; got:\n%s", got)
	}
}
