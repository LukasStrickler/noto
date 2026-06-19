package metrics

import (
	"reflect"
	"testing"
)

func TestHungarian(t *testing.T) {
	cases := []struct {
		name string
		cost [][]float64
		want []int
	}{
		{"identity 2x2", [][]float64{{1, 2}, {2, 1}}, []int{0, 1}},
		{"swap 2x2", [][]float64{{2, 1}, {1, 2}}, []int{1, 0}},
		{"diag 3x3", [][]float64{{0, 5, 5}, {5, 0, 5}, {5, 5, 0}}, []int{0, 1, 2}},
		{"permuted 3x3", [][]float64{{5, 0, 5}, {0, 5, 5}, {5, 5, 0}}, []int{1, 0, 2}},
		{"1x1", [][]float64{{7}}, []int{0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hungarian(c.cost); !reflect.DeepEqual(got, c.want) {
				t.Errorf("hungarian(%v) = %v, want %v", c.cost, got, c.want)
			}
		})
	}
}

func TestHungarianMinimizes(t *testing.T) {
	// A cost matrix where the greedy choice is wrong: row0's cheapest is col0(1),
	// but the optimal assignment takes row0→col1 to free col0 for row1.
	cost := [][]float64{{1, 2, 3}, {2, 4, 6}, {3, 6, 9}}
	got := hungarian(cost)
	total := 0.0
	for i, j := range got {
		total += cost[i][j]
	}
	// Optimal is row0→col2(3), row1→col1(4), row2→col0(3) = 10? check all perms:
	// the min perfect assignment total for this matrix is 10.
	approx(t, "total", total, 10)
}

func TestMaxWeightMapping(t *testing.T) {
	cases := []struct {
		name   string
		weight [][]float64
		want   map[int]int
	}{
		{"diag", [][]float64{{10, 1}, {1, 10}}, map[int]int{0: 0, 1: 1}},
		{"swap", [][]float64{{1, 10}, {10, 1}}, map[int]int{0: 1, 1: 0}},
		{"rectangular", [][]float64{{5, 0, 0}, {0, 0, 5}}, map[int]int{0: 0, 1: 2}},
		{"all zero", [][]float64{{0, 0}, {0, 0}}, map[int]int{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := maxWeightMapping(c.weight); !reflect.DeepEqual(got, c.want) {
				t.Errorf("maxWeightMapping(%v) = %v, want %v", c.weight, got, c.want)
			}
		})
	}
}
