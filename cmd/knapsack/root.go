package main

import (
	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/paths"
)

// cli holds the global flags and the App built from them. The App is built
// on first use, after flags are parsed, so commands like version and help
// never touch the filesystem.
type cli struct {
	env        env
	configFile string
	verbose    bool

	app *app.App
}

// App returns the App, building it the first time from the environment and
// --config.
func (c *cli) App() (*app.App, error) {
	if c.app != nil {
		return c.app, nil
	}
	p, err := paths.Resolve(c.env.getenv)
	if err != nil {
		return nil, err
	}
	if c.configFile != "" {
		if p, err = p.WithConfigFile(c.configFile); err != nil {
			return nil, err
		}
	}
	c.app = app.New(app.Options{Paths: p, Config: config.NewStore(p.ConfigFile)})
	return c.app, nil
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
	flags.BoolVarP(&c.verbose, "verbose", "v", false, "show the underlying cause of errors")

	root.SetHelpCommand(newHelpCmd())
	root.AddCommand(
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
