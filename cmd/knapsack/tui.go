package main

import (
	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/tui"
)

func newTUICmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "Browse a profile's addons in an interactive terminal interface",
		Long: `Open an interactive interface over a profile's addons, like k9s: each
addon and whether it's up to date, checked as list checks them, with
details on enter and a filter on /. Press ? for every key, q to quit.

It opens on the current profile, or the one -p names, without making it
current. It's for a terminal; scripts use list, check and update.`,
		Example: "  knapsack tui\n  knapsack tui -p retail",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Bubble Tea reads keys from stdin and draws on stdout.
			if !c.env.stdinTerminal || !isTerminal(c.env.stdout) {
				return &app.UserError{
					Msg:  "knapsack tui needs a terminal",
					Hint: "run it in a terminal; in a script, use `knapsack list`, `check` or `update`",
				}
			}
			p, err := c.paths()
			if err != nil {
				return err
			}
			return tui.Run(cmd.Context(), tui.Options{
				NewApp:  func(r app.Reporter) *app.App { return newApp(c.env, p, true, r) },
				Profile: c.profileName(),
				Home:    c.env.home,
				Running: c.env.version + " on " + c.env.platform,
			})
		},
	}
	c.addProfileFlag(cmd)
	return cmd
}
