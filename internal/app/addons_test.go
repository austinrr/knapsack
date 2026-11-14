package app_test

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/lock"
	"github.com/austinrr/knapsack/internal/paths"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/state"
)

// fakeSource serves releases from memory: each repo is a zip of Forever
// addon folders, downloaded from an httptest server.
type fakeSource struct {
	srv      *httptest.Server
	mu       sync.Mutex
	zips     map[string][]byte // repo ID → archive
	sums     map[string]string // repo ID → published sha256, if set
	resolves atomic.Int32
}

func (f *fakeSource) Name() string { return ref.GitHub }

func (f *fakeSource) Resolve(_ context.Context, id string, _ flavor.Flavor) (source.Release, error) {
	f.resolves.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.zips[id]; !ok {
		return source.Release{}, fmt.Errorf("github %s: %w", id, source.ErrNotFound)
	}
	return source.Release{Version: "v1", DownloadURL: f.srv.URL + "/" + id, SHA256: f.sums[id]}, nil
}

// publish makes repo a release containing the given addon folders.
func (f *fakeSource) publish(t *testing.T, repo string, folders ...string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range folders {
		for file, body := range map[string]string{
			name + "/" + name + ".toc": "## Interface: 16001\n## Title: " + name + "\n## Author: Someone\n",
			name + "/Core.lua":         repo,
		} {
			w, err := zw.Create(file)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.zips[repo] = buf.Bytes()
	sum := sha256.Sum256(buf.Bytes())
	f.sums[repo] = hex.EncodeToString(sum[:])
}

// recorder is a Reporter that keeps every event. It's safe for concurrent
// use, as Reporter requires.
type recorder struct {
	mu     sync.Mutex
	events []app.Event
}

func (r *recorder) Event(e app.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

func (r *recorder) kinds(ref ref.Ref) []app.EventKind {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []app.EventKind
	for _, e := range r.events {
		if e.Ref == ref {
			out = append(out, e.Kind)
		}
	}
	return out
}

// env is an App on a temp KNAPSACK_HOME with a "forever" profile and a fake
// GitHub source.
type env struct {
	app     *app.App
	paths   paths.Paths
	config  *config.Store
	state   *state.Store
	src     *fakeSource
	rec     *recorder
	addons  string
	profile string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := t.TempDir()
	p, err := paths.Resolve(func(string) string { return home })
	if err != nil {
		t.Fatal(err)
	}
	addons := filepath.Join(home, "wow", "_forever_", "Interface", "AddOns")
	if err := os.MkdirAll(addons, 0o700); err != nil {
		t.Fatal(err)
	}
	src := &fakeSource{zips: map[string][]byte{}, sums: map[string]string{}}
	src.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src.mu.Lock()
		data, ok := src.zips[strings.TrimPrefix(r.URL.Path, "/")]
		src.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(src.srv.Close)

	e := &env{
		paths:   p,
		config:  config.NewStore(p.ConfigFile),
		state:   state.NewStore(p.StateFile),
		src:     src,
		rec:     &recorder{},
		addons:  addons,
		profile: "forever",
	}
	e.app = app.New(app.Options{
		Paths:    p,
		Config:   e.config,
		State:    e.state,
		Sources:  source.NewRegistry(src),
		HTTP:     httpx.NewClient("test"),
		Reporter: e.rec,
	})
	if _, err := e.app.SetProfile(context.Background(), app.SetProfileRequest{Name: e.profile, Path: addons}); err != nil {
		t.Fatal(err)
	}
	return e
}

func mustRef(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func (e *env) configAddons(t *testing.T) []ref.Ref {
	t.Helper()
	c, err := e.config.Load()
	if err != nil {
		t.Fatal(err)
	}
	return c.Profiles[e.profile].Addons
}

func (e *env) loadState(t *testing.T) *state.State {
	t.Helper()
	st, err := e.state.Load(e.profile)
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func (e *env) exists(folder string) bool {
	_, err := os.Stat(filepath.Join(e.addons, folder))
	return err == nil
}

func actions(results []app.Result) []app.Action {
	out := make([]app.Action, len(results))
	for i, r := range results {
		out[i] = r.Action
	}
	return out
}

// Add installs, then records in state and config. "" means the current
// profile.
func TestAdd(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details", "Details_DataStorage")
	r := mustRef(t, "github:o/details")

	results, err := e.app.Add(context.Background(), "", []ref.Ref{r, r}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != app.ActionInstalled || results[0].Err != nil {
		t.Fatalf("results = %+v, want one install", results)
	}
	if diff := cmp.Diff([]string{"Details", "Details_DataStorage"}, results[0].Folders); diff != "" {
		t.Errorf("folders (-want +got):\n%s", diff)
	}
	if !e.exists("Details") || !e.exists("Details_DataStorage") {
		t.Error("folders aren't in AddOns")
	}
	got := e.loadState(t).Addons[r]
	if got.Version != "v1" || got.SHA256 != e.src.sums["o/details"] || got.InstalledAt.IsZero() {
		t.Errorf("state = %+v", got)
	}
	if diff := cmp.Diff([]ref.Ref{r}, e.configAddons(t)); diff != "" {
		t.Errorf("config addons (-want +got):\n%s", diff)
	}
	want := []app.EventKind{app.EventResolving, app.EventDownloading, app.EventInstalled}
	if diff := cmp.Diff(want, e.rec.kinds(r)); diff != "" {
		t.Errorf("events (-want +got):\n%s", diff)
	}
	if entries, _ := os.ReadDir(e.paths.CacheDir); len(entries) != 0 {
		t.Errorf("downloads left in the cache: %v", entries)
	}

	// Adding it again changes nothing and downloads nothing.
	before := e.src.resolves.Load()
	results, err = e.app.Add(context.Background(), e.profile, []ref.Ref{r}, false)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionUnchanged}, actions(results)); diff != "" {
		t.Errorf("second add (-want +got):\n%s", diff)
	}
	if e.src.resolves.Load() != before {
		t.Error("second add resolved the release again")
	}
}

// One addon failing doesn't stop the rest, and only the ones that installed
// reach config.
func TestAddPartialFailure(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/good", "Good")
	e.src.publish(t, "o/corrupt", "Corrupt")
	e.src.sums["o/corrupt"] = strings.Repeat("0", 64)
	good, missing, corrupt := mustRef(t, "github:o/good"), mustRef(t, "github:o/missing"), mustRef(t, "github:o/corrupt")

	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{missing, good, corrupt}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []app.Action{app.ActionFailed, app.ActionInstalled, app.ActionFailed}
	if diff := cmp.Diff(want, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if !errors.Is(results[0].Err, source.ErrNotFound) || !errors.Is(results[2].Err, httpx.ErrChecksum) {
		t.Errorf("errors = %v, %v; want not found and checksum", results[0].Err, results[2].Err)
	}
	if diff := cmp.Diff([]ref.Ref{good}, e.configAddons(t)); diff != "" {
		t.Errorf("config addons (-want +got):\n%s", diff)
	}
	if e.exists("Corrupt") {
		t.Error("the corrupt download was installed")
	}
	if kinds := e.rec.kinds(missing); kinds[len(kinds)-1] != app.EventFailed {
		t.Errorf("events for the missing addon = %v, want a final Failed", kinds)
	}
}

// An addon in state but not config, as after an interrupted add, is
// adopted without reinstalling.
func TestAddAdopts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	r := mustRef(t, "github:o/details")
	st := state.New()
	st.Addons[r] = state.Addon{Version: "v0", SHA256: strings.Repeat("ab", 32), InstalledAt: time.Now(), Folders: []string{"Details"}}
	if err := e.state.Save(e.profile, st); err != nil {
		t.Fatal(err)
	}
	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{r}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Action != app.ActionAdopted || results[0].Version != "v0" {
		t.Errorf("results = %+v, want adopted v0", results)
	}
	if e.src.resolves.Load() != 0 {
		t.Error("adopting resolved a release")
	}
	if diff := cmp.Diff([]ref.Ref{r}, e.configAddons(t)); diff != "" {
		t.Errorf("config addons (-want +got):\n%s", diff)
	}
}

// A folder no addon owns needs force; one another addon owns is refused
// even with it.
func TestAddConflicts(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := os.MkdirAll(filepath.Join(e.addons, "Manual"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.src.publish(t, "o/manual", "Manual")
	manual := mustRef(t, "github:o/manual")

	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{manual}, false)
	if err != nil {
		t.Fatal(err)
	}
	var ue *app.UserError
	if !errors.As(results[0].Err, &ue) || !strings.Contains(ue.Hint, "--force") {
		t.Fatalf("result error = %v, want a UserError hinting --force", results[0].Err)
	}
	if results, err = e.app.Add(context.Background(), e.profile, []ref.Ref{manual}, true); err != nil || results[0].Err != nil {
		t.Fatalf("add with force: %v, %+v", err, results)
	}

	e.src.publish(t, "o/bundle", "Bundle", "Manual")
	results, err = e.app.Add(context.Background(), e.profile, []ref.Ref{mustRef(t, "github:o/bundle")}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.As(results[0].Err, &ue) || !strings.Contains(ue.Hint, "another addon") {
		t.Errorf("result error = %v, want a UserError about another addon", results[0].Err)
	}
	if e.exists("Bundle") {
		t.Error("the conflicting addon was partly installed")
	}
}

func TestRemove(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details", "Details_DataStorage")
	e.src.publish(t, "o/other", "Other")
	details, other := mustRef(t, "github:o/details"), mustRef(t, "github:o/other")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{details, other}, false); err != nil {
		t.Fatal(err)
	}

	// By short name, plus a name that matches nothing.
	results, err := e.app.Remove(context.Background(), e.profile, []string{"DETAILS", "nothing"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionRemoved, app.ActionFailed}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if results[1].Name != "nothing" {
		t.Errorf("failed result Name = %q, want the name typed", results[1].Name)
	}
	if e.exists("Details") || e.exists("Details_DataStorage") || !e.exists("Other") {
		t.Error("Remove deleted the wrong folders")
	}
	if diff := cmp.Diff([]ref.Ref{other}, e.configAddons(t)); diff != "" {
		t.Errorf("config addons (-want +got):\n%s", diff)
	}
	if _, ok := e.loadState(t).Addons[details]; ok {
		t.Error("details is still in state")
	}
}

// A short name two addons share is an error listing both.
func TestRemoveAmbiguous(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "a/addon", "A")
	e.src.publish(t, "b/addon", "B")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{mustRef(t, "github:a/addon"), mustRef(t, "github:b/addon")}, false); err != nil {
		t.Fatal(err)
	}
	results, err := e.app.Remove(context.Background(), e.profile, []string{"addon"})
	if err != nil {
		t.Fatal(err)
	}
	if err := results[0].Err; err == nil || !strings.Contains(err.Error(), "github:a/addon, github:b/addon") {
		t.Errorf("error = %v, want both refs listed", err)
	}
	if !e.exists("A") || !e.exists("B") {
		t.Error("an ambiguous remove deleted something")
	}
}

func TestListAndInfo(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/details", "Details")
	installed, missing, orphan := mustRef(t, "github:o/details"), mustRef(t, "github:o/missing"), mustRef(t, "github:o/orphan")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{installed}, false); err != nil {
		t.Fatal(err)
	}
	// missing is in config but was never installed; orphan is the reverse.
	c, _ := e.config.Load()
	p := c.Profiles[e.profile]
	p.Addons = append(p.Addons, missing)
	c.Profiles[e.profile] = p
	if err := e.config.Save(c); err != nil {
		t.Fatal(err)
	}
	st := e.loadState(t)
	st.Addons[orphan] = state.Addon{Version: "v0", SHA256: strings.Repeat("ab", 32), InstalledAt: time.Now(), Folders: []string{"Orphan"}}
	if err := e.state.Save(e.profile, st); err != nil {
		t.Fatal(err)
	}

	list, err := e.app.List(context.Background(), app.Selector{})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, in := range list {
		got = append(got, in.Ref.String()+" "+string(in.Status))
	}
	want := []string{"github:o/details installed", "github:o/missing missing", "github:o/orphan not in config"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("list (-want +got):\n%s", diff)
	}
	if e.src.resolves.Load() != 1 {
		t.Error("List used the source")
	}

	filtered, err := e.app.List(context.Background(), app.Selector{Profiles: []string{e.profile}, Addons: []string{"orphan"}})
	if err != nil || len(filtered) != 1 || filtered[0].Ref != orphan {
		t.Errorf("filtered list = %+v, %v", filtered, err)
	}

	d, err := e.app.Info(context.Background(), e.profile, "details")
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Details" || d.Author != "Someone" || !slices.Equal(d.Interfaces, []int{16001}) || d.Status != app.StatusInstalled {
		t.Errorf("info = %+v", d)
	}
	if _, err := e.app.Info(context.Background(), e.profile, "nope"); err == nil {
		t.Error("Info of an unknown addon succeeded")
	}
}

// A second process holding the profile's lock makes every use case fail
// fast with a UserError.
func TestLockContention(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	l, err := lock.Acquire(e.paths.ProfileLock(e.profile))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Release() }()

	_, addErr := e.app.Add(context.Background(), e.profile, []ref.Ref{mustRef(t, "github:o/x")}, false)
	_, removeErr := e.app.Remove(context.Background(), e.profile, []string{"x"})
	_, listErr := e.app.List(context.Background(), app.Selector{})
	for _, err := range []error{addErr, removeErr, listErr} {
		var ue *app.UserError
		if !errors.As(err, &ue) || !strings.Contains(ue.Msg, "in use by another knapsack process") {
			t.Errorf("error = %v, want the profile-in-use UserError", err)
		}
	}
}

func TestUnknownProfile(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	for _, name := range []string{"nope", "../escape"} {
		var ue *app.UserError
		if _, err := e.app.List(context.Background(), app.Selector{Profiles: []string{name}}); !errors.As(err, &ue) {
			t.Errorf("List(%q) error = %v, want a UserError", name, err)
		}
	}
}

// Results come back in the order the refs were given, whatever happened
// to each.
func TestAddResultOrder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/old", "Old")
	e.src.publish(t, "o/new", "New")
	old, fresh := mustRef(t, "github:o/old"), mustRef(t, "github:o/new")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{old}, false); err != nil {
		t.Fatal(err)
	}
	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{fresh, old}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 || results[0].Ref != fresh || results[0].Action != app.ActionInstalled || results[1].Ref != old || results[1].Action != app.ActionUnchanged {
		t.Errorf("results = %+v, want new installed then old unchanged", results)
	}
}

