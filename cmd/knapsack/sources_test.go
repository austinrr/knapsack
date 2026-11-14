package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// setCurseForgeKey pipes a key to set-key, as a setup step.
func setCurseForgeKey(s *sandbox) {
	s.stdin = "cf-secret\n"
	s.mustRun("sources", "set-key", "curseforge")
	s.stdin = ""
}

func TestSourcesCommands(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		{name: "sources/help", args: []string{"sources", "--help"}},
		{name: "sources/list", args: []string{"sources"}},
		{name: "sources/list_keys", after: setCurseForgeKey,
			env:  map[string]string{"GITHUB_TOKEN": "gh-env", "KNAPSACK_WAGO_API_KEY": "wago-env"},
			args: []string{"sources"}},
		{name: "sources/list_gh", after: func(s *sandbox) { s.ghToken = "from-gh" }, args: []string{"sources"}},
		{name: "sources/list_json", after: setCurseForgeKey, env: map[string]string{"GH_TOKEN": "gh-env"}, args: []string{"sources", "-o", "json"}},
		{name: "sources/unknown_subcommand", args: []string{"sources", "bogus"}},

		// set-key
		{name: "sources/set_key_piped", stdin: "cf-secret\n", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_typed", stdin: "cf-secret", terminal: true, args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_unchanged", after: setCurseForgeKey, stdin: "cf-secret", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_overridden", stdin: "cf-secret", env: map[string]string{"KNAPSACK_CURSEFORGE_API_KEY": "env"},
			args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_empty", stdin: "  \n", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_github", stdin: "token", args: []string{"sources", "set-key", "github"}},
		{name: "sources/set_key_unknown", stdin: "key", args: []string{"sources", "set-key", "nope"}},
		{name: "sources/set_key_no_args", args: []string{"sources", "set-key"}},
		// The key is redacted wherever config is shown.
		{name: "sources/view_redacted", after: setCurseForgeKey, args: []string{"config", "view"}},

		// unset-key
		{name: "sources/unset_key", after: setCurseForgeKey, args: []string{"sources", "unset-key", "curseforge"}},
		{name: "sources/unset_key_none", args: []string{"sources", "unset-key", "wago"}},
		{name: "sources/unset_key_overridden", after: setCurseForgeKey, env: map[string]string{"KNAPSACK_CURSEFORGE_API_KEY": "env"},
			args: []string{"sources", "unset-key", "curseforge"}},
	})
}

// A key in config makes it readable only by the user, and removing it
// leaves the key out of the file.
func TestSetKeyFile(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	setCurseForgeKey(s)
	path := filepath.Join(s.home, "config", "config.toml")
	data, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(data), `api_key = "cf-secret"`) {
		t.Fatalf("config = %q, %v; want the key", data, err)
	}
	if info, err := os.Stat(path); runtime.GOOS != "windows" && (err != nil || info.Mode().Perm() != 0o600) {
		t.Errorf("config mode = %v, %v; want 0600", info.Mode().Perm(), err)
	}
	s.mustRun("sources", "unset-key", "curseforge")
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "cf-secret") {
		t.Errorf("config still has the key after unset-key:\n%s", data)
	}
}
