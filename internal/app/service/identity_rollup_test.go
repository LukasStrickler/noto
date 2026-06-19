package service

import "testing"

// identitySummary folds per-status mapping counts into the dashboard's four
// buckets: auto+manual→Resolved, pending→Likely, new→New, the rest→Unset.
func TestIdentitySummaryBuckets(t *testing.T) {
	got := identitySummary(map[string]int{
		"auto": 2, "manual": 1, "pending": 1, "new": 1, "unmatched": 2, "": 1,
	})
	if got == nil {
		t.Fatal("expected a summary, got nil")
	}
	if got.Resolved != 3 {
		t.Errorf("Resolved = %d, want 3 (auto+manual)", got.Resolved)
	}
	if got.Likely != 1 {
		t.Errorf("Likely = %d, want 1 (pending)", got.Likely)
	}
	if got.New != 1 {
		t.Errorf("New = %d, want 1", got.New)
	}
	if got.Unset != 3 {
		t.Errorf("Unset = %d, want 3 (unmatched + blank)", got.Unset)
	}

	if identitySummary(map[string]int{}) != nil {
		t.Error("empty counts should yield nil (no Identity attached)")
	}
	if identitySummary(map[string]int{"auto": 0}) != nil {
		t.Error("all-zero counts should yield nil")
	}
}
