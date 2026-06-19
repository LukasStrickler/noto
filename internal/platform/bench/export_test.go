package bench

import "time"

// SetSpendClock overrides a SpendLog's clock for deterministic window tests.
func SetSpendClock(l *SpendLog, now func() time.Time) { l.now = now }

// KnobEnv exposes the unexported knob→launcher-env mapping for tests.
func KnobEnv(knobs map[string]string) []string { return knobEnv(knobs) }
