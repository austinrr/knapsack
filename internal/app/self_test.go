package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/paths"
	"github.com/austinrr/knapsack/internal/source"
)

// fakeReleases is knapsack's own releases: version, or err, and a count of
// how often it was asked.
type fakeReleases struct {
	version string
	err     error
	asked   atomic.Int32
}

func (f *fakeReleases) LatestAssets(context.Context, string) (string, []source.Asset, error) {
	f.asked.Add(1)
	return f.version, nil, f.err
}

func newSelfApp(t *testing.T, rel *fakeReleases) (*app.App, paths.Paths) {
	t.Helper()
	home := t.TempDir()
	p, err := paths.Resolve(func(string) string { return home })
	if err != nil {
		t.Fatal(err)
	}
	return app.New(app.Options{Paths: p, Releases: rel}), p
}

var released = app.Build{Version: "v0.1.0", Released: true, Executable: "/home/me/.local/bin/knapsack"}

func TestLatestVersionCaches(t *testing.T) {
	t.Parallel()
	rel := &fakeReleases{version: "v0.2.0"}
	a, _ := newSelfApp(t, rel)
	ctx := context.Background()

	got, err := a.LatestVersion(ctx, released, time.Hour)
	if err != nil || got != (app.Latest{Version: "v0.2.0", Newer: true}) {
		t.Fatalf("first check = %+v, %v", got, err)
	}
	// Within maxAge, the answer comes from the cache.
	got, err = a.LatestVersion(ctx, released, time.Hour)
	if err != nil || got != (app.Latest{Version: "v0.2.0", Newer: true, Cached: true}) || rel.asked.Load() != 1 {
		t.Errorf("second check = %+v, %v after %d requests; want it cached", got, err, rel.asked.Load())
	}
	// Zero maxAge always asks.
	if _, err := a.LatestVersion(ctx, released, 0); err != nil || rel.asked.Load() != 2 {
		t.Errorf("uncached check = %v after %d requests; want 2", err, rel.asked.Load())
	}
	// The cached version is compared with the running one each time.
	got, _ = a.LatestVersion(ctx, app.Build{Version: "v0.2.0", Released: true}, time.Hour)
	if got.Newer {
		t.Errorf("v0.2.0 is newer than itself: %+v", got)
	}
}

// Being offline is recorded too, so commands don't each wait for a check
// that fails, and so is running out of time, as on a network that drops
// requests. A check the user cancelled isn't.
func TestLatestVersionRecordsFailure(t *testing.T) {
	t.Parallel()
	for name, failed := range map[string]func() (context.Context, context.CancelFunc){
		"offline":   func() (context.Context, context.CancelFunc) { return context.Background(), func() {} },
		"timed out": func() (context.Context, context.CancelFunc) { return context.WithTimeout(context.Background(), 0) },
	} {
		rel := &fakeReleases{err: errors.New("offline")}
		a, _ := newSelfApp(t, rel)

		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := a.LatestVersion(cancelled, released, time.Hour); err == nil {
			t.Fatal("check succeeded")
		}
		ctx, done := failed()
		if _, err := a.LatestVersion(ctx, released, time.Hour); err == nil {
			t.Fatal("check succeeded")
		}
		done()
		if rel.asked.Load() != 2 {
			t.Errorf("%s: asked %d times, want 2: a cancelled check isn't recorded", name, rel.asked.Load())
		}
		got, err := a.LatestVersion(context.Background(), released, time.Hour)
		if err != nil || got.Newer || !got.Cached || rel.asked.Load() != 2 {
			t.Errorf("%s: after a failed check = %+v, %v after %d requests; want a cached nothing", name, got, err, rel.asked.Load())
		}
	}
}

// A corrupt or future-dated cache is ignored.
func TestLatestVersionBadCache(t *testing.T) {
	t.Parallel()
	for name, contents := range map[string]string{
		"corrupt": "{",
		"future":  `{"checked_at":"` + time.Now().Add(48*time.Hour).UTC().Format(time.RFC3339) + `","version":"v9.0.0"}`,
	} {
		rel := &fakeReleases{version: "v0.2.0"}
		a, p := newSelfApp(t, rel)
		if err := os.MkdirAll(p.CacheDir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p.CacheDir, "latest-release.json"), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := a.LatestVersion(context.Background(), released, time.Hour)
		if err != nil || got.Version != "v0.2.0" || got.Cached {
			t.Errorf("%s cache: %+v, %v; want a fresh check", name, got, err)
		}
	}
}

func TestSelfUpdateRefuses(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		build app.Build
		hint  string
	}{
		"dev":        {app.Build{Version: "dev"}, "build it again from source, or install a release"},
		"go install": {app.Build{Version: "v0.1.0"}, "run `go install github.com/austinrr/knapsack/cmd/knapsack@latest`"},
		"homebrew":   {app.Build{Version: "v0.1.0", Released: true, Executable: "/opt/homebrew/bin/knapsack"}, "update it with Homebrew"},
	} {
		rel := &fakeReleases{version: "v0.2.0"}
		a, _ := newSelfApp(t, rel)
		_, err := a.SelfUpdate(context.Background(), tc.build)
		var ue *app.UserError
		if !errors.As(err, &ue) || ue.Hint != tc.hint || rel.asked.Load() != 0 {
			t.Errorf("%s: SelfUpdate = %v (hint %q) after %d requests; want hint %q and no request", name, err, hintOf(ue), rel.asked.Load(), tc.hint)
		}
		if got := app.UpdateHint(tc.build); name != "dev" && got != tc.hint {
			t.Errorf("%s: UpdateHint = %q, want %q", name, got, tc.hint)
		}
	}
	// Builds from a checkout are dev builds too, whatever version Go
	// stamps on them: go install @latest would replace work in progress.
	for _, v := range []string{"dev", "", "v0.1.1-0.20261114183000-abcdef123456", "v0.1.0+dirty", "v0.1.1-0.20261114183000-abcdef123456+dirty"} {
		if got := app.UpdateHint(app.Build{Version: v}); got != "" {
			t.Errorf("UpdateHint for a %q build = %q, want none", v, got)
		}
	}
}

func hintOf(ue *app.UserError) string {
	if ue == nil {
		return ""
	}
	return ue.Hint
}

func TestSelfUpdateUpToDate(t *testing.T) {
	t.Parallel()
	a, _ := newSelfApp(t, &fakeReleases{version: "v0.1.0"})
	got, err := a.SelfUpdate(context.Background(), released)
	if err != nil || got != (app.SelfUpdated{From: "v0.1.0", To: "v0.1.0"}) {
		t.Errorf("SelfUpdate = %+v, %v; want up to date", got, err)
	}
}