// A source with no API key gets a hint naming its own config key and
// environment variable.
func TestAddSourceNotConfigured(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{mustRef(t, "curseforge:12345")}, false)
	if err != nil {
		t.Fatal(err)
	}
	var ue *app.UserError
	if !errors.As(results[0].Err, &ue) || !strings.Contains(ue.Hint, "sources.curseforge.api_key") || !strings.Contains(ue.Hint, "KNAPSACK_CURSEFORGE_API_KEY") {
		t.Errorf("error = %v, want a hint naming curseforge's key", results[0].Err)
	}
}

// If saving state fails for one addon, it's dropped from memory too, so
// the next addon in the same command still saves. On Linux and macOS, a
// folder named CON extracts and installs, but state refuses it.
func TestAddStateSaveFailureIsolated(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("Windows can't extract a folder named CON")
	}
	e := newEnv(t)
	e.src.publish(t, "o/bad", "CON")
	e.src.publish(t, "o/good", "Good")
	bad, good := mustRef(t, "github:o/bad"), mustRef(t, "github:o/good")
	results, err := e.app.Add(context.Background(), e.profile, []ref.Ref{bad, good}, false)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionFailed, app.ActionInstalled}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	st := e.loadState(t)
	if _, ok := st.Addons[bad]; ok {
		t.Error("the addon whose save failed is in state")
	}
	if _, ok := st.Addons[good]; !ok {
		t.Error("the next addon wasn't saved")
	}
}

