package bench_test

import (
	"testing"

	"github.com/lukasstrickler/noto/internal/platform/bench"
)

func TestEffectiveBudget_MinTierAndOverride(t *testing.T) {
	if got := bench.EffectiveBudget("gate", 1.0); got != 1.0 {
		t.Fatalf("got %v want 1.0", got)
	}
	if got := bench.EffectiveBudget("gate", 0); got != 1.5 {
		t.Fatalf("got %v want 1.5", got)
	}
}

// A 0 override resolves to the full per-tier cap (what `bench run` and now
// `bench preflight` both pass when --budget-usd is unset) — so a non-gate tier
// reports its real ceiling, not the gate's 1.5.
func TestEffectiveBudget_ZeroOverrideUsesTierCap(t *testing.T) {
	if got := bench.EffectiveBudget("confirm", 0); got != 5.0 {
		t.Fatalf("confirm tier default = %v; want 5.0 (not the gate 1.5)", got)
	}
	if got := bench.EffectiveBudget("baseline_variance", 0); got != 15.0 {
		t.Fatalf("baseline_variance tier default = %v; want 15.0", got)
	}
}

// A quick AMI run with no explicit Hours (an unknown suite id) processes a
// subsample, so it must project against the ~2h gate slice, not the full 11.44h
// corpus; only the explicit non-quick anchor uses the whole corpus.
func TestSuiteAudioHours_QuickAMIUsesSubsample(t *testing.T) {
	if got := bench.SuiteAudioHours("typo_ami@2026"); got != bench.AMIQuickAudioHours {
		t.Fatalf("unknown quick AMI suite = %vh; want %vh (subsample, not full corpus)", got, bench.AMIQuickAudioHours)
	}
	if got := bench.SuiteAudioHours("anchor_ami@2026-06-18"); got != bench.AMIAnchorAudioHours {
		t.Fatalf("anchor suite = %vh; want %vh (full corpus)", got, bench.AMIAnchorAudioHours)
	}
	if got := bench.SuiteAudioHours("gate_ami@2026-06-18"); got != 2 {
		t.Fatalf("gate suite = %vh; want 2 (its explicit Hours)", got)
	}
}

func TestResolveSuite_GateAMI(t *testing.T) {
	s := bench.ResolveSuite("gate_ami@2026-06-18")
	if s.ModalSuite != "ami" || !s.Quick {
		t.Fatalf("suite: %+v", s)
	}
}

func TestModalProfile_BatchQueue(t *testing.T) {
	if bench.ModalProfile("batch_queue") != "batch" {
		t.Fatal()
	}
	if bench.ModalProfile("single_meeting") != "prod" {
		t.Fatal()
	}
}
