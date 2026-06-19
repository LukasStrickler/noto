package metrics

import "math"

// hungarian computes a minimum-cost perfect assignment of an n×n cost matrix and
// returns rowToCol, where rowToCol[i] is the column assigned to row i. It is the
// classic O(n³) Kuhn–Munkres / Jonker–Volgenant augmenting-path implementation
// with row/column potentials, and works with arbitrary real costs (including
// zeros and negatives). The matrix must be square; pad rectangular problems with
// dummy rows/columns before calling.
func hungarian(cost [][]float64) []int {
	n := len(cost)
	if n == 0 {
		return nil
	}

	const inf = math.MaxFloat64
	u := make([]float64, n+1) // row potentials (1-indexed)
	v := make([]float64, n+1) // column potentials (1-indexed)
	p := make([]int, n+1)     // p[j] = row matched to column j (0 = unmatched)
	way := make([]int, n+1)   // augmenting-path predecessor columns

	for i := 1; i <= n; i++ {
		p[0] = i
		j0 := 0
		minv := make([]float64, n+1)
		used := make([]bool, n+1)
		for j := 0; j <= n; j++ {
			minv[j] = inf
		}
		for {
			used[j0] = true
			i0 := p[j0]
			delta := inf
			j1 := -1
			for j := 1; j <= n; j++ {
				if used[j] {
					continue
				}
				cur := cost[i0-1][j-1] - u[i0] - v[j]
				if cur < minv[j] {
					minv[j] = cur
					way[j] = j0
				}
				if minv[j] < delta {
					delta = minv[j]
					j1 = j
				}
			}
			for j := 0; j <= n; j++ {
				if used[j] {
					u[p[j]] += delta
					v[j] -= delta
				} else {
					minv[j] -= delta
				}
			}
			j0 = j1
			if p[j0] == 0 {
				break
			}
		}
		for j0 != 0 {
			j1 := way[j0]
			p[j0] = p[j1]
			j0 = j1
		}
	}

	rowToCol := make([]int, n)
	for j := 1; j <= n; j++ {
		if p[j] != 0 {
			rowToCol[p[j]-1] = j - 1
		}
	}
	return rowToCol
}

// maxWeightMapping returns a one-to-one mapping rowIdx → colIdx that maximizes
// the total weight of the rectangular weight matrix, keeping only pairs with a
// strictly positive weight (a pair with zero co-occurrence is meaningless). It
// pads to a square cost matrix (cost = maxWeight − weight; dummy cells weight 0)
// and runs the min-cost hungarian solver.
func maxWeightMapping(weight [][]float64) map[int]int {
	rows := len(weight)
	if rows == 0 {
		return map[int]int{}
	}
	cols := len(weight[0])
	if cols == 0 {
		return map[int]int{}
	}

	maxW := 0.0
	for i := range weight {
		for j := range weight[i] {
			if weight[i][j] > maxW {
				maxW = weight[i][j]
			}
		}
	}

	n := max(rows, cols)
	cost := make([][]float64, n)
	for i := 0; i < n; i++ {
		cost[i] = make([]float64, n)
		for j := 0; j < n; j++ {
			w := 0.0
			if i < rows && j < cols {
				w = weight[i][j]
			}
			cost[i][j] = maxW - w
		}
	}

	rowToCol := hungarian(cost)
	mapping := make(map[int]int)
	for i := 0; i < rows; i++ {
		j := rowToCol[i]
		if j < cols && weight[i][j] > 0 {
			mapping[i] = j
		}
	}
	return mapping
}
