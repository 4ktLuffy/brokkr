package minimize

import "errors"

// ErrBudget is returned by a test function when no oracle runs are left.
var ErrBudget = errors.New("run budget exhausted")

// ddmin is Zeller's delta debugging minimizer. units are the ids of the
// pieces currently kept; test reports whether a subset still passes. It
// returns the smallest passing subset found, which is 1-minimal (no single
// unit can be removed) unless err is not nil (budget or a failing oracle), in
// which case it is the best passing subset seen so far. The full set must be
// known to pass.
func ddmin(units []int, test func([]int) (bool, error)) ([]int, error) {
	c := append([]int(nil), units...)
	n := 2
	for len(c) >= 2 {
		subsets := split(c, n)
		reduced := false
		for _, s := range subsets {
			ok, err := test(s)
			if err != nil {
				return c, err
			}
			if ok {
				c, n, reduced = s, 2, true
				break
			}
		}
		if !reduced {
			for i := range subsets {
				comp := complement(subsets, i)
				ok, err := test(comp)
				if err != nil {
					return c, err
				}
				if ok {
					c, reduced = comp, true
					n = max(n-1, 2)
					break
				}
			}
		}
		if !reduced {
			if n >= len(c) {
				break
			}
			n = min(len(c), 2*n)
		}
	}
	// With one unit left, the only smaller subset is the empty one (a task the
	// unpatched repository already passes).
	if len(c) == 1 {
		ok, err := test(nil)
		if err != nil {
			return c, err
		}
		if ok {
			c = nil
		}
	}
	return c, nil
}

// split cuts c into n chunks of near-equal size, in order.
func split(c []int, n int) [][]int {
	n = min(n, len(c))
	var out [][]int
	start := 0
	for i := 0; i < n; i++ {
		end := start + (len(c)-start)/(n-i)
		out = append(out, c[start:end])
		start = end
	}
	return out
}

func complement(subsets [][]int, skip int) []int {
	var out []int
	for i, s := range subsets {
		if i != skip {
			out = append(out, s...)
		}
	}
	return out
}
