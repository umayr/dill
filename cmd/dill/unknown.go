package main

var allCommands = []string{
	cmdUp, cmdDown, cmdTeardown, cmdStop, cmdStart, cmdRestart,
	cmdPs, cmdLogs, cmdPull, cmdImages, cmdExec, cmdPlan, cmdConfig,
	cmdInit, cmdValidate, cmdFmt, cmdVersion,
}

// closestCommand returns the command name most similar to s, or "" if no
// command is close enough (edit distance > 2 or > half the input length).
func closestCommand(s string) string {
	threshold := 2
	if len(s) > 4 {
		threshold = len(s) / 2
	}
	best, bestDist := "", threshold+1
	for _, cmd := range allCommands {
		if d := levenshtein(s, cmd); d < bestDist {
			best, bestDist = cmd, d
		}
	}
	return best
}

// levenshtein computes the edit distance between two strings.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	m, n := len(ra), len(rb)
	dp := make([]int, n+1)
	for j := range dp {
		dp[j] = j
	}
	for i := 1; i <= m; i++ {
		prev := i
		for j := 1; j <= n; j++ {
			cur := dp[j-1]
			if ra[i-1] != rb[j-1] {
				cur = 1 + min3(dp[j], prev, dp[j-1])
			}
			dp[j-1] = prev
			prev = cur
		}
		dp[n] = prev
	}
	return dp[n]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
