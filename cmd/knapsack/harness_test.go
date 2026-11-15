package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/paths"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files in testdata/")

// result is everything observable about one CLI invocation.
type result struct {
	Stdout string
	Stderr string
	Code   int
}

// testVersion is the version every golden test runs with, so golden files
// don't depend on how the test binary was built.
const testVersion = "v0.0.0-test"

// sandbox is an isolated Knapsack home with fake game folders, so tests
// never touch the real config. In arguments, {home} expands to its path; in
// output, the path is replaced by <home>.
type sandbox struct {
	t      *testing.T
	home   string
	env    map[string]string // extra environment variables
	github *httptest.Server  // fake GitHub, serving fakeReleases

	mu     sync.Mutex
	bumped map[string]bool // repos whose newest release is v2, not v1

	// The running binary, as self-update sees it: exe is a file in the
	// sandbox, a release build unless notReleased, and stderr is a
	// terminal when interactive.
	exe         string
	notReleased bool
	interactive bool
	// latest is the newest knapsack release on the fake GitHub; "" means
	// none. Without a build for this platform when noBuild.
	latest  string
	noBuild bool
	// ghCalls counts how often the CLI asked gh for a token, and ghToken
	// is the token gh gives.
	ghCalls atomic.Int32
	ghToken string
	// stdin is what the CLI reads from stdin, typed at a terminal when
	// terminal is set.
	stdin    string
	terminal bool
}

// bump publishes a new release, v2, of repo on the fake GitHub, or of an
// addon on the fake Wago.
func (s *sandbox) bump(repo string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bumped[repo] = true
}

func (s *sandbox) version(repo string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bumped[repo] {
		return "v2"
	}
	return "v1"
}

// clientDirs are the AddOns folders every sandbox has, relative to home.
var clientDirs = []string{
	"wow/_retail_/Interface/AddOns",
	"wow/_classic_/Interface/AddOns",
	"wow/_forever_/Interface/AddOns",
	"wow/_ptr_/Interface/AddOns",
	"wow/_classic_beta_/Interface/AddOns",
	"addons", // no client folder, so no flavor can be inferred
}

