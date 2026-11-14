package main

import (
	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/paths"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/source/github"
	"github.com/austinrr/knapsack/internal/state"
)

// cli holds the global flags and the App built from them.
type cli struct {
	env        env
	configFile string
	verbose    bool
	profile    string // -p, for the commands that take it

	app *app.App
}

// buildApp builds an App for local work from the environment and --config.
// Commands that need it call it from PreRunE or PersistentPreRunE, after
// flags are parsed (DESIGN.md §8.7); version and help never do.
func (c *cli) buildApp(cmd *cobra.Command, _ []string) error {
	return c.build(cmd, false)
}

// buildNetApp is buildApp plus sources and an HTTP client, for commands
// that download. Only these look for a GitHub token, which may run the
// GitHub CLI, so offline commands stay instant.
func (c *cli) buildNetApp(cmd *cobra.Command, _ []string) error {
	return c.build(cmd, true)
}

func (c *cli) build(cmd *cobra.Command, network bool) error {
	p, err := paths.Resolve(c.env.getenv)
	if err != nil {
		return err
	}
	if c.configFile != "" {
		if p, err = p.WithConfigFile(c.configFile); err != nil {
			return err
		}
	}
	opts := app.Options{
		Paths:    p,
		Config:   config.NewStore(p.ConfigFile),
		State:    state.NewStore(p.StateFile),
		Reporter: &lineReporter{w: cmd.ErrOrStderr(), verbose: c.verbose},
	}
	if network {
		opts.HTTP = httpx.NewClient(c.env.version)
		token := githubToken(cmd.Context(), c.env.getenv, c.env.ghAuthToken)
		opts.Sources = source.NewRegistry(github.New(opts.HTTP, c.env.githubURL, token))
	}
	c.app = app.New(opts)
	return nil
}

// profileEnv picks the profile when -p isn't given, like kubectl's
// KUBECONFIG context (DESIGN.md §8.3).
const profileEnv = "KNAPSACK_PROFILE"

// addProfileFlag registers -p on a command that acts on one profile.
func (c *cli) addProfileFlag(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&c.profile, "profile", "p", "",
		"profile to act on (default $"+profileEnv+", then the current profile)")
}

// profileName is -p, else $KNAPSACK_PROFILE, else "" for the current
// profile.
func (c *cli) profileName() string {
	if c.profile != "" {
		return c.profile
	}
	return c.env.getenv(profileEnv)
}

// newRootCmd builds the complete command tree. Commands are constructed
// here rather than registered from package-level variables and init(), so
// every invocation, including each test, gets a fresh, independent tree
// (DESIGN.md §8.7).
func newRootCmd(c *cli) *cobra.Command {
	root := &cobra.Command{
		Use:   "knapsack",
		Short: "A small, cross-platform World of Warcraft addon manager",
		// With no arguments, print help (like kubectl). Any argument that
		// isn't a subcommand is rejected by subcommandArgs.
		Args: subcommandArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
		// Errors are rendered once, by run. Usage is printed only for --help,
		// not after every error.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	flags := root.PersistentFlags()
	flags.StringVar(&c.configFile, "config", "", "use this config file instead of the default")
	flags.BoolVarP(&c.verbose, "verbose", "v", false, "show progress and the underlying cause of errors")

	root.SetHelpCommand(newHelpCmd())
	root.AddCommand(
		newAddCmd(c),
		newRemoveCmd(c),
		newListCmd(c),
		newInfoCmd(c),
		newConfigCmd(c),
		newVersionCmd(c.env.version),
	)
	return root
}

// newHelpCmd replaces cobra's help command, which prints "Unknown help
// topic" for an unknown command and still exits 0.
func newHelpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "help [command]",
		Short: "Help about any command",
		RunE: func(cmd *cobra.Command, args []string) error {
			target, rest, err := cmd.Root().Find(args)
			if err != nil {
				return err
			}
			if len(rest) > 0 {
				return unknownCommandError(target, rest[0])
			}
			return target.Help()
		},
	}
}
