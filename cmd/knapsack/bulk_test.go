package main

import "testing"

var (
	addRetailOnly = []string{"add", "-p", "retail", "github:austinrr/retail-only"}
	bothProfiles  = [][]string{addForever, addRetail, addBaganator, addRetailOnly}
)

func bumpBaganator(s *sandbox) { s.bump("austinrr/baganator-mirror") }

// pruneDetails leaves details-mirror installed but drops it from config,
// as a hand edit would.
func pruneDetails(s *sandbox) {
	s.writeConfig(`current_profile = "forever"

[profiles.forever]
path = "{home}/wow/_forever_/Interface/AddOns"
flavor = "forever"
addons = ["github:austinrr/baganator-mirror"]
`)
}

// missingAndGone has an addon in config that was never installed, and
// one whose repository has gone.
const missingAndGone = `current_profile = "forever"

[profiles.forever]
path = "{home}/wow/_forever_/Interface/AddOns"
flavor = "forever"
addons = ["github:austinrr/baganator-mirror", "github:austinrr/gone"]
`

func TestBulkCommands(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		// update
		{name: "update/help", args: []string{"update", "--help"}},
		{name: "update/no_addons", setup: [][]string{addForever}, args: []string{"update"}},
		{name: "update/up_to_date", setup: [][]string{addForever, addBaganator, addDetails}, args: []string{"update"}},
		{name: "update/upgrade", setup: [][]string{addForever, addBaganator, addDetails}, after: bumpBaganator,
			args: []string{"update"}},
		// One addon, so the progress lines come in a fixed order.
		{name: "update/verbose", setup: [][]string{addForever, addBaganator}, after: bumpBaganator,
			args: []string{"update", "-v"}},
		{name: "update/named", setup: [][]string{addForever, addBaganator, addDetails}, after: bumpBaganator,
			args: []string{"update", "details-mirror"}},
		{name: "update/missing_and_gone", config: missingAndGone, args: []string{"update"}},
		{name: "update/no_prune", setup: [][]string{addForever, addBaganator, addDetails}, after: pruneDetails,
			args: []string{"update"}},
		{name: "update/no_prune_named", setup: [][]string{addForever, addBaganator, addDetails}, after: pruneDetails,
			args: []string{"update", "details-mirror"}},
		{name: "update/prune", setup: [][]string{addForever, addBaganator, addDetails}, after: pruneDetails,
			args: []string{"update", "--prune"}},
		{name: "update/all", setup: bothProfiles, after: bumpBaganator, args: []string{"update", "-A"}},
		{name: "update/all_with_profile", setup: [][]string{addForever}, args: []string{"update", "-A", "-p", "forever"}},
		{name: "update/all_with_names", setup: [][]string{addForever}, args: []string{"update", "-A", "baganator-mirror"}},
		{name: "update/all_no_profiles", args: []string{"update", "-A"}},
		{name: "update/all_ignores_env", setup: [][]string{addForever, addRetail, {"add", "-p", "forever", "github:austinrr/baganator-mirror"}, addRetailOnly}, env: map[string]string{"KNAPSACK_PROFILE": "retail"},
			args: []string{"update", "-A"}},

		// check
		{name: "check/help", args: []string{"check", "--help"}},
		{name: "check/no_addons", setup: [][]string{addForever}, args: []string{"check"}},
		{name: "check/up_to_date", setup: [][]string{addForever, addBaganator}, args: []string{"check"}},
		{name: "check/pending", setup: [][]string{addForever, addBaganator, addDetails},
			after: func(s *sandbox) { bumpBaganator(s); pruneDetails(s) }, args: []string{"check"}},
		{name: "check/json", setup: [][]string{addForever, addBaganator}, after: bumpBaganator,
			args: []string{"check", "-o", "json"}},
		{name: "check/failure", config: missingAndGone, args: []string{"check"}},
		{name: "check/unknown_name", setup: [][]string{addForever, addBaganator}, args: []string{"check", "nope"}},
		{name: "check/all", setup: bothProfiles, after: bumpBaganator, args: []string{"check", "-A"}},
		{name: "check/all_with_profile", setup: [][]string{addForever}, args: []string{"check", "-A", "-p", "forever"}},

		// list -A
		{name: "list/all", setup: bothProfiles, args: []string{"list", "-A"}},
		{name: "list/all_json", setup: bothProfiles, args: []string{"list", "-A", "-o", "json"}},
		{name: "list/all_empty", setup: [][]string{addForever, addRetail}, args: []string{"list", "-A"}},
		{name: "list/all_with_profile", setup: [][]string{addForever}, args: []string{"list", "-A", "-p", "forever"}},
	})
}
