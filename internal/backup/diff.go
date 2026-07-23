package backup

import "strings"

// DiffLine is one line of the merge page diff.
type DiffLine struct {
	Kind string // same, add, del
	Text string
}

// DiffLines computes a line diff from old to new via an LCS table. It
// returns ok false when either side exceeds maxLines, the quadratic table
// would get too big, the merge page then falls back to the raw views.
func DiffLines(oldText, newText string, maxLines int) ([]DiffLine, bool) {
	a := strings.Split(oldText, "\n")
	b := strings.Split(newText, "\n")
	if len(a) > maxLines || len(b) > maxLines {
		return nil, false
	}
	n, m := len(a), len(b)
	dp := make([]int32, (n+1)*(m+1))
	idx := func(i, j int) int { return i*(m+1) + j }
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[idx(i, j)] = dp[idx(i+1, j+1)] + 1
			} else {
				dp[idx(i, j)] = max(dp[idx(i+1, j)], dp[idx(i, j+1)])
			}
		}
	}
	out := make([]DiffLine, 0, max(n, m))
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			out = append(out, DiffLine{"same", a[i]})
			i++
			j++
		case dp[idx(i+1, j)] >= dp[idx(i, j+1)]:
			out = append(out, DiffLine{"del", a[i]})
			i++
		default:
			out = append(out, DiffLine{"add", b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		out = append(out, DiffLine{"del", a[i]})
	}
	for ; j < m; j++ {
		out = append(out, DiffLine{"add", b[j]})
	}
	return out, true
}
