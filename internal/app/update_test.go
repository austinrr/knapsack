package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// setConfigAddons replaces the profile's addons in config, as a hand edit
// would, without touching state or AddOns.
func (e *env) setConfigAddons(t *testing.T, profile string, refs ...ref.Ref) {
	t.Helper()
	c, err := e.config.Load()
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles[profile]
	p.Addons = refs
	c.Profiles[profile] = p
	if err := e.config.Save(c); err != nil {
		t.Fatal(err)
	}
}

// addProfile creates another Forever profile with its own AddOns folder.
func (e *env) addProfile(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(e.paths.ConfigFile), "..", name, "_forever_", "Interface", "AddOns")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.SetProfile(context.Background(), app.SetProfileRequest{Name: name, Path: dir}); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *env) mustAdd(t *testing.T, refs ...ref.Ref) {
	t.Helper()
	results, err := e.app.Add(context.Background(), e.profile, refs, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, res := range results {
		if res.Err != nil {
			t.Fatalf("add %s: %v", res.Ref, res.Err)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// An addon in config but not installed, as on a new machine, is installed.
func TestUpdateInstallsMissing(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.setConfigAddons(t, e.profile, r)

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionInstalled}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if results[0].Profile != e.profile || !e.exists("Details") {
		t.Errorf("result = %+v; Details installed = %v", results[0], e.exists("Details"))
	}
	if got := e.loadState(t).Addons[r].SHA256; got != e.src.sums["o/details"] {
		t.Errorf("state sha256 = %q, want the release's", got)
	}
}

// A new release replaces the installed one, and state records it.
func TestUpdateUpgrades(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.mustAdd(t, r)
	e.src.release(t, "o/details", "v2", "Details")

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != app.ActionUpgraded || results[0].Version != "v2" {
		t.Fatalf("results = %+v, want one upgrade to v2", results)
	}
	if got := readFile(t, filepath.Join(e.addons, "Details", "Core.lua")); got != "o/details v2" {
		t.Errorf("Core.lua = %q, want v2's", got)
	}
	got := e.loadState(t).Addons[r]
	if got.Version != "v2" || got.SHA256 != e.src.sums["o/details"] {
		t.Errorf("state = %+v, want v2", got)
	}
}

// When the source publishes a SHA-256 that matches, nothing is downloaded
// or installed.
func TestUpdateUpToDate(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.mustAdd(t, r)
	before := e.loadState(t).Addons[r]
	e.rec.events = nil

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionUnchanged}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"Details"}, results[0].Folders); diff != "" {
		t.Errorf("folders (-want +got):\n%s", diff)
	}
	want := []app.EventKind{app.EventResolving, app.EventSkipped}
	if diff := cmp.Diff(want, e.rec.kinds(r)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if after := e.loadState(t).Addons[r]; !after.InstalledAt.Equal(before.InstalledAt) {
		t.Error("state was rewritten for an up-to-date addon")
	}
}

// Without a published SHA-256, the release is downloaded to compare, but
// not reinstalled when it matches.
func TestUpdateUpToDateWithoutPublishedSum(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.mustAdd(t, r)
	before := e.loadState(t).Addons[r]
	delete(e.src.sums, "o/details")
	e.rec.events = nil

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionUnchanged}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	want := []app.EventKind{app.EventResolving, app.EventDownloading, app.EventSkipped}
	if diff := cmp.Diff(want, e.rec.kinds(r)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if after := e.loadState(t).Addons[r]; !after.InstalledAt.Equal(before.InstalledAt) {
		t.Error("an identical release was reinstalled")
	}
	if entries, _ := os.ReadDir(e.paths.CacheDir); len(entries) != 0 {
		t.Errorf("downloads left in the cache: %v", entries)
	}
}

// An addon in state but not config is left alone without --prune, and
// uninstalled with it.
func TestUpdatePrune(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/keep", "Keep")
	e.src.publish(t, "o/old", "Old")
	keep, old := mustRef(t, "github:o/keep"), mustRef(t, "github:o/old")
	e.mustAdd(t, keep, old)
	e.setConfigAddons(t, e.profile, keep)

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Ref != keep {
		t.Fatalf("results without --prune = %+v, want only keep", results)
	}
	if !e.exists("Old") {
		t.Fatal("update without --prune removed an addon")
	}

	// Named, it's an error that says how to resolve it.
	results, err = e.app.Update(context.Background(), app.Selector{Addons: []string{"old"}}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var ue *app.UserError
	if len(results) != 1 || !errors.As(results[0].Err, &ue) || results[0].Name != "old" {
		t.Fatalf("named without --prune = %+v, want a UserError", results)
	}

	results, err = e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{Prune: true})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionUnchanged, app.ActionRemoved}, actions(results)); diff != "" {
		t.Fatalf("actions with --prune (-want +got):\n%s", diff)
	}
	if e.exists("Old") || !e.exists("Keep") {
		t.Error("--prune removed the wrong folders")
	}
	if _, ok := e.loadState(t).Addons[old]; ok {
		t.Error("pruned addon is still in state")
	}
}

