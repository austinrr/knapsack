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
		// Bare sources shows its subcommands, as config does.
		{name: "sources/bare", args: []string{"sources"}},
		{name: "sources/ls", args: []string{"sources", "ls"}},
		{name: "sources/list", args: []string{"sources", "list"}},
		{name: "sources/list_keys", after: setCurseForgeKey,
			env:  map[string]string{"GITHUB_TOKEN": "gh-env", "KNAPSACK_WAGO_API_KEY": "wago-env"},
			args: []string{"sources", "list"}},
		{name: "sources/list_wago_ready", env: wagoKeyEnv, args: []string{"sources", "list"}},
		{name: "sources/list_gh", after: func(s *sandbox) { s.ghToken = "from-gh" }, args: []string{"sources", "list"}},
		{name: "sources/list_json", after: setCurseForgeKey, env: map[string]string{"GH_TOKEN": "gh-env"}, args: []string{"sources", "list", "-o", "json"}},
		{name: "sources/unknown_subcommand", args: []string{"sources", "bogus"}},
		{name: "sources/list_website", setup: [][]string{useWagoSite}, args: []string{"sources", "list"}},
		{name: "sources/list_website_json", setup: [][]string{useWagoSite}, args: []string{"sources", "list", "-o", "json"}},

		// set-api
		{name: "sources/set_api_help", args: []string{"sources", "set-api", "--help"}},
		{name: "sources/set_api_website", args: []string{"sources", "set-api", "wago", "website"}},
		{name: "sources/set_api_unchanged", setup: [][]string{useWagoSite}, args: []string{"sources", "set-api", "wago", "website"}},
		{name: "sources/set_api_official", setup: [][]string{useWagoSite}, args: []string{"sources", "set-api", "wago", "official"}},
		{name: "sources/set_api_official_with_key", setup: [][]string{useWagoSite}, env: wagoKeyEnv, args: []string{"sources", "set-api", "wago", "official"}},
		{name: "sources/set_api_official_default", args: []string{"sources", "set-api", "wago", "official"}},
		{name: "sources/set_api_not_yet", args: []string{"sources", "set-api", "curseforge", "website"}},
		{name: "sources/set_api_bad", args: []string{"sources", "set-api", "wago", "browser"}},
		{name: "sources/set_api_github", args: []string{"sources", "set-api", "github", "website"}},
		// The source is checked first, so no hint names one that fails.
		{name: "sources/set_api_github_bad", args: []string{"sources", "set-api", "github", "browser"}},
		{name: "sources/set_api_unknown_bad", args: []string{"sources", "set-api", "nope", "browser"}},
		// Completion offers the sources, then how to read them.
		{name: "sources/set_api_complete_source", args: []string{"__complete", "sources", "set-api", ""}},
		{name: "sources/set_api_complete_api", args: []string{"__complete", "sources", "set-api", "wago", ""}},
		{name: "sources/set_api_unknown", args: []string{"sources", "set-api", "nope", "website"}},
		{name: "sources/set_api_no_args", args: []string{"sources", "set-api", "wago"}},
		{name: "sources/view_api", setup: [][]string{useWagoSite}, args: []string{"config", "view"}},
		{name: "sources/view_api_json", setup: [][]string{useWagoSite}, args: []string{"config", "view", "-o", "json"}},

		// set-key
		{name: "sources/set_key_piped", stdin: "cf-secret\n", args: []string{"sources", "set-key", "curseforge"}},
		// Read from its website, Wago doesn't use the key until set-api
		// official.
		{name: "sources/set_key_website", setup: [][]string{useWagoSite}, stdin: "wago-secret\n", args: []string{"sources", "set-key", "wago"}},
		{name: "sources/set_key_typed", stdin: "cf-secret", terminal: true, args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_unchanged", after: setCurseForgeKey, stdin: "cf-secret", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_overridden", stdin: "cf-secret", env: map[string]string{"KNAPSACK_CURSEFORGE_API_KEY": "env"},
			args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_empty", stdin: "  \n", args: []string{"sources", "set-key", "curseforge"}},
		// pass prints the secret first, then other fields.
		{name: "sources/set_key_pass_output", stdin: "cf-secret\nlogin: me\nurl: console.curseforge.com\n", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_spaces", stdin: "cf secret\n", args: []string{"sources", "set-key", "curseforge"}},
		{name: "sources/set_key_not_text", stdin: "cf\xff\xfe", args: []string{"sources", "set-key", "curseforge"}},
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
	// Only pass's first line is the key.
	s.stdin = "cf-secret-2\nlogin: me\n"
	s.mustRun("sources", "set-key", "curseforge")
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `api_key = "cf-secret-2"`) || strings.Contains(string(data), "login") {
		t.Errorf("config after a pass-style key:\n%s", data)
	}
	s.stdin = ""
	s.mustRun("sources", "unset-key", "curseforge")
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "cf-secret") {
		t.Errorf("config still has the key after unset-key:\n%s", data)
	}
}

// Choosing the website writes it to config, and choosing the official
// API, the default, leaves config without the setting.
func TestSetAPIFile(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	path := filepath.Join(s.home, "config", "config.toml")
	s.mustRun(useWagoSite...)
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), "[sources.wago]\napi = \"website\"\n") {
		t.Fatalf("config = %q, %v; want api = website", data, err)
	}
	s.mustRun("sources", "set-api", "wago", "official")
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "sources.wago") {
		t.Errorf("config still has a wago table:\n%s", data)
	}
}
