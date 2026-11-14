package main

import "testing"

// Setup steps for the addon commands. The fake GitHub has baganator-mirror
// and details-mirror for Forever, and retail-only, which isn't.
var (
	addBaganator = []string{"add", "github:austinrr/baganator-mirror"}
	addDetails   = []string{"add", "github:austinrr/details-mirror"}
)

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
		{name: "add/unconfigured_source", setup: [][]string{addForever}, args: []string{"add", "curseforge:12345"}},
		{name: "add/verbose", setup: [][]string{addForever}, args: []string{"add", "-v", "github:austinrr/details-mirror"}},
		{name: "add/profile_flag", setup: [][]string{addForever, addRetail},
			args: []string{"add", "-p", "retail", "github:austinrr/retail-only"}},
		{name: "add/profile_env", setup: [][]string{addForever, addRetail},
			env:  map[string]string{"KNAPSACK_PROFILE": "retail"},
			args: []string{"add", "github:austinrr/retail-only"}},
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

		// info
		{name: "info/one", setup: [][]string{addForever, addDetails}, args: []string{"info", "details-mirror"}},
		{name: "info/json", setup: [][]string{addForever, addBaganator}, args: []string{"info", "baganator-mirror", "-o", "json"}},
		{name: "info/not_installed", config: configWithKey, args: []string{"info", "baganator-mirror"}},
		{name: "info/unknown", setup: [][]string{addForever}, args: []string{"info", "nope"}},
		{name: "info/no_args", setup: [][]string{addForever}, args: []string{"info"}},
	})
}