// One addon failing doesn't stop the rest.
func TestUpdatePartialFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/gone", "Gone")
	e.src.publish(t, "o/details", "Details")
	gone, details := mustRef(t, "github:o/gone"), mustRef(t, "github:o/details")
	e.mustAdd(t, gone, details)
	delete(e.src.zips, "o/gone")
	e.src.release(t, "o/details", "v2", "Details")

	results, err := e.app.Update(context.Background(), app.Selector{}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionFailed, app.ActionUpgraded}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if !errors.Is(results[0].Err, source.ErrNotFound) {
		t.Errorf("error = %v, want not found", results[0].Err)
	}
	if !e.exists("Gone") {
		t.Error("a failed update removed the installed addon")
	}
}

// Named addons are updated in the order given, once each; others are left
// alone.
func TestUpdateNamed(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, repo := range []string{"o/a", "o/b", "o/c"} {
		e.src.publish(t, repo, filepath.Base(repo))
	}
	a, b, c := mustRef(t, "github:o/a"), mustRef(t, "github:o/b"), mustRef(t, "github:o/c")
	e.mustAdd(t, a, b, c)
	for _, repo := range []string{"o/a", "o/b", "o/c"} {
		e.src.release(t, repo, "v2", filepath.Base(repo))
	}

	results, err := e.app.Update(context.Background(), app.Selector{Addons: []string{"c", "github:o/a", "c", "nope"}}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(results))
	for i, res := range results {
		got[i] = res.Name + " " + string(res.Action)
	}
	want := []string{"c upgraded", "github:o/a upgraded", "nope failed"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("results (-want +got):\n%s", diff)
	}
	if v := e.loadState(t).Addons[b].Version; v != "v1" {
		t.Errorf("unnamed addon b is at %s, want v1", v)
	}
}

// Several profiles are each updated in turn, and results are grouped by
// profile in the order given.
func TestUpdateSeveralProfiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.mustAdd(t, r)
	other := e.addProfile(t, "second")
	e.setConfigAddons(t, "second", r)
	e.src.release(t, "o/details", "v2", "Details")

	results, err := e.app.Update(context.Background(), app.Selector{Profiles: []string{e.profile, "second"}}, app.UpdateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(results))
	for i, res := range results {
		got[i] = res.Profile + " " + string(res.Action)
	}
	want := []string{"forever upgraded", "second installed"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("results (-want +got):\n%s", diff)
	}
	if _, err := os.Stat(filepath.Join(other, "Details")); err != nil {
		t.Errorf("Details not installed in the second profile: %v", err)
	}
}

