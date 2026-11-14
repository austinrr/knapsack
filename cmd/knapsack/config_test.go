package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/austinrr/knapsack/internal/lock"
	"github.com/austinrr/knapsack/internal/paths"
)

const (
	forever = "{home}/wow/_forever_/Interface/AddOns"
	retail  = "{home}/wow/_retail_/Interface/AddOns"
	classic = "{home}/wow/_classic_/Interface/AddOns"
	plain   = "{home}/addons"
	ptr     = "{home}/wow/_ptr_/Interface/AddOns"
	beta    = "{home}/wow/_classic_beta_/Interface/AddOns"
)

// Setup steps shared by many cases.
var (
	addForever = []string{"config", "set-profile", "forever", "--path", forever}
	addRetail  = []string{"config", "set-profile", "retail", "--path", retail}
)

// configWithKey has an API key, to check that view redacts it.
const configWithKey = `current_profile = "forever"

[profiles.forever]
path = "{home}/wow/_forever_/Interface/AddOns"
flavor = "forever"
addons = [
  "github:austinrr/baganator-mirror",
  "wago:VBNBxKx5",
]

[sources.curseforge]
api_key = "secret-key"
`

func TestConfigCommands(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		{name: "config/help", args: []string{"config"}},
		{name: "config/unknown_subcommand", args: []string{"config", "set-profil"}},

		// set-profile
		{name: "config/set_profile_first", args: addForever},
		{name: "config/set_profile_second", setup: [][]string{addForever}, args: addRetail},
		{name: "config/set_profile_modify_path",
			setup: [][]string{{"config", "set-profile", "main", "--path", forever}},
			args:  []string{"config", "set-profile", "main", "--path", retail}},
		{name: "config/set_profile_keeps_flavor",
			setup: [][]string{{"config", "set-profile", "custom", "--path", plain, "--flavor", "classic_era"}},
			args:  []string{"config", "set-profile", "custom", "--path", "{home}/wow"}},
		{name: "config/set_profile_explicit_flavor",
			args: []string{"config", "set-profile", "custom", "--path", plain, "--flavor", "anniversary"}},
		// Without a name, the profile is the one using the path, or is
		// named after the flavor.
		{name: "config/set_profile_no_name", args: []string{"config", "set-profile", "--path", forever}},
		{name: "config/set_profile_no_name_second", setup: [][]string{addForever}, args: []string{"config", "set-profile", "--path", retail}},
		{name: "config/set_profile_no_name_existing_path", setup: [][]string{{"config", "set-profile", "main", "--path", forever}},
			args: []string{"config", "set-profile", "--path", forever}},
		{name: "config/set_profile_no_name_change_flavor", setup: [][]string{{"config", "set-profile", "test", "--path", plain, "--flavor", "forever"}},
			args: []string{"config", "set-profile", "--path", plain, "--flavor", "classic"}},
		{name: "config/set_profile_no_name_taken", setup: [][]string{addForever},
			args: []string{"config", "set-profile", "--path", plain, "--flavor", "forever"}},
		{name: "config/set_profile_no_name_no_path", args: []string{"config", "set-profile"}},
		{name: "config/set_profile_no_name_no_flavor", args: []string{"config", "set-profile", "--path", plain}},
		// No example to show, so the hint only points at --help.
		{name: "config/use_profile_no_name", args: []string{"config", "use-profile"}},
		{name: "config/set_profile_two_names", args: []string{"config", "set-profile", "a", "b"}},
		{name: "config/set_profile_new_without_path", args: []string{"config", "set-profile", "forever"}},
		{name: "config/set_profile_not_a_dir",
			args: []string{"config", "set-profile", "forever", "--path", "{home}/missing"}},
		{name: "config/set_profile_flavor_not_inferable",
			args: []string{"config", "set-profile", "custom", "--path", plain}},
		{name: "config/set_profile_flavor_mismatch",
			args: []string{"config", "set-profile", "forever", "--path", forever, "--flavor", "retail"}},
		{name: "config/set_profile_unknown_flavor",
			args: []string{"config", "set-profile", "custom", "--path", plain, "--flavor", "classic_beta"}},
		{name: "config/set_profile_bad_name",
			args: []string{"config", "set-profile", "Forever", "--path", forever}},
		{name: "config/set_profile_unchanged", setup: [][]string{addForever},
			args: []string{"config", "set-profile", "forever"}},
		// Changing an existing profile never makes it current, even when
		// none is.
		{name: "config/set_profile_modify_not_current",
			setup: [][]string{addForever, addRetail, {"config", "delete-profile", "forever"}},
			args:  []string{"config", "set-profile", "retail", "--path", classic}},
		{name: "config/set_profile_same_path", setup: [][]string{addForever},
			args: []string{"config", "set-profile", "again", "--path", forever + "/"}},
		// --path can be the client folder or its Interface folder, and the
		// game folder lists its clients.
		{name: "config/set_profile_client_folder", args: []string{"config", "set-profile", "--path", "{home}/wow/_forever_"}},
		{name: "config/set_profile_interface_folder", args: []string{"config", "set-profile", "--path", "{home}/wow/_forever_/Interface"}},
		{name: "config/set_profile_game_folder", args: []string{"config", "set-profile", "--path", "{home}/wow"}},
		// An unquoted path split by the shell, with and without a name.
		{name: "config/set_profile_split_path", args: []string{"config", "set-profile", "--path", "C:/Program", "Files", "(x86)/World", "of", "Warcraft/_retail_"}},
		{name: "config/set_profile_split_path_named", args: []string{"config", "set-profile", "retail", "--path", "/games/World", "of", "Warcraft/_retail_"}},
		// "of" is a valid profile name, but it's part of the path here.
		{name: "config/set_profile_split_path_of", args: []string{"config", "set-profile", "--path", "/games/World", "of", "Warcraft/_retail_"}},
		{name: "config/set_profile_split_path_flag_after", args: []string{"config", "set-profile", "--path", "/games/World", "of", "Warcraft", "--flavor", "retail"}},
		// A name after a path that exists isn't a split.
		{name: "config/set_profile_name_after_path", args: []string{"config", "set-profile", "--path", forever, "main"}},
		// A client that's never been started has no AddOns folder yet.
		{name: "config/set_profile_client_without_addons", after: noAddOns("_xptr_"), args: []string{"config", "set-profile", "--path", "{home}/wow/_xptr_"}},
		// A test client is its live client's flavor.
		{name: "config/set_profile_ptr", setup: [][]string{addRetail}, args: []string{"config", "set-profile", "retail-ptr", "--path", ptr}},
		// A test folder's flavor is only a default: Blizzard has used
		// _classic_beta_ for more than one game.
		{name: "config/set_profile_ptr_explicit_flavor", args: []string{"config", "set-profile", "ptr", "--path", ptr, "--flavor", "forever"}},
		// Without a name, a test client is named after its flavor and
		// itself, leaving the flavor's name for the live client.
		{name: "config/set_profile_ptr_no_name", args: []string{"config", "set-profile", "--path", ptr}},
		{name: "config/set_profile_retail_after_ptr", setup: [][]string{{"config", "set-profile", "--path", ptr}},
			args: []string{"config", "set-profile", "--path", retail}},
		{name: "config/set_profile_beta_other_flavor", args: []string{"config", "set-profile", "--path", beta, "--flavor", "anniversary"}},
		// A stored flavor stands: changing the profile doesn't re-infer it
		// from a test folder, so a beta profile made with --flavor keeps it.
		{name: "config/set_profile_beta_keeps_flavor", setup: [][]string{{"config", "set-profile", "--path", beta, "--flavor", "anniversary"}},
			args: []string{"config", "set-profile", "--path", beta}},

		// get-profiles
		{name: "config/get_profiles_empty", args: []string{"config", "get-profiles"}},
		{name: "config/get_profiles", setup: [][]string{addForever, addRetail},
			args: []string{"config", "get-profiles"}},
		{name: "config/get_profiles_json", config: configWithKey, setup: [][]string{addRetail},
			args: []string{"config", "get-profiles", "-o", "json"}},
		{name: "config/get_profiles_json_empty", args: []string{"config", "get-profiles", "-o", "json"}},
		{name: "config/get_profiles_bad_output", args: []string{"config", "get-profiles", "-o", "yaml"}},
		{name: "config/get_profiles_extra_arg", args: []string{"config", "get-profiles", "forever"}},

		// use-profile
		{name: "config/use_profile", setup: [][]string{addForever, addRetail},
			args: []string{"config", "use-profile", "retail"}},
		{name: "config/use_profile_unknown", setup: [][]string{addForever},
			args: []string{"config", "use-profile", "retial"}},

		// current-profile
		{name: "config/current_profile", setup: [][]string{addForever, addRetail},
			args: []string{"config", "current-profile"}},
		{name: "config/current_profile_no_profiles", args: []string{"config", "current-profile"}},
		{name: "config/current_profile_none",
			setup: [][]string{addForever, addRetail, {"config", "delete-profile", "forever"}},
			args:  []string{"config", "current-profile"}},

		// delete-profile
		{name: "config/delete_profile", setup: [][]string{addForever, addRetail},
			args: []string{"config", "delete-profile", "retail"}},
		{name: "config/delete_profile_current", setup: [][]string{addForever},
			args: []string{"config", "delete-profile", "forever"}},
		{name: "config/delete_profile_unknown", args: []string{"config", "delete-profile", "nope"}},

		// view
		{name: "config/view", config: configWithKey, setup: [][]string{addRetail},
			args: []string{"config", "view"}},
		{name: "config/view_json", config: configWithKey, args: []string{"config", "view", "-o", "json"}},
		{name: "config/view_empty", args: []string{"config", "view"}},

		// Global flags and broken config.
		{name: "config/config_flag",
			setup: [][]string{{"--config", "{home}/other.toml", "config", "set-profile", "elsewhere", "--path", classic}},
			args:  []string{"--config", "{home}/other.toml", "config", "get-profiles"}},
		{name: "config/config_flag_default_untouched",
			setup: [][]string{{"--config", "{home}/other.toml", "config", "set-profile", "elsewhere", "--path", classic}},
			args:  []string{"config", "get-profiles"}},
		{name: "config/invalid_config", config: "[profiles.forever]\nflavour = \"forever\"\n",
			args: []string{"config", "get-profiles"}},
	})
}

// Commands that change config fail with a hint while another process holds
// the config lock; -v adds the cause.
func TestConfigLocked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"config/locked", []string{"config", "set-profile", "forever", "--path", forever}},
		{"config/locked_verbose", []string{"-v", "config", "set-profile", "forever", "--path", forever}},
		{"config/profile_locked", []string{"config", "delete-profile", "forever"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSandbox(t)
			p, err := paths.Resolve(func(string) string { return s.home })
			if err != nil {
				t.Fatal(err)
			}
			path := p.ConfigLock()
			if tc.name == "config/profile_locked" {
				s.mustRun(addForever...)
				path = p.ProfileLock("forever")
			}
			l, err := lock.Acquire(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = l.Release() }()
			checkGolden(t, tc.name, s.run(tc.args...))
		})
	}
}

// noAddOns adds a client folder to the sandbox's game folder with no
// Interface/AddOns in it, as a client that was never started has.
func noAddOns(client string) func(*sandbox) {
	return func(s *sandbox) {
		if err := os.MkdirAll(filepath.Join(s.home, "wow", client), 0o700); err != nil {
			s.t.Fatal(err)
		}
	}
}