func newSandbox(t *testing.T) *sandbox {
	t.Helper()
	home := t.TempDir()
	for _, d := range clientDirs {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(d)), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	s := &sandbox{t: t, home: home, bumped: map[string]bool{}, exe: filepath.Join(home, "bin", binaryFile())}
	if err := os.MkdirAll(filepath.Dir(s.exe), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.exe, []byte("old binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	s.github = httptest.NewServer(http.HandlerFunc(s.serveFakeGitHub))
	t.Cleanup(s.github.Close)
	return s
}

// fakeReleases are the addons the fake GitHub has, by repo: each maps
// addon folders to their ## Interface line.
var fakeReleases = map[string]map[string]string{
	"austinrr/baganator-mirror": {"Baganator": "16001"},
	"austinrr/details-mirror":   {"Details": "120105, 16001", "Details_DataStorage": "120105, 16001"},
	"austinrr/retail-only":      {"RetailOnly": "120105"},
	"austinrr/private-addon":    {"PrivateAddon": "16001"},
}

// fakePrivateRepos are the fake GitHub's private repos, which only
// fakePrivateToken can read.
var fakePrivateRepos = map[string]bool{"austinrr/private-addon": true}

const fakePrivateToken = "gh-private"

// fakeWagoAddons are the addons the fake Wago has, by ID or slug, as
// fakeReleases. Its API is at <github>/wago and wants fakeWagoKey.
var fakeWagoAddons = map[string]map[string]string{
	"plumber": {"Plumber": "16001, 120105"},
}

const fakeWagoKey = "wago-key"

// wagoKeyEnv sets the fake Wago's key in the environment.
var wagoKeyEnv = map[string]string{"KNAPSACK_WAGO_API_KEY": fakeWagoKey}

// useWagoSite is a setup step that reads Wago from its website rather than
// its official API, the default, so it needs no key.
var useWagoSite = []string{"sources", "set-api", "wago", "website"}

// changedWagoAddon is an addon whose page on the fake Wago website isn't
// as Knapsack knows it, as if Wago had changed it.
const changedWagoAddon = "changed"

// serveFakeWagoSite answers /wagosite/addons/<id> with the addon's page, its
// stable release for each game version in Inertia's data-page attribute as
// Wago's website has it, and serves its zip at /wagosite/download/<id>.zip.
// Neither wants a key.
func (s *sandbox) serveFakeWagoSite(w http.ResponseWriter, r *http.Request) {
	if id, ok := strings.CutPrefix(r.URL.Path, "/wagosite/download/"); ok {
		id = strings.TrimSuffix(id, ".zip")
		folders, ok := fakeWagoAddons[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fakeZip(folders, s.version(id)))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/wagosite/addons/")
	if id == changedWagoAddon {
		_, _ = w.Write([]byte("<html><body>Wago, redesigned</body></html>"))
		return
	}
	if _, ok := fakeWagoAddons[id]; !ok {
		http.NotFound(w, r)
		return
	}
	rel := map[string]string{"label": s.version(id), "download_link": "http://" + r.Host + "/wagosite/download/" + id + ".zip"}
	data, err := json.Marshal(map[string]any{
		"component": "Addon/AddonDetails",
		"props": map[string]any{"metadata": map[string]any{"recent_releases": map[string]any{
			"retail": rel, "mop": rel, "classic": rel, "bc": rel, "forever": rel,
		}}},
	})
	if err != nil {
		panic(err)
	}
	_, _ = fmt.Fprintf(w, `<html><body><div id="app" data-page="%s"></div></body></html>`, html.EscapeString(string(data)))
}

// fakeCurseForgeAddons are the projects the fake CurseForge has, by slug:
// their ID, and their folders as fakeReleases. Its API is at <github>/cf
// and wants fakeCurseForgeKey; its website is at <github>/cfsite.
var fakeCurseForgeAddons = map[string]struct {
	id      string
	folders map[string]string
}{
	"syndicator": {"988370", map[string]string{"Syndicator": "16001, 120105"}},
	// The author allows only CurseForge's app to download it.
	"app-only": {"424242", map[string]string{"AppOnly": "16001"}},
}

const fakeCurseForgeKey = "cf-key"

// cfKeyEnv sets the fake CurseForge's key in the environment.
var cfKeyEnv = map[string]string{"KNAPSACK_CURSEFORGE_API_KEY": fakeCurseForgeKey}

// useCurseForgeSite is a setup step that reads CurseForge from its website.
var useCurseForgeSite = []string{"sources", "set-api", "curseforge", "website"}

// cfProject finds a fake CurseForge project by ID, returning its slug.
func cfProject(id string) (string, bool) {
	for slug, p := range fakeCurseForgeAddons {
		if p.id == id {
			return slug, true
		}
	}
	return "", false
}

// cfFileID is the project's file for its version, v1 or v2, so a bump is
// a new file, as on CurseForge.
func (s *sandbox) cfFileID(slug string) string {
	return fakeCurseForgeAddons[slug].id + strings.TrimPrefix(s.version(slug), "v")
}

// serveFakeCurseForge answers the fake CurseForge's API under /cf: a slug
// search at /cf/v1/mods/search, a project's files for a game version type
// at /cf/v1/mods/<id>/files, and their zips at /cf/download/<slug>.zip.
func (s *sandbox) serveFakeCurseForge(w http.ResponseWriter, r *http.Request) {
	if slug, ok := strings.CutPrefix(r.URL.Path, "/cf/download/"); ok {
		s.serveCurseForgeZip(w, r, strings.TrimSuffix(slug, ".zip"))
		return
	}
	if r.Header.Get("x-api-key") != fakeCurseForgeKey {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}
	if r.URL.Path == "/cf/v1/mods/search" {
		var found []map[string]any
		if p, ok := fakeCurseForgeAddons[r.URL.Query().Get("slug")]; ok {
			found = append(found, map[string]any{"id": json.Number(p.id), "slug": r.URL.Query().Get("slug")})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": found})
		return
	}
	id, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/cf/v1/mods/"), "/files")
	slug, known := cfProject(id)
	if !ok || !known {
		http.NotFound(w, r)
		return
	}
	file := map[string]any{
		"id": json.Number(s.cfFileID(slug)), "displayName": s.version(slug), "releaseType": 1, "isAvailable": true,
		"fileDate": "2026-11-01T00:00:00Z", "downloadUrl": "http://" + r.Host + "/cf/download/" + slug + ".zip",
	}
	if slug == "app-only" {
		file["downloadUrl"] = nil
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{file}})
}

// serveFakeCurseForgeSite answers the fake CurseForge website's JSON API
// under /cfsite/api/v1, which lists a project's files and downloads them,
// without a key. A slug's ID is on the fake CFWidget, at
// /cfwidget/wow/addons/<slug>.
func (s *sandbox) serveFakeCurseForgeSite(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/cfsite/api/v1/mods/")
	id, files, _ := strings.Cut(rest, "/")
	slug, known := cfProject(id)
	switch {
	case !ok || !known:
		// The website answers an unknown project with no files.
		_, _ = w.Write([]byte(`{"data":[],"pagination":{}}`))
	case files == "files":
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{
			"id": json.Number(s.cfFileID(slug)), "displayName": s.version(slug), "releaseType": 1,
			"gameVersionTypeIds": []int{517, 88568},
		}}})
	case files == "files/"+s.cfFileID(slug)+"/download":
		s.serveCurseForgeZip(w, r, slug)
	default:
		http.NotFound(w, r)
	}
}

