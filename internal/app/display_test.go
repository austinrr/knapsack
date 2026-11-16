package app_test

import (
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/ref"
)

// Names shown twice in one profile get their ref's ID; the same name in
// another profile, or an unmanaged folder with no ref, doesn't.
func TestUniqueNames(t *testing.T) {
	t.Parallel()
	a := ref.Ref{Source: ref.GitHub, ID: "a/details"}
	b := ref.Ref{Source: ref.GitHub, ID: "b/details-mirror"}
	c := ref.Ref{Source: ref.GitHub, ID: "c/baganator"}
	got := app.UniqueNames([]app.NameRow{
		{Profile: "forever", Name: "Details!", Ref: a},
		{Profile: "forever", Name: "details!", Ref: b},
		{Profile: "forever", Name: "Baganator", Ref: c},
		{Profile: "retail", Name: "Baganator", Ref: c},
		{Profile: "forever", Name: "Loose"},
	})
	want := []string{"Details! (a/details)", "details! (b/details-mirror)", "Baganator", "Baganator", "Loose"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("UniqueNames (-want +got):\n%s", diff)
	}
}

func TestCount(t *testing.T) {
	t.Parallel()
	for n, want := range map[int]string{0: "0 addons", 1: "1 addon", 2: "2 addons"} {
		if got := app.Count(n, "addon"); got != want {
			t.Errorf("Count(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestFormatProgress(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		done, total int64
		want        string
	}{
		{512, 2048, "512 B of 2.0 KiB"},
		{1258291, 4718592, "1.2 MiB of 4.5 MiB"},
		{1258291, -1, "1.2 MiB"},
	} {
		if got := app.FormatProgress(tt.done, tt.total); got != tt.want {
			t.Errorf("FormatProgress(%d, %d) = %q, want %q", tt.done, tt.total, got, tt.want)
		}
	}
}

// A source's status and key are said as sources list says them.
func TestSourceDisplay(t *testing.T) {
	t.Parallel()
	github := app.SourceInfo{Name: ref.GitHub, API: config.APIOfficial}
	noKey := app.SourceInfo{Name: ref.CurseForge, NeedsKey: true, Key: app.KeyNone, Env: "KNAPSACK_CURSEFORGE_API_KEY", API: config.APIOfficial}
	website := noKey
	website.API = config.APIWebsite
	inConfig := noKey
	inConfig.Key = app.KeyConfig
	inEnv := noKey
	inEnv.Key = app.KeyEnv
	for _, tt := range []struct {
		name         string
		s            app.SourceInfo
		github       string
		status, desc string
	}{
		{"github with gh", github, "gh", "ready", "from gh auth token"},
		{"github with GITHUB_TOKEN", github, "GITHUB_TOKEN", "ready", "from $GITHUB_TOKEN"},
		{"github with none", github, "", "ready", "none (60 requests an hour)"},
		{"no key", noKey, "", "needs key", ""},
		{"no key, website", website, "", "ready", ""},
		{"key in config", inConfig, "", "ready", "set in config"},
		{"key in env", inEnv, "", "ready", "from $KNAPSACK_CURSEFORGE_API_KEY"},
	} {
		if got := tt.s.Status(); got != tt.status {
			t.Errorf("%s: Status = %q, want %q", tt.name, got, tt.status)
		}
		if got := app.DescribeKey(tt.s, app.KeyFrom(tt.s, tt.github)); got != tt.desc {
			t.Errorf("%s: DescribeKey = %q, want %q", tt.name, got, tt.desc)
		}
	}
}
