package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Setup steps for the addon commands. The fake GitHub has baganator-mirror
// and details-mirror for Forever, and retail-only, which isn't.
var (
	addBaganator = []string{"add", "github:austinrr/baganator-mirror"}
	addDetails   = []string{"add", "github:austinrr/details-mirror"}
	addPlumber   = []string{"add", "wago:plumber"} // needs wagoKeyEnv, or useWagoSite
)

// wagoKeyInConfig is a Forever profile with the fake Wago's key in config
// rather than the environment.
const wagoKeyInConfig = `current_profile = "forever"

[profiles.forever]
path = "{home}/wow/_forever_/Interface/AddOns"
flavor = "forever"

[sources.wago]
api_key = "` + fakeWagoKey + `"
`

// handInstall puts folders in AddOns as installing by hand would:
// WeakAuras, with a TOC, and LibStub, without one, in Forever, and Plater
// in Retail.
func handInstall(s *sandbox) {
	s.t.Helper()
	for dir, folders := range map[string][]string{
		"wow/_forever_/Interface/AddOns": {"WeakAuras", "LibStub"},
		"wow/_retail_/Interface/AddOns":  {"Plater"},
	} {
		for _, f := range folders {
			if err := os.MkdirAll(filepath.Join(s.home, filepath.FromSlash(dir), f), 0o700); err != nil {
				s.t.Fatal(err)
			}
		}
	}
	toc := filepath.Join(s.home, "wow", "_forever_", "Interface", "AddOns", "WeakAuras", "WeakAuras.toc")
	if err := os.WriteFile(toc, []byte("## Interface: 11508\n## Title: WeakAuras\n## Version: 5.12.0\n"), 0o600); err != nil {
		s.t.Fatal(err)
	}
}