func (s *sandbox) serveCurseForgeZip(w http.ResponseWriter, r *http.Request, slug string) {
	p, ok := fakeCurseForgeAddons[slug]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(fakeZip(p.folders, s.version(slug)))
}

// serveFakeWago answers /wago/addons/<id> with the addon's stable release
// for the game version asked for, and serves its zip at
// /wago/download/<id>.zip. Both want the key, as Wago does.
func (s *sandbox) serveFakeWago(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+fakeWagoKey {
		http.Error(w, `{"message":"Unauthenticated."}`, http.StatusUnauthorized)
		return
	}
	if id, ok := strings.CutPrefix(r.URL.Path, "/wago/download/"); ok {
		id = strings.TrimSuffix(id, ".zip")
		folders, ok := fakeWagoAddons[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fakeZip(folders, s.version(id)))
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/wago/addons/")
	if _, ok := fakeWagoAddons[id]; !ok || r.URL.Query().Get("game_version") == "" {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": id,
		"recent_release": map[string]any{"stable": map[string]string{
			"label":         s.version(id),
			"download_link": "http://" + r.Host + "/wago/download/" + id + ".zip",
		}},
	})
}

// serveFakeGitHub answers /repos/<repo>/releases/latest with a release of
// one zip, and serves the zip at /download/<repo>.zip. No digest is
// published, so update and check download to compare. Under /wago and
// /wagosite, it's the fake Wago's API and website instead.
func (s *sandbox) serveFakeGitHub(w http.ResponseWriter, r *http.Request) {
	if s.serveFakeKnapsack(w, r) {
		return
	}
	if strings.HasPrefix(r.URL.Path, "/wago/") {
		s.serveFakeWago(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/wagosite/") {
		s.serveFakeWagoSite(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/cf/") {
		s.serveFakeCurseForge(w, r)
		return
	}
	if slug, ok := strings.CutPrefix(r.URL.Path, "/cfwidget/wow/addons/"); ok {
		p, known := fakeCurseForgeAddons[slug]
		if !known {
			http.Error(w, "", http.StatusNotFound)
			return
		}
		_, _ = fmt.Fprintf(w, `{"id":%s,"title":"%s"}`, p.id, slug)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/cfsite/") {
		s.serveFakeCurseForgeSite(w, r)
		return
	}
	// A private repo is there only for its token, as on GitHub, which
	// answers 404 otherwise.
	hidden := func(repo string) bool {
		return fakePrivateRepos[repo] && r.Header.Get("Authorization") != "Bearer "+fakePrivateToken
	}
	if repo, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		repo = strings.TrimSuffix(repo, ".zip")
		folders, ok := fakeReleases[repo]
		if !ok || fakePrivateRepos[repo] { // a browser link needs a login
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(fakeZip(folders, s.version(repo)))
		return
	}
	if repo, ok := strings.CutPrefix(r.URL.Path, "/signed/"); ok {
		repo = strings.TrimSuffix(repo, ".zip")
		_, _ = w.Write(fakeZip(fakeReleases[repo], s.version(repo)))
		return
	}
	if repo, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/releases/assets/1"); ok {
		// The API downloads an asset when asked for the file, redirecting
		// to a signed link.
		if _, known := fakeReleases[repo]; !known || hidden(repo) || r.Header.Get("Accept") != "application/octet-stream" {
			http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
			return
		}
		http.Redirect(w, r, "/signed/"+repo+".zip", http.StatusFound)
		return
	}
	repo, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/repos/"), "/releases/latest")
	if _, known := fakeReleases[repo]; !ok || !known || hidden(repo) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}
	version := s.version(repo)
	name := path.Base(repo) + "-" + version + ".zip"
	_ = json.NewEncoder(w).Encode(map[string]any{
		"tag_name": version,
		"assets": []map[string]string{{
			"name":                 name,
			"url":                  "http://" + r.Host + "/repos/" + repo + "/releases/assets/1",
			"browser_download_url": "http://" + r.Host + "/download/" + repo + ".zip",
		}},
	})
}

