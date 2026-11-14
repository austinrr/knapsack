package main

import (
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
		Use:   "set-profile <name> --path <AddOns folder> [--flavor <flavor>]",
		Short: "Create or change a profile",
		Long: `Create a profile, or change an existing one.

The flavor is inferred from the client folder in the path (_retail_,
_classic_, _classic_era_, _anniversary_, _forever_) unless --flavor is
given. On an existing profile, only the flags you pass change. The first
profile created becomes the current profile.`,
		Example: `  knapsack config set-profile forever --path "/games/World of Warcraft/_forever_/Interface/AddOns"
  knapsack config set-profile beta --path ~/wow-beta/AddOns --flavor retail`,
		Args: exactArgs("profile name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			req.Name = args[0]
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
			return out.err
		},
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "the game's Interface/AddOns folder (required for a new profile)")
	cmd.Flags().StringVar(&req.Flavor, "flavor", "", "the game client, if the path doesn't show it")
	_ = cmd.RegisterFlagCompletionFunc("flavor", cobra.FixedCompletions(flavor.Names(), cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.MarkFlagDirname("path")
	return cmd
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
