package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
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

// exactArgs is the Args validator for leaf commands that take exactly one
// positional argument, described by what (e.g. "profile name").
func exactArgs(what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		switch {
		case len(args) == 0:
			return missingArgs(cmd, fmt.Sprintf("%s needs %s %s", cmd.CommandPath(), article(what), what))
		case len(args) > 1:
			return fmt.Errorf("%s takes one %s, got %d arguments", cmd.CommandPath(), what, len(args))
		}
		return nil
	}
}

// maxArgs is the Args validator for leaf commands that take at most one
// positional argument, described by what.
func maxArgs(what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return fmt.Errorf("%s takes one %s, got %d arguments", cmd.CommandPath(), what, len(args))
		}
		return nil
	}
}

// article is "an" before a word starting with a vowel, else "a".
func article(word string) string {
	if word != "" && strings.ContainsRune("aeiou", rune(word[0])) {
		return "an"
	}
	return "a"
}

// minArgs is the Args validator for leaf commands that take one or more
// positional arguments, each described by what (e.g. "addon").
func minArgs(what string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return missingArgs(cmd, fmt.Sprintf("%s needs at least one %s", cmd.CommandPath(), what))
		}
		return nil
	}
}

// missingArgs is the error for a command run without the arguments it
// needs, with a hint showing how it's used: its first example, and where
// to find the rest. Running a command bare to see what it wants is common,
// so the answer shouldn't need a second command.
func missingArgs(cmd *cobra.Command, msg string) error {
	help := fmt.Sprintf("`%s --help` shows more", cmd.CommandPath())
	example, _, _ := strings.Cut(strings.TrimSpace(cmd.Example), "\n")
	if example == "" {
		return &app.UserError{Msg: msg, Hint: help}
	}
	return &app.UserError{Msg: msg, Hint: fmt.Sprintf("for example, `%s`; %s", example, help)}
}
