package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// subcommandArgs is the Args validator for commands that only group
// subcommands: any positional argument is an unknown subcommand. It keeps
// cobra's suggestion on the same line, so errors stay one line
// (DESIGN.md §8.6).
func subcommandArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return unknownCommandError(cmd, args[0])
	}
	return nil
}

// noArgs is the Args validator for leaf commands that take no positional
// arguments. Unlike cobra.NoArgs, it doesn't describe a stray argument as an
// unknown command.
func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("%s takes no arguments, got %q", cmd.CommandPath(), args[0])
	}
	return nil
}

// unknownCommandError reports name as an unknown subcommand of cmd, with a
// suggestion when one is close enough.
func unknownCommandError(cmd *cobra.Command, name string) error {
	msg := fmt.Sprintf("unknown command %q for %q", name, cmd.CommandPath())
	if suggestions := cmd.SuggestionsFor(name); len(suggestions) > 0 {
		msg += fmt.Sprintf(" (did you mean %q?)", suggestions[0])
	}
	return fmt.Errorf("%s", msg)
}
