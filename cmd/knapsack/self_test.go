package main

import (
	"os"
	"runtime"
	"testing"
)

// testVersion is v0.0.0-test, a pre-release, so v0.0.0 is newer.
const newerVersion = "v0.0.1"

func TestSelfCommands(t *testing.T) {
	t.Parallel()
	noNotifier := map[string]string{noNotifierEnv: "1"}
	// checked runs a command that does the daily check, as if earlier
	// today, so the next one doesn't check again.
	checked := func(s *sandbox) {
		s.interactive, s.latest = true, newerVersion
		s.mustRun("config", "get-profiles")
	}
	runCases(t, []cliCase{
		// version checks every time, whether or not stderr is a terminal.
		{name: "version/newer", latest: newerVersion, args: []string{"version"}},
		{name: "version/up_to_date", latest: testVersion, args: []string{"version"}},
		{name: "version/opted_out", latest: newerVersion, env: noNotifier, args: []string{"version"}},
		{name: "version/go_install", latest: newerVersion, notReleased: true, args: []string{"version"}},
		{name: "version/check_failed", args: []string{"version"}},
		{name: "version/check_failed_verbose", args: []string{"version", "-v"}},
		{name: "version/after_daily_check", after: checked, args: []string{"version"}},

		// Other commands check once a day, on a terminal.
		{name: "notice/shown", latest: newerVersion, interactive: true, args: []string{"config", "get-profiles"}},
		{name: "notice/after_error", latest: newerVersion, interactive: true, args: []string{"info", "nope"}},
		{name: "notice/up_to_date", latest: testVersion, interactive: true, args: []string{"config", "get-profiles"}},
		{name: "notice/not_a_terminal", latest: newerVersion, args: []string{"config", "get-profiles"}},
		{name: "notice/ci", latest: newerVersion, interactive: true, env: map[string]string{"CI": "true"}, args: []string{"config", "get-profiles"}},
		{name: "notice/opted_out", latest: newerVersion, interactive: true, env: noNotifier, args: []string{"config", "get-profiles"}},
		{name: "notice/once_a_day", after: checked, args: []string{"config", "get-profiles"}},
		{name: "notice/go_install", latest: newerVersion, interactive: true, notReleased: true, args: []string{"config", "get-profiles"}},
		{name: "notice/check_failed", interactive: true, args: []string{"config", "get-profiles"}},
		{name: "notice/not_for_help", latest: newerVersion, interactive: true, args: []string{"help"}},

		// self-update
		{name: "self-update/help", args: []string{"self-update", "--help"}},
		{name: "self-update/updated", latest: newerVersion, args: []string{"self-update"}},
		{name: "self-update/up_to_date", latest: testVersion, args: []string{"self-update"}},
		{name: "self-update/go_install", latest: newerVersion, notReleased: true, args: []string{"self-update"}},
		{name: "self-update/no_build", latest: newerVersion, after: func(s *sandbox) { s.noBuild = true }, args: []string{"self-update"}},
		{name: "self-update/no_release", args: []string{"self-update"}},
		{name: "self-update/extra_arg", args: []string{"self-update", "now"}},
	})
}

// self-update replaces the binary with the release's, and leaves it alone
// when it can't verify the download.
func TestSelfUpdateReplacesBinary(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.latest = newerVersion
	if r := s.run("self-update"); r.Code != exitOK {
		t.Fatalf("self-update failed:\n%s", r)
	}
	got, err := os.ReadFile(s.exe)
	if err != nil || string(got) != newBinary(newerVersion) {
		t.Errorf("binary = %q, %v; want %q", got, err, newBinary(newerVersion))
	}
	// Windows has no executable bit.
	if info, err := os.Stat(s.exe); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm()&0o100 == 0) {
		t.Errorf("binary mode = %v, %v; want executable", info.Mode(), err)
	}
}
