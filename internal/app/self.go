package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"time"

	"github.com/austinrr/knapsack/internal/atomicfile"
	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/selfupdate"
	"github.com/austinrr/knapsack/internal/source"
)

// SelfRepo is the GitHub repository Knapsack's own releases come from.
const SelfRepo = "austinrr/knapsack"

// goInstallCmd updates a binary built with go install.
const goInstallCmd = "go install github.com/austinrr/knapsack/cmd/knapsack@latest"

// Releases finds a GitHub repository's newest release with its assets.
// source/github's Source implements it.
type Releases interface {
	LatestAssets(ctx context.Context, id string) (string, []source.Asset, error)
}

// Build describes the running binary.
type Build struct {
	// Version is what `knapsack version` prints: v1.2.3, or "dev".
	Version string
	// Released is true for a release archive's binary, whose version was
	// set when it was built. A go install build has a version from Go's
	// module data but isn't Released.
	Released bool
	// Executable is the binary's path, with symlinks resolved.
	Executable string
}

// How a binary is updated.
const (
	viaSelfUpdate = iota
	viaGoInstall
	viaPackageManager
	notUpdatable // a dev build
)

// method works out how b is updated, and for a package manager, which.
func (b Build) method() (int, string) {
	switch {
	case b.Released:
		if pm := selfupdate.PackageManager(b.Executable); pm != "" {
			return viaPackageManager, pm
		}
		return viaSelfUpdate, ""
	case b.Version != "dev" && b.Version != "" && !localBuild.MatchString(b.Version):
		return viaGoInstall, ""
	}
	return notUpdatable, ""
}

// localBuild matches the versions Go stamps on a build from a checkout
// rather than a tag: a pseudo-version (v0.1.1-0.20261114183000-abcdef123456)
// or one with uncommitted changes (+dirty). go install @latest would
// replace work in progress, so these count as dev builds.
var localBuild = regexp.MustCompile(`\d{14}-[0-9a-f]{12}|\+dirty$`)

// UpdateHint says how to update b, for the notice of a newer release:
// "run `knapsack self-update`", or the go install command, or the package
// manager. It's "" for a dev build.
func UpdateHint(b Build) string {
	switch m, pm := b.method(); m {
	case viaSelfUpdate:
		return "run `knapsack self-update`"
	case viaGoInstall:
		return "run `" + goInstallCmd + "`"
	case viaPackageManager:
		return "update it with " + pm
	}
	return ""
}

// Latest is the newest Knapsack release, as LatestVersion found it.
type Latest struct {
	Version string
	// Newer is whether it's newer than the running binary.
	Newer bool
	// Cached is true when it came from an earlier check, without asking
	// GitHub.
	Cached bool
}

// latestCheck is the cache file: the last time GitHub was asked, and what
// it said. A failed check is recorded too, with no version, so being
// offline doesn't mean asking on every command.
type latestCheck struct {
	CheckedAt time.Time `json:"checked_at"`
	Version   string    `json:"version,omitempty"`
}

func (a *App) latestCheckFile() string {
	return filepath.Join(a.paths.CacheDir, "latest-release.json")
}

// LatestVersion finds the newest Knapsack release (DESIGN.md §8.4). If the
// last check is younger than maxAge, its answer is used without the
// network; zero always asks GitHub. A dev build is never compared, so
// Newer is false.
func (a *App) LatestVersion(ctx context.Context, b Build, maxAge time.Duration) (Latest, error) {
	if maxAge > 0 {
		if c, err := a.readLatestCheck(); err == nil && time.Since(c.CheckedAt) < maxAge && time.Since(c.CheckedAt) >= 0 {
			return Latest{Version: c.Version, Newer: selfupdate.Newer(c.Version, b.Version), Cached: true}, nil
		}
	}
	version, _, err := a.releases.LatestAssets(ctx, SelfRepo)
	// A check the user cancelled says nothing about GitHub, so it isn't
	// recorded. One that ran out of time is: a network that drops requests
	// would otherwise cost every command the full timeout.
	if !errors.Is(ctx.Err(), context.Canceled) {
		// The cache is a convenience; failing to write it changes nothing
		// the user asked for.
		_ = a.writeLatestCheck(latestCheck{CheckedAt: time.Now().UTC(), Version: version})
	}
	if err != nil {
		return Latest{}, err
	}
	return Latest{Version: version, Newer: selfupdate.Newer(version, b.Version)}, nil
}

