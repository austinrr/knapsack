package main

import (
	"github.com/spf13/cobra"
)

// newRootCmd builds the complete command tree. Commands are constructed
// here rather than registered from package-level variables and init(), so
// every invocation, including each test, gets a fresh, independent tree
// (DESIGN.md §8.7).
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "knapsack",
		Short: "A small, cross-platform World of Warcraft addon manager",
		// Errors are rendered once, by run. Usage is printed only for --help,
		// not after every error.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.AddCommand(newVersionCmd())
	return root
}
