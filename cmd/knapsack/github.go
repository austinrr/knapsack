package main

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// githubToken finds a GitHub token the way the LemonUp fork did: GITHUB_TOKEN,
// then GH_TOKEN, then the GitHub CLI's login. Knapsack never stores it
// (DESIGN.md §5.2). It returns "" when there's none, which still works,
// with GitHub's lower anonymous rate limit.
func githubToken(ctx context.Context, getenv func(string) string, ghAuthToken func(context.Context) string) string {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(getenv(k)); t != "" {
			return t
		}
	}
	return ghAuthToken(ctx)
}

// ghAuthToken asks the GitHub CLI for its token, if it's installed and
// logged in. Anything going wrong just means no token. ctx is the
// command's, so Ctrl-C stops a hung gh too.
func ghAuthToken(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
