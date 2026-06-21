package cli

import (
	"bytes"
	"context"
	"testing"

	"github.com/lukasstrickler/noto/internal/testutil"
	"github.com/lukasstrickler/noto/internal/transport/notoapi"
)

// TestRunSearch_PassesFullMultiWordQuery pins that the CLI forwards the WHOLE
// query to the backend. stripFlags returned only the first non-flag arg, so
// `noto search ship plan` silently searched just "ship" — defeating the FTS
// multi-word fix for every CLI user.
func TestRunSearch_PassesFullMultiWordQuery(t *testing.T) {
	var gotQuery string
	fc := testutil.NewFakeClient()
	fc.SearchFn = func(_ context.Context, opts notoapi.SearchOpts) (notoapi.SearchResult, error) {
		gotQuery = opts.Query
		return notoapi.SearchResult{}, nil
	}
	var out, errOut bytes.Buffer
	a := &app{
		out:    &out,
		errOut: &errOut,
		connectFn: func(context.Context) (notoapi.Client, func(), int) {
			return fc, func() {}, 0
		},
	}

	if code := a.runSearch([]string{"ship", "plan", "--json"}); code != 0 {
		t.Fatalf("runSearch exit %d; stderr=%s", code, errOut.String())
	}
	if gotQuery != "ship plan" {
		t.Errorf("query passed to client = %q; want %q (multi-word dropped?)", gotQuery, "ship plan")
	}
}

// TestStripFlagsJoin verifies the multi-word helper joins non-flag args while
// stripFlags keeps its single-token behavior (used by id-based commands).
func TestStripFlagsJoin(t *testing.T) {
	if got := stripFlagsJoin([]string{"ship", "plan", "--json"}); got != "ship plan" {
		t.Errorf("stripFlagsJoin = %q; want %q", got, "ship plan")
	}
	if got := stripFlagsJoin([]string{"--json"}); got != "" {
		t.Errorf("stripFlagsJoin with only flags = %q; want empty", got)
	}
	if got := stripFlags([]string{"alpha", "beta"}); got != "alpha" {
		t.Errorf("stripFlags should keep first-arg behavior, got %q", got)
	}
}