// Remove stops at Ctrl-C before touching the next addon, and what it
// already removed still leaves config.
func TestRemoveCancelled(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/a", "A")
	e.src.publish(t, "o/b", "B")
	a, b := mustRef(t, "github:o/a"), mustRef(t, "github:o/b")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{a, b}, false); err != nil {
		t.Fatal(err)
	}
	// Cancel as soon as the first addon is reported removed.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.app = app.New(app.Options{Paths: e.paths, Config: e.config, State: e.state, Reporter: cancelOnFirst{cancel}})
	results, err := e.app.Remove(ctx, e.profile, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]app.Action{app.ActionRemoved, app.ActionFailed}, actions(results)); diff != "" {
		t.Fatalf("actions (-want +got):\n%s", diff)
	}
	if !errors.Is(results[1].Err, context.Canceled) {
		t.Errorf("second result error = %v, want context.Canceled", results[1].Err)
	}
	if e.exists("A") || !e.exists("B") {
		t.Error("want A removed and B left alone")
	}
	if diff := cmp.Diff([]ref.Ref{b}, e.configAddons(t)); diff != "" {
		t.Errorf("config addons (-want +got):\n%s", diff)
	}
}

type cancelOnFirst struct{ cancel context.CancelFunc }

func (c cancelOnFirst) Event(app.Event) { c.cancel() }

