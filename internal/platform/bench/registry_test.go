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
