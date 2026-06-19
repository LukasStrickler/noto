package metrics

import (
	"testing"
	"time"
)

func TestAttributionAccuracy(t *testing.T) {
	t.Run("perfect", func(t *testing.T) {
		ref := []Segment{{"A", 0, 10}}
		hyp := []Segment{{"X", 0, 10}}
		approx(t, "acc", AttributionAccuracy(ref, hyp), 1.0)
	})
	t.Run("swap forgiven", func(t *testing.T) {
		ref := []Segment{{"A", 0, 5}, {"B", 5, 10}}
		hyp := []Segment{{"Y", 0, 5}, {"X", 5, 10}}
		approx(t, "acc", AttributionAccuracy(ref, hyp), 1.0)
	})
	t.Run("half wrong", func(t *testing.T) {
		ref := []Segment{{"A", 0, 5}, {"B", 5, 10}}
		hyp := []Segment{{"X", 0, 10}}
		approx(t, "acc", AttributionAccuracy(ref, hyp), 0.5)
	})
	t.Run("no reference", func(t *testing.T) {
		approx(t, "acc", AttributionAccuracy(nil, []Segment{{"X", 0, 5}}), 0)
	})
}

func TestTimingRTF(t *testing.T) {
	cases := []struct {
		wall  time.Duration
		audio float64
		want  float64
	}{
		{30 * time.Second, 60, 0.5},
		{2 * time.Minute, 60, 2.0},
		{time.Second, 0, 0}, // guard against divide-by-zero
	}
	for _, c := range cases {
		got := Timing{Wall: c.wall, AudioSeconds: c.audio}.RTF()
		approx(t, "RTF", got, c.want)
	}
}