// A name that matches nothing leaves Ref zero, even when it parses as a
// ref, so a frontend never shows an addon that isn't there.
func TestRemoveUnknownRef(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	results, err := e.app.Remove(context.Background(), e.profile, []string{"github:a/b"})
	if err != nil {
		t.Fatal(err)
	}
	if results[0].Ref != (ref.Ref{}) || results[0].Name != "github:a/b" || results[0].Err == nil {
		t.Errorf("result = %+v, want a zero Ref with the name and an error", results[0])
	}
}

// List and Info only read, so they work when the AddOns folder is gone.
func TestListWithoutAddOnsFolder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	if err := os.RemoveAll(e.addons); err != nil {
		t.Fatal(err)
	}
	if _, err := e.app.List(context.Background(), app.Selector{}); err != nil {
		t.Errorf("List without AddOns: %v", err)
	}
}

// Info reads the addon's own folder, not a bundled library sorted first.
func TestInfoMainFolder(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.src.publish(t, "o/bugsack", "!BugGrabber", "BugSack")
	if _, err := e.app.Add(context.Background(), e.profile, []ref.Ref{mustRef(t, "github:o/bugsack")}, false); err != nil {
		t.Fatal(err)
	}
	d, err := e.app.Info(context.Background(), e.profile, "bugsack")
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "BugSack" {
		t.Errorf("Title = %q, want BugSack", d.Title)
	}
}