// fakeZip builds a release zip with a TOC per folder. The version is in
// each TOC, so every version's checksum differs.
func fakeZip(folders map[string]string, version string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range slices.Sorted(maps.Keys(folders)) {
		w, _ := zw.Create(f + "/" + f + ".toc")
		_, _ = fmt.Fprintf(w, "## Interface: %s\n## Title: %s\n## Author: Someone\n## Version: %s\n", folders[f], f, version)
	}
	_ = zw.Close()
	return buf.Bytes()
}

// run runs the CLI in-process with args and returns its normalized output
// and exit code.
func (s *sandbox) run(args ...string) result {
	s.t.Helper()
	expanded := make([]string, len(args))
	for i, a := range args {
		expanded[i] = strings.ReplaceAll(a, "{home}", s.home)
	}
	getenv := func(k string) string {
		if k == paths.HomeEnv {
			return s.home
		}
		return s.env[k]
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), expanded, env{
		stdout: &stdout, stderr: &stderr, getenv: getenv, version: testVersion,
		released:          !s.notReleased,
		executable:        s.exe,
		home:              s.home,
		interactive:       s.interactive,
		githubURL:         s.github.URL,
		wagoURL:           s.github.URL + "/wago",
		wagoSiteURL:       s.github.URL + "/wagosite",
		curseforgeURL:     s.github.URL + "/cf",
		curseforgeSiteURL: s.github.URL + "/cfsite",
		cfwidgetURL:       s.github.URL + "/cfwidget",
		platform:          "testos/testarch",
		ghAuthToken:       func(context.Context) string { s.ghCalls.Add(1); return s.ghToken },

		stdin:         strings.NewReader(s.stdin),
		stdinTerminal: s.terminal,
		readPassword:  func(context.Context) ([]byte, error) { return []byte(s.stdin), nil },
	})
	return result{Stdout: s.normalize(stdout.String()), Stderr: s.normalize(stderr.String()), Code: code}
}

// mustRun runs a setup step and fails the test if it doesn't succeed.
func (s *sandbox) mustRun(args ...string) {
	s.t.Helper()
	if r := s.run(args...); r.Code != exitOK {
		s.t.Fatalf("setup %q failed:\n%s", args, r)
	}
}