// An unknown profile stops the run, but what was done before it is
// returned.
func TestUpdateStopsAtFailedProfile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	e.setConfigAddons(t, e.profile, mustRef(t, "github:o/details"))

	results, err := e.app.Update(context.Background(), app.Selector{Profiles: []string{e.profile, "nope"}}, app.UpdateOptions{})
	var ue *app.UserError
	if !errors.As(err, &ue) {
		t.Fatalf("error = %v, want a UserError for the unknown profile", err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionInstalled}, actions(results)); diff != "" {
		t.Errorf("results so far (-want +got):\n%s", diff)
	}
}

// Check reports what update would do, and changes nothing.
func TestCheck(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, repo := range []string{"o/current", "o/outdated", "o/unpublished", "o/gone", "o/old", "o/missing"} {
		e.src.publish(t, repo, filepath.Base(repo))
	}
	current, outdated, unpublished := mustRef(t, "github:o/current"), mustRef(t, "github:o/outdated"), mustRef(t, "github:o/unpublished")
	gone, old, missing := mustRef(t, "github:o/gone"), mustRef(t, "github:o/old"), mustRef(t, "github:o/missing")
	e.mustAdd(t, current, outdated, unpublished, gone, old)
	e.setConfigAddons(t, e.profile, current, outdated, unpublished, gone, missing)
	e.src.release(t, "o/outdated", "v2", "outdated")
	e.src.release(t, "o/unpublished", "v2", "unpublished")
	delete(e.src.sums, "o/unpublished") // only a download can tell
	delete(e.src.zips, "o/gone")
	stateBefore := e.loadState(t)
	e.rec.events = nil

	plans, err := e.app.Check(context.Background(), app.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	type row struct {
		Ref                  string
		Action               app.PlanAction
		Installed, Available string
	}
	got := make([]row, len(plans))
	for i, p := range plans {
		got[i] = row{p.Ref.String(), p.Action, p.Installed, p.Available}
	}
	want := []row{
		{"github:o/current", app.PlanNone, "v1", "v1"},
		{"github:o/outdated", app.PlanUpgrade, "v1", "v2"},
		{"github:o/unpublished", app.PlanUpgrade, "v1", "v2"},
		{"github:o/gone", app.PlanFailed, "v1", ""},
		{"github:o/missing", app.PlanInstall, "", "v1"},
		{"github:o/old", app.PlanPrune, "v1", ""},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("plans (-want +got):\n%s", diff)
	}
	if !errors.Is(plans[3].Err, source.ErrNotFound) {
		t.Errorf("gone's error = %v, want not found", plans[3].Err)
	}

	// Only the addon without a published SHA-256 was downloaded.
	for _, r := range []ref.Ref{current, outdated, unpublished, missing} {
		downloaded := slices.Contains(e.rec.kinds(r), app.EventDownloading)
		if downloaded != (r == unpublished) {
			t.Errorf("%s downloaded = %v", r, downloaded)
		}
	}
	if diff := cmp.Diff(stateBefore, e.loadState(t)); diff != "" {
		t.Errorf("check changed state (-before +after):\n%s", diff)
	}
	if e.exists("missing") || readFile(t, filepath.Join(e.addons, "outdated", "Core.lua")) != "o/outdated v1" {
		t.Error("check changed AddOns")
	}
	if entries, _ := os.ReadDir(e.paths.CacheDir); len(entries) != 0 {
		t.Errorf("downloads left in the cache: %v", entries)
	}
}

// Check works on several profiles, like update.
func TestCheckSeveralProfiles(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	r := mustRef(t, "github:o/details")
	e.mustAdd(t, r)
	e.addProfile(t, "second")
	e.setConfigAddons(t, "second", r)

	plans, err := e.app.Check(context.Background(), app.Selector{Profiles: []string{e.profile, "second"}})
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plans))
	for i, p := range plans {
		got[i] = p.Profile + " " + string(p.Action)
	}
	if diff := cmp.Diff([]string{"forever none", "second install"}, got); diff != "" {
		t.Errorf("plans (-want +got):\n%s", diff)
	}
}