func TestAddonCommands(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		// add
		{name: "add/help", args: []string{"add", "--help"}},
		{name: "add/no_args", setup: [][]string{addForever}, args: []string{"add"}},
		{name: "add/bad_ref", setup: [][]string{addForever}, args: []string{"add", "notaref"}},
		{name: "add/bad_ref_among_good", setup: [][]string{addForever},
			args: []string{"add", "github:austinrr/baganator-mirror", "notaref"}},
		{name: "add/no_profiles", args: addBaganator},
		{name: "add/one", setup: [][]string{addForever}, args: addBaganator},
		{name: "add/url", setup: [][]string{addForever},
			args: []string{"add", "https://github.com/austinrr/Baganator-Mirror"}},
		{name: "add/already_added", setup: [][]string{addForever, addBaganator}, args: addBaganator},
		{name: "add/partial_failure", setup: [][]string{addForever},
			args: []string{"add", "github:austinrr/baganator-mirror", "github:austinrr/missing", "github:austinrr/retail-only"}},
		{name: "add/curseforge_no_key", setup: [][]string{addForever}, args: []string{"add", "curseforge:12345"}},
		{name: "add/verbose", setup: [][]string{addForever}, args: []string{"add", "-v", "github:austinrr/details-mirror"}},
		{name: "add/profile_flag", setup: [][]string{addForever, addRetail},
			args: []string{"add", "-p", "retail", "github:austinrr/retail-only"}},
		{name: "add/profile_env", setup: [][]string{addForever, addRetail},
			env:  map[string]string{"KNAPSACK_PROFILE": "retail"},
			args: []string{"add", "github:austinrr/retail-only"}},
		// Wago's website, which needs no key.
		{name: "add/wago_website", setup: [][]string{addForever, useWagoSite}, args: addPlumber},
		{name: "add/wago_website_url", setup: [][]string{addForever, useWagoSite},
			args: []string{"add", "https://addons.wago.io/addons/plumber"}},
		{name: "add/wago_website_unknown", setup: [][]string{addForever, useWagoSite}, args: []string{"add", "wago:nope"}},
		// A page Knapsack doesn't understand gets a link to report it.
		{name: "add/wago_website_changed", setup: [][]string{addForever, useWagoSite}, args: []string{"add", "wago:" + changedWagoAddon}},
		// Wago's official API, the default, with the key in the environment
		// or config, or without one.
		{name: "add/wago", env: wagoKeyEnv, setup: [][]string{addForever}, args: addPlumber},
		{name: "add/wago_url", env: wagoKeyEnv, setup: [][]string{addForever},
			args: []string{"add", "https://addons.wago.io/addons/plumber"}},
		{name: "add/wago_key_in_config", config: wagoKeyInConfig, args: addPlumber},
		{name: "add/wago_no_key", setup: [][]string{addForever}, args: addPlumber},
		{name: "add/wago_bad_key", env: map[string]string{"KNAPSACK_WAGO_API_KEY": "wrong"}, setup: [][]string{addForever}, args: addPlumber},
		{name: "add/wago_unknown", env: wagoKeyEnv, setup: [][]string{addForever}, args: []string{"add", "wago:nope"}},
		// CurseForge's official API, the default, by slug, ID or link.
		{name: "add/curseforge", env: cfKeyEnv, setup: [][]string{addForever}, args: []string{"add", "curseforge:syndicator"}},
		{name: "add/curseforge_id", env: cfKeyEnv, setup: [][]string{addForever}, args: []string{"add", "curseforge:988370"}},
		{name: "add/curseforge_url", env: cfKeyEnv, setup: [][]string{addForever},
			args: []string{"add", "https://www.curseforge.com/wow/addons/syndicator"}},
		{name: "add/curseforge_bad_key", env: map[string]string{"KNAPSACK_CURSEFORGE_API_KEY": "wrong"}, setup: [][]string{addForever},
			args: []string{"add", "curseforge:syndicator"}},
		{name: "add/curseforge_unknown", env: cfKeyEnv, setup: [][]string{addForever}, args: []string{"add", "curseforge:nope"}},
		{name: "add/curseforge_not_distributed", env: cfKeyEnv, setup: [][]string{addForever}, args: []string{"add", "curseforge:app-only"}},
		// CurseForge's website: a pasted link or slug is looked up on the
		// project's page.
		{name: "add/curseforge_website_url", setup: [][]string{addForever, useCurseForgeSite},
			args: []string{"add", "https://www.curseforge.com/wow/addons/syndicator"}},
		{name: "add/curseforge_website_id", setup: [][]string{addForever, useCurseForgeSite}, args: []string{"add", "curseforge:988370"}},
		// Given as a slug and an ID, it's one addon.
		{name: "add/curseforge_slug_and_id", env: cfKeyEnv, setup: [][]string{addForever}, args: []string{"add", "curseforge:syndicator", "curseforge:988370"}},
		{name: "add/curseforge_already_added", env: cfKeyEnv, setup: [][]string{addForever, {"add", "curseforge:988370"}},
			args: []string{"add", "https://www.curseforge.com/wow/addons/syndicator"}},
		{name: "add/curseforge_website_unknown", setup: [][]string{addForever, useCurseForgeSite}, args: []string{"add", "curseforge:nope", "curseforge:999"}},
		{name: "add/unknown_profile", setup: [][]string{addForever}, args: []string{"add", "-p", "nope", "github:austinrr/baganator-mirror"}},

		// remove
		{name: "remove/help", args: []string{"remove", "--help"}},
		{name: "remove/no_args", setup: [][]string{addForever}, args: []string{"remove"}},
		{name: "remove/short_name", setup: [][]string{addForever, addBaganator, addDetails}, args: []string{"remove", "baganator-mirror"}},
		{name: "remove/alias", setup: [][]string{addForever, addBaganator}, args: []string{"rm", "github:austinrr/baganator-mirror"}},
		{name: "remove/unknown", setup: [][]string{addForever, addBaganator}, args: []string{"rm", "nope", "baganator-mirror"}},

		// list
		{name: "list/empty", setup: [][]string{addForever}, args: []string{"list"}},
		{name: "list/table", setup: [][]string{addForever, addBaganator, addDetails}, args: []string{"list"}},
		{name: "list/wide", setup: [][]string{addForever, addBaganator, addDetails}, args: []string{"ls", "-o", "wide"}},
		{name: "list/json", setup: [][]string{addForever, addBaganator}, args: []string{"list", "-o", "json"}},
		{name: "list/bad_output", setup: [][]string{addForever}, args: []string{"list", "-o", "yaml"}},
		{name: "list/extra_arg", setup: [][]string{addForever}, args: []string{"list", "baganator"}},
		{name: "list/missing", config: configWithKey, args: []string{"list"}},
		// list checks each addon, unless --no-check.
		{name: "list/update_available", setup: [][]string{addForever, addBaganator, addDetails}, after: bumpBaganator, args: []string{"list"}},
		{name: "list/no_check", setup: [][]string{addForever, addBaganator}, after: bumpBaganator, args: []string{"list", "--no-check"}},
		{name: "list/wago", env: wagoKeyEnv, setup: [][]string{addForever, addBaganator, addPlumber}, args: []string{"list"}},
		{name: "list/wago_website", setup: [][]string{addForever, useWagoSite, addBaganator, addPlumber}, args: []string{"list"}},
		{name: "list/check_failed", config: missingAndGone, args: []string{"list"}},
		{name: "list/check_failed_json", config: missingAndGone, args: []string{"list", "-o", "json"}},
		// list names addons by title, so commands take titles too.
		{name: "info/by_title", setup: [][]string{addForever, addDetails}, args: []string{"info", "Details"}},
		{name: "remove/by_title", setup: [][]string{addForever, addBaganator}, args: []string{"remove", "baganator"}},
		{name: "list/unmanaged_hint", setup: [][]string{addForever, addBaganator}, after: handInstall, args: []string{"list"}},
		{name: "list/unmanaged_hint_empty", setup: [][]string{addForever}, after: handInstall, args: []string{"list"}},
		{name: "list/unmanaged_hint_one", setup: [][]string{addRetail}, after: handInstall, args: []string{"list"}},
		{name: "list/unmanaged", setup: [][]string{addForever, addBaganator}, after: handInstall, args: []string{"list", "--unmanaged"}},
		{name: "list/unmanaged_wide", setup: [][]string{addForever, addBaganator}, after: handInstall, args: []string{"list", "--unmanaged", "-o", "wide"}},
		{name: "list/unmanaged_json", setup: [][]string{addForever, addBaganator}, after: handInstall, args: []string{"list", "--unmanaged", "-o", "json"}},
		{name: "list/unmanaged_json_hidden", setup: [][]string{addForever, addBaganator}, after: handInstall, args: []string{"list", "-o", "json"}},
		{name: "list/unmanaged_all", setup: [][]string{addForever, addRetail, addBaganator}, after: handInstall, args: []string{"list", "-A", "--unmanaged"}},
		{name: "list/unmanaged_hint_all", setup: [][]string{addForever, addRetail, addBaganator}, after: handInstall, args: []string{"list", "-A"}},

		// info
		{name: "info/one", setup: [][]string{addForever, addDetails}, args: []string{"info", "details-mirror"}},
		{name: "info/json", setup: [][]string{addForever, addBaganator}, args: []string{"info", "baganator-mirror", "-o", "json"}},
		{name: "info/not_installed", config: configWithKey, args: []string{"info", "baganator-mirror"}},
		{name: "info/unknown", setup: [][]string{addForever}, args: []string{"info", "nope"}},
		{name: "info/no_args", setup: [][]string{addForever}, args: []string{"info"}},
	})
}
