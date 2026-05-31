package tui

import "testing"

// TestUpdatePeakDecayHoldLatch covers the three behaviours design.md asks of
// the meter read-out: a held peak that decays one step per sample, a peak that
// jumps up immediately on a louder sample, and a CLIP latch that stays set.
func TestUpdatePeakDecayHoldLatch(t *testing.T) {
	t.Run("decays toward the floor", func(t *testing.T) {
		peak, clip := -20, false
		peak, clip = updatePeak(peak, clip, dbFloor)
		if peak != -21 {
			t.Fatalf("peak = %d, want -21 (one decay step)", peak)
		}
		if clip {
			t.Fatal("clip latched on a quiet sample")
		}
	})

	t.Run("never decays below the floor", func(t *testing.T) {
		peak, _ := updatePeak(dbFloor, false, dbFloor)
		if peak != dbFloor {
			t.Fatalf("peak = %d, want floor %d", peak, dbFloor)
		}
	})

	t.Run("jumps up to a louder sample", func(t *testing.T) {
		peak, _ := updatePeak(-40, false, -10)
		if peak != -10 {
			t.Fatalf("peak = %d, want -10 (raised to sample)", peak)
		}
	})

	t.Run("latches CLIP at the threshold and holds it", func(t *testing.T) {
		peak, clip := updatePeak(-30, false, clipThresholdDB)
		if !clip {
			t.Fatalf("clip = false, want latched at threshold %d", clipThresholdDB)
		}
		if peak != clipThresholdDB {
			t.Fatalf("peak = %d, want %d", peak, clipThresholdDB)
		}
		// Latch survives subsequent quiet samples.
		_, clip = updatePeak(peak, clip, dbFloor)
		if !clip {
			t.Fatal("clip latch cleared on a later quiet sample")
		}
	})
}
