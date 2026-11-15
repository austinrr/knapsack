package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/discover"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

func newProfilesCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "profiles",
		Aliases: []string{"profile"},
		Short:   "Create, list and switch profiles",
		Long: `Create, list and switch profiles.

A profile is one game client's AddOns folder, its flavor, and the addons
you want in it. Addon commands act on the current profile; -p names
another, and -A acts on every profile, as kubectl's -n and -A do for
namespaces.

Profiles are kept in config.toml, which commands that change it rewrite
in a fixed layout, so comments in the file are not kept.`,
	}
	return c.groupCmd(cmd,
		newProfilesListCmd(c),
		newProfilesCreateCmd(c),
		newProfilesSetCmd(c),
		newProfilesEditCmd(c),
		newProfilesUseCmd(c),
		newProfilesCurrentCmd(c),
		newProfilesDeleteCmd(c),
	)
}

func newProfilesCreateCmd(c *cli) *cobra.Command {
	req := app.SetProfileRequest{Must: app.MustBeNew}
	var auto bool
	cmd := &cobra.Command{
		Use:   "create [name] --path <game client folder> [--flavor <flavor>] | --auto [--path <game folder>]",
		Short: "Create a profile, or one for each client --auto finds",
		Long: `Create a profile: one game client's AddOns folder.

The easy way is --auto: it finds World of Warcraft and makes a profile for
each client in it. It leaves existing profiles alone and skips clients
that have never been started. To search one game folder, add --path; -v
lists where it looks.

To set up one client, pass its folder, such as _retail_, or its
Interface/AddOns folder, with --path. Quote it, since the usual path has
spaces in it.

Name: leave it off, and the profile is named after its flavor (retail,
forever), plus the test for a test client (retail-ptr). Only a second
copy of the same client needs a name of its own.

Flavor: read from the client folder. Test clients such as _ptr_ and
_classic_beta_ get their live client's flavor; pass --flavor to choose
another, or for a folder that doesn't show it.

A profile that already exists is refused: change it with knapsack
profiles set. The first profile you make becomes the current one.`,
		Example: "  knapsack profiles create --auto\n" +
			"  knapsack profiles create --path " + app.Quoted(app.ExampleClientFolder()) + "\n" +
			"  knapsack profiles create retail-test --path " + app.Quoted(filepath.Join(filepath.Dir(filepath.Dir(app.ExampleClientFolder())), "WoW Test", "_retail_")),
		Args: func(cmd *cobra.Command, args []string) error {
			if err := splitPath(c.rawArgs, c.env.home); err != nil {
				return err
			}
			if auto {
				switch {
				case len(args) > 0:
					return fmt.Errorf("--auto names each profile itself, so it takes no name, got %q", args[0])
				case req.Flavor != "":
					return errors.New("--auto works out each profile's flavor, so it can't be used with --flavor")
				}
			}
			return maxArgs("profile name")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if auto {
				return autoProfiles(cmd, c, req.Path)
			}
			if len(args) == 1 {
				req.Name = args[0]
			}
			res, err := c.app.SetProfile(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			out.printf("Profile %q created (%s, %s).\n", res.Profile.Name, res.Profile.Flavor, res.Profile.Path)
			if res.MadeCurrent {
				out.printf("Switched to profile %q.\n", res.Profile.Name)
			}
			if out.err != nil {
				return out.err
			}
			if res.MadeCurrent {
				// The first profile: say what to do next.
				_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Next, add addons: knapsack add github:owner/repo, or a GitHub, CurseForge or Wago link.")
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "the game's client folder, such as _retail_, or its Interface/AddOns folder")
	cmd.Flags().BoolVar(&auto, "auto", false, "find World of Warcraft and make a profile for each of its clients")
	addProfileFlags(cmd, &req)
	return cmd
}

// addProfileFlags adds the flags create and set share, beyond --path,
// whose help differs.
func addProfileFlags(cmd *cobra.Command, req *app.SetProfileRequest) {
	cmd.Flags().StringVar(&req.Flavor, "flavor", "", "the game client, if the path doesn't show it")
	_ = cmd.RegisterFlagCompletionFunc("flavor", cobra.FixedCompletions(flavor.Names(), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagDirname("path")
}

func newProfilesSetCmd(c *cli) *cobra.Command {
	req := app.SetProfileRequest{Must: app.MustExist}
	cmd := &cobra.Command{
		Use:   "set <name> [--path <game client folder>] [--flavor <flavor>]",
		Short: "Change a profile's folder or flavor",
		Long: `Change a profile's AddOns folder or flavor. Pass only what changes; a
new folder's flavor is read from it, as for create.

The profile keeps its addons and Knapsack's record of what it installed,
which name folders inside AddOns, not where it is. So it's for a game that
moved with its addons, or a folder given wrongly before anything was
installed. For another copy of the game, create a profile of its own.`,
		Example: "  knapsack profiles set retail --path " + app.Quoted(app.ExampleClientFolder()) + "\n" +
			"  knapsack profiles set retail-test --flavor retail",
		Args: func(cmd *cobra.Command, args []string) error {
			if err := splitPath(c.rawArgs, c.env.home); err != nil {
				return err
			}
			if err := exactArgs("profile name")(cmd, args); err != nil {
				return err
			}
			if !cmd.Flags().Changed("path") && !cmd.Flags().Changed("flavor") {
				return missingArgs(cmd, "profiles set needs --path or --flavor: what to change")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[0]
			res, err := c.app.SetProfile(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			verb := "changed"
			if !res.Changed {
				verb = "unchanged"
			}
			out.printf("Profile %q %s (%s, %s).\n", res.Profile.Name, verb, res.Profile.Flavor, res.Profile.Path)
			return out.err
		},
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "the game's client folder, such as _retail_, or its Interface/AddOns folder")
	addProfileFlags(cmd, &req)
	return cmd
}

// autoProfiles runs profiles create --auto: where the game was found, a table
// of what was done with each client, and how to add any that were skipped.
// game is --path, the game folder to use, or "" to look for it.
func autoProfiles(cmd *cobra.Command, c *cli, game string) error {
	res, err := c.app.AutoProfiles(cmd.Context(), game)
	if c.verbose && len(res.Searched) > 0 {
		w := cmd.ErrOrStderr()
		_, _ = fmt.Fprintln(w, "Looked for World of Warcraft in:") // -v output; nowhere to report a failed write
		for _, s := range res.Searched {
			_, _ = fmt.Fprintln(w, "  "+s)
		}
	}
	// What was done is shown even when it ends in an error: profiles made
	// before a failure, or why every client was skipped.
	out := &printer{w: cmd.OutOrStdout()}
	printAutoProfiles(out, res.Profiles, c.env.home)
	if err != nil {
		return err
	}
	if out.err != nil {
		return out.err
	}
	if slices.ContainsFunc(res.Profiles, func(p app.AutoProfile) bool { return p.Result.MadeCurrent }) {
		_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Next, add addons: knapsack add github:owner/repo, or a GitHub, CurseForge or Wago link.")
		return err
	}
	return nil
}

// printAutoProfiles writes profiles create --auto's report. With more than one
// game folder, each is numbered, and the table names clients by number,
// so no long path is repeated.
func printAutoProfiles(out *printer, profiles []app.AutoProfile, home string) {
	var games []string
	for _, p := range profiles {
		if !slices.Contains(games, p.Game) {
			games = append(games, p.Game)
		}
	}
	if len(games) == 0 {
		return
	}
	// label is how the table names a client: "_retail_", or "[2] _retail_".
	label := func(p app.AutoProfile) string { return p.Client }
	if len(games) == 1 {
		out.printf("Found World of Warcraft in %s\n\n", shortPath(games[0], home))
	} else {
		out.printf("Found World of Warcraft in %d places:\n", len(games))
		for i, g := range games {
			out.printf("  [%d] %s\n", i+1, shortPath(g, home))
		}
		out.printf("\n")
		label = func(p app.AutoProfile) string {
			return fmt.Sprintf("[%d] %s", slices.Index(games, p.Game)+1, p.Client)
		}
	}
	// inGame names the client whose AddOns folder addons is, as label
	// does, or "" when it's none of those found.
	inGame := func(addons string) string {
		for _, p := range profiles {
			if sameFolder(filepath.Join(p.Game, p.Client, "Interface", "AddOns"), addons) {
				return label(p)
			}
		}
		return ""
	}

	tw := newTable(out.w)
	row := &printer{w: tw}
	row.printf("PROFILE\tCLIENT\tRESULT\n")
	var current string
	var copies, unstarted, failed []app.AutoProfile
	for _, p := range profiles {
		name, result := "-", ""
		switch p.Skip {
		case app.NotSkipped:
			name, result = p.Result.Profile.Name, "already set up"
			if p.Result.Created {
				result = "created"
			}
			if p.Result.MadeCurrent {
				current = name
			}
		case app.SkipNoAddOns:
			result = "skipped: never started, no Interface/AddOns yet"
			unstarted = append(unstarted, p)
		case app.SkipNameTaken:
			var taken *app.NameTakenError
			_ = errors.As(p.Err, &taken) // always there for SkipNameTaken
			where := inGame(taken.Path)
			if where == "" {
				where = shortPath(taken.Path, home)
			}
			result = fmt.Sprintf("skipped: %q already uses %s", taken.Name, where)
			copies = append(copies, p)
		case app.SkipFailed:
			// The message can hold a long path, so it goes below.
			result = "skipped: see below"
			failed = append(failed, p)
		}
		row.printf("%s\t%s\t%s\n", name, label(p), result)
	}
	if err := tw.Flush(); err != nil && row.err == nil {
		row.err = err
	}
	if row.err != nil && out.err == nil {
		out.err = row.err
	}

	if current != "" {
		out.printf("\nSwitched to profile %q.\n", current)
	}
	if len(copies) > 0 {
		out.printf("\nA second copy of a client needs its own profile name:\n")
		for _, p := range copies {
			out.printf("  knapsack profiles create <name> --path %s\n", app.Quoted(hintPath(filepath.Join(p.Game, p.Client), home)))
		}
		out.printf("Or move the existing profile to that copy with knapsack profiles set <name> --path ….\n")
	}
	if len(unstarted) > 0 {
		out.printf("\nStart a never-started client once, or make its Interface/AddOns folder,\nthen run this again.\n")
	}
	for _, p := range failed {
		out.printf("\n%s: %s\n", label(p), p.Err)
		var ue *app.UserError
		if errors.As(p.Err, &ue) && ue.Hint != "" {
			out.printf("  hint: %s\n", ue.Hint)
		}
	}
}

// sameFolder reports whether a and b are the same path, ignoring case only
// where the filesystem does, on Windows and macOS.
func sameFolder(a, b string) bool {
	return sameName(filepath.Clean(a), filepath.Clean(b))
}

// hintPath is a path for a command to copy, quoted: one under home starts
// with $HOME, which shells expand inside double quotes, unlike ~.
func hintPath(path, home string) string {
	if short := shortPath(path, home); short != path {
		return "$HOME" + strings.TrimPrefix(short, "~")
	}
	return path
}

// shortPath shows a path under home as ~/…, which --path accepts too.
// Windows paths are left alone: ~ isn't how anyone writes them there.
func shortPath(path, home string) string {
	if home == "" || runtime.GOOS == "windows" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || rest[0] == '/') {
		return "~" + rest
	}
	return path
}

// splitPath catches a --path the shell split at its spaces, as an unquoted
// C:\Program Files (x86)\… is in cmd.exe. It reads the raw arguments,
// since only their order shows which words followed --path's value: those
// up to the next flag. They're the rest of the path unless the value is a
// folder on its own, as with --path …\AddOns retail, a name after the path.
func splitPath(raw []string, home string) error {
	value, rest, ok := afterFlag(raw, "--path")
	if !ok || len(rest) == 0 {
		return nil
	}
	if info, err := os.Stat(app.ExpandHome(value, home)); err == nil && info.IsDir() {
		return nil
	}
	joined := strings.Join(append([]string{value}, rest...), " ")
	return &app.UserError{
		Msg:  "the path looks split at its spaces: " + joined,
		Hint: "put it in quotes: --path " + app.Quoted(joined),
	}
}

// afterFlag finds flag in raw (as "--flag value" or "--flag=value") and
// returns its value and the words after it, up to the next flag or "--".
func afterFlag(raw []string, flag string) (value string, rest []string, ok bool) {
	for i, a := range raw {
		var after []string
		switch {
		case a == flag && i+1 < len(raw):
			value, after = raw[i+1], raw[i+2:]
		case strings.HasPrefix(a, flag+"="):
			value, after = strings.TrimPrefix(a, flag+"="), raw[i+1:]
		default:
			continue
		}
		for _, w := range after {
			if strings.HasPrefix(w, "-") {
				break
			}
			rest = append(rest, w)
		}
		return value, rest, true
	}
	return "", nil, false
}

// profileJSON is the -o json shape of a profile.
type profileJSON struct {
	Name    string   `json:"name"`
	Current bool     `json:"current"`
	Flavor  string   `json:"flavor"`
	Path    string   `json:"path"`
	Addons  []string `json:"addons"`
}

func refStrings(refs []ref.Ref) []string {
	out := make([]string, len(refs)) // never nil, so JSON shows [] not null
	for i, r := range refs {
		out[i] = r.String()
	}
	return out
}

func newProfilesListCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List profiles",
		Long: `List profiles: which is current, each one's flavor, how many addons
it has, and its folder.

The folder is shortened for reading: the client folder, with ~ for your
home and … for the usual part of a Wine install (drive_c/Program Files
(x86)/World of Warcraft). -o wide shows each profile's full AddOns path,
to copy, and -o json always has it.`,
		Example: `  knapsack profiles list
  knapsack profiles list -o wide`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			profiles, err := c.app.Profiles(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				out := make([]profileJSON, len(profiles))
				for i, p := range profiles {
					out[i] = profileJSON{p.Name, p.Current, string(p.Flavor), p.Path, refStrings(p.Addons)}
				}
				return writeJSON(w, out)
			}
			t := newTable(w)
			out := &printer{w: t}
			out.printf("CURRENT\tNAME\tFLAVOR\tADDONS\tPATH\n")
			for _, p := range profiles {
				current := ""
				if p.Current {
					current = "*"
				}
				path := p.Path
				if output != "wide" {
					path = displayPath(path, c.env.home)
				}
				out.printf("%s\t%s\t%s\t%d\t%s\n", current, p.Name, p.Flavor, len(p.Addons), path)
			}
			if out.err != nil {
				return out.err
			}
			return t.Flush()
		},
	}
	addOutputFlag(cmd, &output, "json", "wide")
	return cmd
}

// displayPath shortens a profile's AddOns path for reading in a table, not
// for copying (-o wide is for that). A client folder's Interface/AddOns is
// shown as the client folder, and in a Wine prefix, the launcher's default
// drive_c/Program Files (x86)/World of Warcraft becomes …:
// ~/Games/battlenet/…/_retail_. Only that one layout is cut, so two
// installs in one prefix never look the same. A home is ~, as shortPath
// has it.
func displayPath(path, home string) string {
	path = filepath.Clean(path)
	interfaceDir := filepath.Dir(path)
	client := filepath.Dir(interfaceDir)
	if !sameName(filepath.Base(path), "AddOns") || !sameName(filepath.Base(interfaceDir), "Interface") ||
		!flavor.IsClientFolder(filepath.Base(client)) {
		return shortPath(path, home)
	}
	sep := string(filepath.Separator)
	parts := strings.Split(client, sep)
	// …/<prefix>/drive_c/<install dir>/World of Warcraft/<client>
	if n := len(parts); n >= 5 && sameName(parts[n-2], discover.GameFolder) && sameName(parts[n-3], "Program Files (x86)") &&
		sameName(parts[n-4], "drive_c") {
		client = strings.Join(parts[:n-4], sep) + sep + "…" + sep + parts[n-1]
	}
	return shortPath(client, home)
}

// sameName reports whether two names are the same file or folder, ignoring
// case only where the filesystem does, on Windows and macOS.
func sameName(a, b string) bool {
	return a == b || (runtime.GOOS == "windows" || runtime.GOOS == "darwin") && strings.EqualFold(a, b)
}

func newProfilesUseCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "use <name>",
		Short:   "Switch the current profile, which commands act on",
		Example: "  knapsack profiles use retail",
		Args:    exactArgs("profile name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := c.app.UseProfile(cmd.Context(), args[0]); err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			out.printf("Switched to profile %q.\n", args[0])
			return out.err
		},
	}
}

func newProfilesCurrentCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "current",
		Short: "Print the current profile",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name, err := c.app.CurrentProfile(cmd.Context())
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			out.printf("%s\n", name)
			return out.err
		},
	}
}

func newProfilesDeleteCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm"},
		Short:   "Delete a profile",
		Long: `Delete a profile from config, along with Knapsack's record of what it
installed there. Addon files on disk are never touched.

If it was the current profile, no profile is current afterwards.`,
		Example: "  knapsack profiles delete retail-test",
		Args:    exactArgs("profile name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := c.app.DeleteProfile(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			out.printf("Deleted profile %q.\n", args[0])
			if res.WasCurrent {
				// A warning that can't be written isn't worth failing for.
				warn := &printer{w: cmd.ErrOrStderr()}
				warn.printf("warning: %q was the current profile; choose another with: knapsack profiles use <name>\n", args[0])
			}
			return out.err
		},
	}
}