func (a *App) readLatestCheck() (latestCheck, error) {
	data, err := os.ReadFile(a.latestCheckFile())
	if err != nil {
		return latestCheck{}, err
	}
	var c latestCheck
	err = json.Unmarshal(data, &c)
	return c, err
}

func (a *App) writeLatestCheck(c latestCheck) error {
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(a.paths.CacheDir, 0o700); err != nil {
		return err
	}
	return atomicfile.Write(a.latestCheckFile(), data, 0o600)
}

// SelfUpdated is the result of SelfUpdate.
type SelfUpdated struct {
	From, To string
	// Updated is false when From was already the newest release.
	Updated bool
}

// SelfUpdate replaces the running binary with the newest release's, after
// verifying the archive's SHA-256 (DESIGN.md §8.4). It refuses for a
// binary it shouldn't replace: a dev build, a go install build, or one a
// package manager installed, saying how to update those instead.
func (a *App) SelfUpdate(ctx context.Context, b Build) (SelfUpdated, error) {
	switch m, pm := b.method(); m {
	case notUpdatable:
		return SelfUpdated{}, &UserError{Msg: "this is a development build of knapsack", Hint: "build it again from source, or install a release"}
	case viaGoInstall:
		return SelfUpdated{}, &UserError{Msg: "knapsack was installed with go install", Hint: "run `" + goInstallCmd + "`"}
	case viaPackageManager:
		return SelfUpdated{}, &UserError{Msg: "knapsack was installed by " + pm, Hint: "update it with " + pm}
	}

	version, assets, err := a.releases.LatestAssets(ctx, SelfRepo)
	if err != nil {
		err = fmt.Errorf("find the newest knapsack release: %w", err)
		if errors.Is(err, source.ErrNotFound) {
			return SelfUpdated{}, err // explain's hint is about addon refs
		}
		return SelfUpdated{}, explain(err, ref.Ref{Source: ref.GitHub, ID: SelfRepo})
	}
	res := SelfUpdated{From: b.Version, To: version}
	if !selfupdate.Newer(version, b.Version) {
		return res, nil
	}
	name := selfupdate.AssetName(version, runtime.GOOS, runtime.GOARCH)
	var asset source.Asset
	for _, as := range assets {
		if as.Name == name {
			asset = as
		}
	}
	switch {
	case asset.Name == "":
		return res, &UserError{Msg: fmt.Sprintf("knapsack %s has no build for %s/%s", version, runtime.GOOS, runtime.GOARCH),
			Hint: "build it from source with `" + goInstallCmd + "`"}
	case asset.SHA256 == "":
		// Every release has digests; one without can't be verified.
		return res, fmt.Errorf("knapsack %s: GitHub has no checksum for %s, so it can't be verified", version, name)
	}

	path, _, err := httpx.Download(ctx, a.http, asset.URL, nil, a.paths.CacheDir, asset.SHA256, nil)
	if err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(path) }() // a download; the cache is safe to clear
	if err := selfupdate.Replace(b.Executable, path); err != nil {
		if errors.Is(err, selfupdate.ErrOldInUse) {
			return res, &UserError{Msg: "can't replace " + b.Executable, Hint: "another knapsack is still running; close it and retry", Err: err}
		}
		if errors.Is(err, fs.ErrPermission) {
			return res, &UserError{Msg: "can't replace " + b.Executable,
				Hint: "you don't have permission to write there; rerun as the user that installed knapsack", Err: err}
		}
		return res, fmt.Errorf("replace %s: %w", b.Executable, err)
	}
	res.Updated = true
	return res, nil
}