// writeConfig writes config.toml directly, for states the CLI can't create.
func (s *sandbox) writeConfig(contents string) {
	s.t.Helper()
	dir := filepath.Join(s.home, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		s.t.Fatal(err)
	}
	contents = strings.ReplaceAll(contents, "{home}", filepath.ToSlash(s.home))
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(contents), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

// tildePath matches a path under home shown as ~/… or, in a command to
// copy, $HOME/…, at the start of a word or quote. Windows shows neither.
var tildePath = regexp.MustCompile(`(^|[\s"])(~|\$HOME)/`)

// timestamp matches the install times commands print.
var timestamp = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ`)

// normalize replaces the sandbox path with <home>, including its escaped
// form in TOML and JSON, the fake GitHub's address with <github>, install
// times with <time>, this OS and architecture with <platform>, and the
// folder its example game path is in with <games>, and
// uses / as the separator, so one golden file works on every OS and every
// run.
func (s *sandbox) normalize(out string) string {
	out = strings.ReplaceAll(out, s.github.URL, "<github>")
	out = strings.ReplaceAll(out, url.QueryEscape(s.github.URL), "<github>") // in an issue link
	out = strings.ReplaceAll(out, runtime.GOOS+"/"+runtime.GOARCH, "<platform>")
	// Hints and help show where the game usually is on this OS.
	out = strings.ReplaceAll(out, filepath.Dir(filepath.Dir(app.ExampleClientFolder())), "<games>")
	out = timestamp.ReplaceAllString(out, "<time>")
	for _, h := range []string{strings.ReplaceAll(s.home, `\`, `\\`), s.home, filepath.ToSlash(s.home)} {
		out = strings.ReplaceAll(out, h, "<home>")
	}
	// Paths under home are shown as ~/… or $HOME/… except on Windows.
	out = tildePath.ReplaceAllString(out, "$1<home>/")
	if runtime.GOOS == "windows" {
		out = strings.ReplaceAll(out, `\\`, "/")
		out = strings.ReplaceAll(out, `\`, "/")
	}
	return out
}

// String renders a result in the golden file format: the exit code, then
// stdout and stderr in labelled sections.
func (r result) String() string {
	return fmt.Sprintf("# exit: %d\n# stdout:\n%s# stderr:\n%s", r.Code, r.Stdout, r.Stderr)
}

// checkGolden compares r with testdata/<name>.golden. Run
// `go test ./cmd/... -update` to rewrite the golden files after an
// intended change, then review the diff.
func checkGolden(t *testing.T, name string, r result) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")
	got := r.String()

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			t.Fatalf("update golden file: %v", err)
		}
		return
	}

	wantBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	// .gitattributes checks golden files out with LF on every OS.
	want := string(wantBytes)

	if got != want {
		t.Errorf("output differs from %s (run with -update to accept):\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// cliCase is one golden test: setup steps that must succeed, then the
// command whose output is compared with testdata/<name>.golden.
type cliCase struct {
	name   string
	config string            // written as config.toml before setup, if set
	env    map[string]string // extra environment variables
	setup  [][]string
	after  func(*sandbox) // runs after setup, before args
	args   []string

	// For the update check and self-update, set for args only: the newest
	// knapsack release, whether stderr is a terminal, and whether the
	// binary isn't a release build.
	latest      string
	interactive bool
	notReleased bool

	// For set-key: what's on stdin for args, typed at a terminal when
	// terminal is set.
	stdin    string
	terminal bool
}

func runCases(t *testing.T, cases []cliCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSandbox(t)
			s.env = tc.env
			if tc.config != "" {
				s.writeConfig(tc.config)
			}
			for _, step := range tc.setup {
				s.mustRun(step...)
			}
			if tc.after != nil {
				tc.after(s)
			}
			if tc.latest != "" {
				s.latest = tc.latest
			}
			s.interactive = s.interactive || tc.interactive
			s.notReleased = tc.notReleased
			s.stdin, s.terminal = tc.stdin, tc.terminal
			checkGolden(t, tc.name, s.run(tc.args...))
		})
	}
}
