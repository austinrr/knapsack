package main

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/paths"
)

// noNotifierEnv turns off the update check, like gh's
// GH_NO_UPDATE_NOTIFIER (DESIGN.md §8.4).
const noNotifierEnv = "KNAPSACK_NO_UPDATE_NOTIFIER"

// The update check: how often other commands check, and how long a check
// may take. A command never waits longer than checkTimeout for it, and
// usually not at all, since the check runs while the command does.
const (
	checkInterval = 24 * time.Hour
	checkTimeout  = 2 * time.Second
	// versionTimeout is longer: checking is part of what version does.
	versionTimeout = 5 * time.Second
)

// noCheck are the commands that don't start the daily check: version and
// self-update look for themselves, and help and completion are for typing
// commands, not running them. Asking a command for --help, or running one
// that only groups others, like config or knapsack itself, which print
// help, is help too.
var noCheck = []string{"version", "self-update", "help", "completion", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd}

// isHelp reports whether args ask for help: -h or --help before any --,
// where flags end.
func isHelp(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "--help":
			return true
		}
	}
	return false
}

// startUpdateCheck starts the daily check for a newer Knapsack release,
// in the background while the command runs (DESIGN.md §8.4). It returns
// a function that waits for the check and, if it found one, prints a
// notice on stderr. Nothing is checked unless stderr is a terminal, so
// scripts, pipes and CI never see the notice, nor when the user opted out
// or the build can't be updated. A failed check prints nothing.
func startUpdateCheck(ctx context.Context, root *cobra.Command, args []string, c *cli) func() {
	e := c.env
	if !e.interactive || e.getenv("CI") != "" || e.getenv(noNotifierEnv) != "" {
		return func() {}
	}
	// Cobra adds help and completion only when it runs, so add them now
	// for Find to see.
	root.InitDefaultHelpCmd()
	root.InitDefaultCompletionCmd()
	cmd, _, err := root.Find(args)
	b := c.selfBuild()
	if err != nil || cmd.HasSubCommands() ||
		slices.Contains(noCheck, cmd.Name()) || isHelp(args) || app.UpdateHint(b) == "" {
		return func() {}
	}
	ctx, cancel := context.WithTimeout(ctx, checkTimeout)
	found := make(chan app.Latest, 1)
	go func() {
		defer close(found)
		a, err := newSelfApp(e)
		if err != nil {
			return
		}
		if latest, err := a.LatestVersion(ctx, b, checkInterval); err == nil && latest.Newer && !latest.Cached {
			found <- latest
		}
	}()
	return func() {
		defer cancel()
		if latest, ok := <-found; ok {
			// Nowhere to report a failed write to stderr.
			_, _ = fmt.Fprintf(e.stderr, "knapsack %s is available (you have %s); %s.\n", latest.Version, b.Version, app.UpdateHint(b))
		}
	}
}

// newSelfApp builds an App for the update check. It doesn't use the
// cli's flags, which are still being parsed while the check runs, so it
// can't race with them; the check needs only the cache and GitHub.
func newSelfApp(e env) (*app.App, error) {
	p, err := paths.Resolve(e.getenv)
	if err != nil {
		return nil, err
	}
	return newApp(e, p, true, nil), nil
}

// checkFromVersion is the check version makes every time it runs, unless
// the user opted out or the build can't be updated. The notice goes to
// stderr, so stdout stays just the version for scripts; a failed check
// is only shown with -v.
func checkFromVersion(cmd *cobra.Command, c *cli) {
	b := c.selfBuild()
	if c.env.getenv(noNotifierEnv) != "" || app.UpdateHint(b) == "" {
		return
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), versionTimeout)
	defer cancel()
	a, err := newSelfApp(c.env)
	var latest app.Latest
	if err == nil {
		latest, err = a.LatestVersion(ctx, b, 0)
	}
	w := cmd.ErrOrStderr()
	switch {
	case err != nil && c.verbose:
		_, _ = fmt.Fprintf(w, "couldn't check for a newer release: %v\n", err)
	case err == nil && latest.Newer:
		_, _ = fmt.Fprintf(w, "%s is available; %s.\n", latest.Version, app.UpdateHint(b))
	}
}

func newSelfUpdateCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "self-update",
		Short: "Update knapsack to the newest release",
		Long: `Replace this knapsack binary with the newest release's, after checking the
download's SHA-256.

A knapsack built with go install, or installed by a package manager, is
updated the same way it was installed; self-update says how.

Every command checks for a newer release once a day, and version every
time, and says so on stderr. Set ` + noNotifierEnv + ` to stop that.`,
		Args:    noArgs,
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			res, err := c.app.SelfUpdate(cmd.Context(), c.selfBuild())
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if !res.Updated {
				_, err = fmt.Fprintf(out, "knapsack is up to date (%s).\n", res.From)
			} else {
				_, err = fmt.Fprintf(out, "Updated knapsack %s → %s.\n", res.From, res.To)
			}
			return err
		},
	}
}
