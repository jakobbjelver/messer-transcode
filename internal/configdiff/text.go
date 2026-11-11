package configdiff

import (
	"fmt"
	"strings"
)

// unified is a line diff in the familiar shape. It is here for the "raw" view
// only: the dialog's primary reading is the structural change list, and this is
// what an operator looks at when they want to see the file.
func unified(before, after string) string {
	a := strings.Split(before, "\n")
	b := strings.Split(after, "\n")

	// Longest common subsequence over lines. The documents are a few hundred
	// lines; the table is 400² at most.
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	var out strings.Builder
	out.WriteString("--- current\n+++ saved\n")
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			fmt.Fprintf(&out, "  %s\n", a[i])
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			fmt.Fprintf(&out, "- %s\n", a[i])
			i++
		default:
			fmt.Fprintf(&out, "+ %s\n", b[j])
			j++
		}
	}
	for ; i < n; i++ {
		fmt.Fprintf(&out, "- %s\n", a[i])
	}
	for ; j < m; j++ {
		fmt.Fprintf(&out, "+ %s\n", b[j])
	}
	return out.String()
}
