package main

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/source/github"
)

// lazyGitHub is the GitHub source, built on first use. Finding the token
// may run the GitHub CLI, so it waits until a request is really made: a
// command that turns out not to need GitHub, like the update check
// within its daily interval, stays instant. The token is looked up with
// the first request's context, so Ctrl-C stops a hung gh too.
type lazyGitHub struct {
	client      *http.Client
	baseURL     string
	getenv      func(string) string
	ghAuthToken func(context.Context) string

	once sync.Once
	src  *github.Source
}

func (l *lazyGitHub) source(ctx context.Context) *github.Source {
	l.once.Do(func() {
		l.src = github.New(l.client, l.baseURL, githubToken(ctx, l.getenv, l.ghAuthToken))
	})
	return l.src
}

// Name implements source.Source.
func (l *lazyGitHub) Name() string { return ref.GitHub }

// Resolve implements source.Source.
func (l *lazyGitHub) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	return l.source(ctx).Resolve(ctx, id, f)
}

// LatestAssets implements app.Releases.
func (l *lazyGitHub) LatestAssets(ctx context.Context, id string) (string, []source.Asset, error) {
	return l.source(ctx).LatestAssets(ctx, id)
}

// githubToken finds a GitHub token the way the LemonUp fork did: GITHUB_TOKEN,
// then GH_TOKEN, then the GitHub CLI's login. Knapsack never stores it
// (DESIGN.md §5.2). It returns "" when there's none, which still works,
// with GitHub's lower anonymous rate limit.
func githubToken(ctx context.Context, getenv func(string) string, ghAuthToken func(context.Context) string) string {
	token, _ := findGitHubToken(ctx, getenv, ghAuthToken)
	return token
}

// findGitHubToken is githubToken, also saying where the token came from:
// the variable's name, "gh" for the GitHub CLI, or "" for none. It's the
// one place the order lives, so `knapsack sources list` reports what commands
// use.
func findGitHubToken(ctx context.Context, getenv func(string) string, ghAuthToken func(context.Context) string) (token, origin string) {
	for _, k := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if t := strings.TrimSpace(getenv(k)); t != "" {
			return t, k
		}
	}
	if t := ghAuthToken(ctx); t != "" {
		return t, "gh"
	}
	return "", ""
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
