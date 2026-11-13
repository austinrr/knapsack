package main

import (
	"github.com/spf13/cobra"
)

// newRootCmd builds the complete command tree. Commands are constructed
// here rather than registered from package-level variables and init(), so
// every invocation, including each test, gets a fresh, independent tree
// (DESIGN.md §8.7).
func newRootCmd(version string) *cobra.Command {
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

	root.SetHelpCommand(newHelpCmd())
	root.AddCommand(newVersionCmd(version))
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
