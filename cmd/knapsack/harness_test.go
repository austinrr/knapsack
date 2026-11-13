package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
	t    *testing.T
	home string
}

// clientDirs are the AddOns folders every sandbox has, relative to home.
var clientDirs = []string{
	"wow/_retail_/Interface/AddOns",
	"wow/_classic_/Interface/AddOns",
	"wow/_forever_/Interface/AddOns",
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
	return &sandbox{t: t, home: home}
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
		return ""
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), expanded, env{
		stdout: &stdout, stderr: &stderr, getenv: getenv, version: testVersion,
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

// normalize replaces the sandbox path with <home>, including its escaped
// form in TOML and JSON, and uses / as the separator, so one golden file
// works on every OS.
func (s *sandbox) normalize(out string) string {
	for _, h := range []string{strings.ReplaceAll(s.home, `\`, `\\`), s.home, filepath.ToSlash(s.home)} {
		out = strings.ReplaceAll(out, h, "<home>")
	}
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
	config string // written as config.toml before setup, if set
	setup  [][]string
	args   []string
}

func runCases(t *testing.T, cases []cliCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSandbox(t)
			if tc.config != "" {
				s.writeConfig(tc.config)
			}
			for _, step := range tc.setup {
				s.mustRun(step...)
			}
			checkGolden(t, tc.name, s.run(tc.args...))
		})
	}
}
