package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	addForever = []string{"profiles", "create", "forever", "--path", forever}
	addRetail  = []string{"profiles", "create", "retail", "--path", retail}
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
		{name: "config/unknown_subcommand", args: []string{"config", "set-profile"}},
		{name: "profiles/help", args: []string{"profiles"}},
		{name: "profiles/unknown_subcommand", args: []string{"profiles", "craete"}},

		// create and set
		{name: "profiles/create_first", args: addForever},
		{name: "profiles/create_second", setup: [][]string{addForever}, args: addRetail},
		{name: "profiles/set_path",
			setup: [][]string{{"profiles", "create", "main", "--path", forever}},
			args:  []string{"profiles", "set", "main", "--path", retail}},
		{name: "profiles/set_game_folder",
			setup: [][]string{{"profiles", "create", "custom", "--path", plain, "--flavor", "classic_era"}},
			args:  []string{"profiles", "set", "custom", "--path", "{home}/wow"}},
		{name: "profiles/set_flavor", setup: [][]string{{"profiles", "create", "test", "--path", plain, "--flavor", "forever"}},
			args: []string{"profiles", "set", "test", "--flavor", "classic"}},
		{name: "profiles/set_unchanged", setup: [][]string{addForever}, args: []string{"profiles", "set", "forever", "--path", forever}},
		{name: "profiles/set_unknown", args: []string{"profiles", "set", "nope", "--path", forever}},
		{name: "profiles/set_nothing", setup: [][]string{addForever}, args: []string{"profiles", "set", "forever"}},
		{name: "profiles/set_no_name", args: []string{"profiles", "set", "--flavor", "retail"}},
		{name: "profiles/set_taken_path", setup: [][]string{addForever, addRetail}, args: []string{"profiles", "set", "retail", "--path", forever}},
		{name: "profiles/create_explicit_flavor",
			args: []string{"profiles", "create", "custom", "--path", plain, "--flavor", "anniversary"}},
		// Without a name, the profile is the one using the path, or is
		// named after the flavor.
		{name: "profiles/create_no_name", args: []string{"profiles", "create", "--path", forever}},
		{name: "profiles/create_no_name_second", setup: [][]string{addForever}, args: []string{"profiles", "create", "--path", retail}},
		// A folder that has a profile already isn't made another.
		{name: "profiles/create_existing_path", setup: [][]string{{"profiles", "create", "main", "--path", forever}},
			args: []string{"profiles", "create", "--path", forever}},
		{name: "profiles/create_no_name_taken", setup: [][]string{addForever},
			args: []string{"profiles", "create", "--path", plain, "--flavor", "forever"}},
		{name: "profiles/create_no_name_no_path", args: []string{"profiles", "create"}},
		{name: "profiles/create_no_name_no_flavor", args: []string{"profiles", "create", "--path", plain}},
		// No example to show, so the hint only points at --help.
		{name: "profiles/use_no_name", args: []string{"profiles", "use"}},
		{name: "profiles/create_two_names", args: []string{"profiles", "create", "a", "b"}},
		{name: "profiles/create_new_without_path", args: []string{"profiles", "create", "forever"}},
		{name: "profiles/create_not_a_dir",
			args: []string{"profiles", "create", "forever", "--path", "{home}/missing"}},
		{name: "profiles/create_flavor_not_inferable",
			args: []string{"profiles", "create", "custom", "--path", plain}},
		{name: "profiles/create_flavor_mismatch",
			args: []string{"profiles", "create", "forever", "--path", forever, "--flavor", "retail"}},
		{name: "profiles/create_unknown_flavor",
			args: []string{"profiles", "create", "custom", "--path", plain, "--flavor", "classic_beta"}},
		{name: "profiles/create_bad_name",
			args: []string{"profiles", "create", "Forever", "--path", forever}},
		{name: "profiles/create_exists", setup: [][]string{addForever},
			args: []string{"profiles", "create", "forever", "--path", retail}},
		// Changing an existing profile never makes it current, even when
		// none is.
		{name: "profiles/set_not_current",
			setup: [][]string{addForever, addRetail, {"profiles", "delete", "forever"}},
			args:  []string{"profiles", "set", "retail", "--path", classic}},
		{name: "profiles/create_same_path", setup: [][]string{addForever},
			args: []string{"profiles", "create", "again", "--path", forever + "/"}},
		// --path can be the client folder or its Interface folder, and the
		// game folder lists its clients.
		{name: "profiles/create_client_folder", args: []string{"profiles", "create", "--path", "{home}/wow/_forever_"}},
		{name: "profiles/create_interface_folder", args: []string{"profiles", "create", "--path", "{home}/wow/_forever_/Interface"}},
		{name: "profiles/create_game_folder", args: []string{"profiles", "create", "--path", "{home}/wow"}},
		// An unquoted path split by the shell, with and without a name.
		{name: "profiles/create_split_path", args: []string{"profiles", "create", "--path", "C:/Program", "Files", "(x86)/World", "of", "Warcraft/_retail_"}},
		{name: "profiles/create_split_path_named", args: []string{"profiles", "create", "retail", "--path", "/games/World", "of", "Warcraft/_retail_"}},
		// "of" is a valid profile name, but it's part of the path here.
		{name: "profiles/create_split_path_of", args: []string{"profiles", "create", "--path", "/games/World", "of", "Warcraft/_retail_"}},
		{name: "profiles/create_split_path_flag_after", args: []string{"profiles", "create", "--path", "/games/World", "of", "Warcraft", "--flavor", "retail"}},
		// A name after a path that exists isn't a split.
		{name: "profiles/create_name_after_path", args: []string{"profiles", "create", "--path", forever, "main"}},
		// A client that's never been started has no AddOns folder yet.
		{name: "profiles/create_client_without_addons", after: noAddOns("_xptr_"), args: []string{"profiles", "create", "--path", "{home}/wow/_xptr_"}},
		// --auto makes a profile per client folder with AddOns in it.
		{name: "profiles/create_auto", after: noAddOns("_xptr_"), args: []string{"profiles", "create", "--auto", "--path", "{home}/wow"}},
		// A quoted ~ path, which the shell leaves alone, then a name.
		{name: "profiles/create_tilde_then_name", args: []string{"profiles", "create", "--path", "~/wow/_retail_", "main"}},
		{name: "profiles/create_auto_again", setup: [][]string{{"profiles", "create", "--auto", "--path", "{home}/wow"}},
			args: []string{"profiles", "create", "--auto", "--path", "{home}/wow"}},
		{name: "profiles/create_auto_name_taken", setup: [][]string{{"profiles", "create", "retail", "--path", plain, "--flavor", "retail"}},
			args: []string{"profiles", "create", "--auto", "--path", "{home}/wow"}},
		// The current profile is a live client's, though a test client
		// comes first.
		{name: "profiles/create_auto_live_first", after: game("wow2", "_classic_beta_", "_forever_"), args: []string{"profiles", "create", "--auto", "--path", "{home}/wow2"}},
		// Every client skipped is an error, after saying why.
		{name: "profiles/create_auto_all_skipped", after: func(s *sandbox) {
			if err := os.MkdirAll(filepath.Join(s.home, "fresh", "_retail_"), 0o700); err != nil {
				s.t.Fatal(err)
			}
		}, args: []string{"profiles", "create", "--auto", "--path", "{home}/fresh"}},
		{name: "profiles/create_auto_not_a_game", args: []string{"profiles", "create", "--auto", "--path", plain}},
		{name: "profiles/create_auto_with_name", args: []string{"profiles", "create", "retail", "--auto"}},
		{name: "profiles/create_auto_with_flavor", args: []string{"profiles", "create", "--auto", "--flavor", "retail"}},
		// A test client is its live client's flavor.
		{name: "profiles/create_ptr", setup: [][]string{addRetail}, args: []string{"profiles", "create", "retail-ptr", "--path", ptr}},
		// A test folder's flavor is only a default: Blizzard has used
		// _classic_beta_ for more than one game.
		{name: "profiles/create_ptr_explicit_flavor", args: []string{"profiles", "create", "ptr", "--path", ptr, "--flavor", "forever"}},
		// Without a name, a test client is named after its flavor and
		// itself, leaving the flavor's name for the live client.
		{name: "profiles/create_ptr_no_name", args: []string{"profiles", "create", "--path", ptr}},
		{name: "profiles/create_retail_after_ptr", setup: [][]string{{"profiles", "create", "--path", ptr}},
			args: []string{"profiles", "create", "--path", retail}},
		{name: "profiles/create_beta_other_flavor", args: []string{"profiles", "create", "--path", beta, "--flavor", "anniversary"}},
		// A stored flavor stands: changing the profile doesn't re-infer it
		// from a test folder, so a beta profile made with --flavor keeps it.
		{name: "profiles/set_beta_keeps_flavor", setup: [][]string{{"profiles", "create", "--path", beta, "--flavor", "anniversary"}},
			args: []string{"profiles", "set", "anniversary-beta", "--path", beta}},

		// list
		{name: "profiles/list_empty", args: []string{"profiles", "list"}},
		{name: "profiles/list", setup: [][]string{addForever, addRetail},
			args: []string{"profiles", "list"}},
		{name: "profiles/list_wide", setup: [][]string{addForever, addRetail},
			args: []string{"profiles", "list", "-o", "wide"}},
		{name: "profiles/list_json", config: configWithKey, setup: [][]string{addRetail},
			args: []string{"profiles", "list", "-o", "json"}},
		{name: "profiles/list_json_empty", args: []string{"profiles", "list", "-o", "json"}},
		{name: "profiles/list_bad_output", args: []string{"profiles", "list", "-o", "yaml"}},
		{name: "profiles/list_extra_arg", args: []string{"profiles", "list", "forever"}},

		// use
		{name: "profiles/use", setup: [][]string{addForever, addRetail},
			args: []string{"profiles", "use", "retail"}},
		{name: "profiles/use_unknown", setup: [][]string{addForever},
			args: []string{"profiles", "use", "retial"}},

		// current
		{name: "profiles/current", setup: [][]string{addForever, addRetail},
			args: []string{"profiles", "current"}},
		{name: "profiles/current_no_profiles", args: []string{"profiles", "current"}},
		{name: "profiles/current_none",
			setup: [][]string{addForever, addRetail, {"profiles", "delete", "forever"}},
			args:  []string{"profiles", "current"}},

		// delete
		{name: "profiles/delete", setup: [][]string{addForever, addRetail},
			args: []string{"profiles", "delete", "retail"}},
		{name: "profiles/delete_current", setup: [][]string{addForever},
			args: []string{"profiles", "delete", "forever"}},
		{name: "profiles/delete_unknown", args: []string{"profiles", "delete", "nope"}},

		// view
		{name: "config/view", config: configWithKey, setup: [][]string{addRetail},
			args: []string{"config", "view"}},
		{name: "config/view_json", config: configWithKey, args: []string{"config", "view", "-o", "json"}},
		{name: "config/view_empty", args: []string{"config", "view"}},

		// Global flags and broken config.
		{name: "profiles/config_flag",
			setup: [][]string{{"--config", "{home}/other.toml", "profiles", "create", "elsewhere", "--path", classic}},
			args:  []string{"--config", "{home}/other.toml", "profiles", "list"}},
		{name: "profiles/config_flag_default_untouched",
			setup: [][]string{{"--config", "{home}/other.toml", "profiles", "create", "elsewhere", "--path", classic}},
			args:  []string{"profiles", "list"}},
		{name: "profiles/invalid_config", config: "[profiles.forever]\nflavour = \"forever\"\n",
			args: []string{"profiles", "list"}},
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
		{"profiles/locked", []string{"profiles", "create", "forever", "--path", forever}},
		{"profiles/locked_verbose", []string{"-v", "profiles", "create", "forever", "--path", forever}},
		{"profiles/profile_locked", []string{"profiles", "delete", "forever"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newSandbox(t)
			p, err := paths.Resolve(func(string) string { return s.home })
			if err != nil {
				t.Fatal(err)
			}
			path := p.ConfigLock()
			if tc.name == "profiles/profile_locked" {
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

// game makes a game folder in the sandbox with these clients, each with
// Interface/AddOns.
func game(name string, clients ...string) func(*sandbox) {
	return func(s *sandbox) {
		for _, c := range clients {
			if err := os.MkdirAll(filepath.Join(s.home, name, c, "Interface", "AddOns"), 0o700); err != nil {
				s.t.Fatal(err)
			}
		}
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

// Without --path, --auto looks where launchers put the game: here a Lutris
// prefix in home. Only on Linux, where Wine prefixes are where it looks,
// and home is the sandbox, so the real one is never searched.
func TestAutoProfilesSearch(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("looks in Wine prefixes on Linux only")
	}
	s := newSandbox(t)
	if r := s.run("profiles", "create", "--auto"); r.Code != exitError || !strings.Contains(r.Stderr, "couldn't find World of Warcraft") {
		t.Errorf("with no game installed:\n%s", r)
	}
	// Two installs, as with Lutris and Steam: each has _classic_beta_, so
	// the second copy is skipped and gets a command to name it.
	lutris := filepath.Join("Games", "battlenet", "drive_c", "Program Files (x86)", "World of Warcraft")
	steam := filepath.Join(".steam", "steam", "steamapps", "compatdata", "123", "pfx", "drive_c", "Program Files (x86)", "World of Warcraft")
	game(lutris, "_classic_beta_", "_forever_")(s)
	game(steam, "_retail_", "_classic_beta_")(s)
	r := s.run("profiles", "create", "--auto", "-v")
	if !strings.Contains(r.Stderr, "Looked for World of Warcraft in:") {
		t.Errorf("-v doesn't list where it looked:\n%s", r)
	}
	r.Stderr = "" // the places looked are tested in discover
	checkGolden(t, "profiles/create_auto_two_installs", r)
}
