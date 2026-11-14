package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

func newConfigCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage profiles and config.toml",
		Long: `Manage profiles and config.toml.

A profile is one game's AddOns folder and the addons you want in it.

Commands that change config rewrite config.toml in a fixed layout, so
comments in the file are not kept.`,
		Args:              subcommandArgs,
		PersistentPreRunE: c.buildApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(
		newSetProfileCmd(c),
		newGetProfilesCmd(c),
		newUseProfileCmd(c),
		newCurrentProfileCmd(c),
		newDeleteProfileCmd(c),
		newViewCmd(c),
	)
	return cmd
}

func newSetProfileCmd(c *cli) *cobra.Command {
	var req app.SetProfileRequest
	cmd := &cobra.Command{
		Use:   "set-profile [name] --path <game client folder> [--flavor <flavor>]",
		Short: "Create or change a profile",
		Long: `Create a profile, or change an existing one.

--path is the game's client folder, such as _retail_, or the
Interface/AddOns folder in it. Quote it: the usual path has spaces in it.

The flavor is inferred from the client folder in the path (_retail_,
_classic_, _classic_era_, _anniversary_, _forever_) unless --flavor is
given. Test clients are their live client's flavor: _ptr_, _xptr_ and
_beta_ are retail, _classic_ptr_ and _classic_beta_ classic, and
_classic_era_ptr_ classic_era; --flavor can say otherwise for these. On an
existing profile, only the flags you pass change. The first profile
created becomes the current profile.

Without a name, the profile is the one already using the path, or else is
named after its flavor (retail), or for a test client, its flavor and the
test (retail-ptr), so most people never need to choose one. A second
profile for the same client, such as a test copy of the game, needs a name.`,
		Example: "  knapsack config set-profile --path " + app.Quoted(app.ExampleClientFolder()) + "\n" +
			"  knapsack config set-profile retail-test --path " + app.Quoted(filepath.Join(filepath.Dir(filepath.Dir(app.ExampleClientFolder())), "WoW Test", "_retail_")),
		Args: func(cmd *cobra.Command, args []string) error {
			if err := splitPath(c.rawArgs); err != nil {
				return err
			}
			return maxArgs("profile name")(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				req.Name = args[0]
			}
			res, err := c.app.SetProfile(cmd.Context(), req)
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			verb := "modified"
			switch {
			case res.Created:
				verb = "created"
			case !res.Changed:
				verb = "unchanged"
			}
			out.printf("Profile %q %s (%s, %s).\n", res.Profile.Name, verb, res.Profile.Flavor, res.Profile.Path)
			if res.MadeCurrent {
				out.printf("Switched to profile %q.\n", res.Profile.Name)
			}
			if out.err != nil {
				return out.err
			}
			if res.MadeCurrent {
				// The first profile: say what to do next.
				_, err := fmt.Fprintln(cmd.ErrOrStderr(), "Next, add addons: knapsack add github:owner/repo, or a GitHub URL.")
				return err
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "the game's client folder, such as _retail_, or its Interface/AddOns folder (required for a new profile)")
	cmd.Flags().StringVar(&req.Flavor, "flavor", "", "the game client, if the path doesn't show it")
	_ = cmd.RegisterFlagCompletionFunc("flavor", cobra.FixedCompletions(flavor.Names(), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagDirname("path")
	return cmd
}

// splitPath catches a --path the shell split at its spaces, as an unquoted
// C:\Program Files (x86)\… is in cmd.exe. It reads the raw arguments,
// since only their order shows which words followed --path's value: those
// up to the next flag. They're the rest of the path unless the value is a
// folder on its own, as with --path …\AddOns retail, a name after the path.
func splitPath(raw []string) error {
	value, rest, ok := afterFlag(raw, "--path")
	if !ok || len(rest) == 0 {
		return nil
	}
	if info, err := os.Stat(value); err == nil && info.IsDir() {
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

func newGetProfilesCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "get-profiles",
		Short: "List profiles",
		Args:  noArgs,
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
				out.printf("%s\t%s\t%s\t%d\t%s\n", current, p.Name, p.Flavor, len(p.Addons), p.Path)
			}
			if out.err != nil {
				return out.err
			}
			return t.Flush()
		},
	}
	addOutputFlag(cmd, &output, "json")
	return cmd
}

func newUseProfileCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "use-profile <name>",
		Short: "Set the current profile",
		Args:  exactArgs("profile name"),
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

func newCurrentProfileCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "current-profile",
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

func newDeleteProfileCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "delete-profile <name>",
		Short: "Delete a profile",
		Long: `Delete a profile from config, along with Knapsack's record of what it
installed there. Addon files on disk are never touched.

If it was the current profile, no profile is current afterwards.`,
		Args: exactArgs("profile name"),
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
				warn.printf("warning: %q was the current profile; choose another with: knapsack config use-profile <name>\n", args[0])
			}
			return out.err
		},
	}
}

// configJSON is the -o json shape of config view.
type configJSON struct {
	CurrentProfile string                       `json:"current_profile"`
	Profiles       map[string]profileJSON       `json:"profiles"`
	Sources        map[string]map[string]string `json:"sources"`
}

func newViewCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "view",
		Short: "Print the config, with API keys redacted",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := c.app.ViewConfig(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				return writeJSON(w, toConfigJSON(cfg))
			}
			_, err = w.Write(config.Marshal(cfg))
			return err
		},
	}
	addOutputFlag(cmd, &output, "json")
	return cmd
}

func toConfigJSON(cfg *config.Config) configJSON {
	out := configJSON{
		CurrentProfile: cfg.CurrentProfile,
		Profiles:       map[string]profileJSON{},
		Sources:        map[string]map[string]string{},
	}
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		out.Profiles[name] = profileJSON{name, name == cfg.CurrentProfile, string(p.Flavor), p.Path, refStrings(p.Addons)}
	}
	for name, key := range cfg.Sources.Keys() {
		out.Sources[name] = map[string]string{"api_key": key}
	}
	return out
}
