package main

import (
	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/config"
)

func newConfigCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show and edit Knapsack's settings",
		Long: `Show and edit Knapsack's settings, kept in config.toml: profiles (managed with
knapsack profiles), how each source is read and its API keys (knapsack
sources), and settings for all of Knapsack.`,
	}
	return c.groupCmd(cmd, newViewCmd(c), newConfigEditCmd(c))
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
		Sources:        cfg.Sources.Settings(),
	}
	for _, name := range cfg.ProfileNames() {
		p := cfg.Profiles[name]
		out.Profiles[name] = profileJSON{name, name == cfg.CurrentProfile, string(p.Flavor), p.Path, refStrings(p.Addons)}
	}
	return out
}
