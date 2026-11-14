package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// pruneNote follows update's and check's summary when addons that are no
// longer in config are being left installed.
const pruneNote = "Addons not in config are left installed; `knapsack update --prune` removes them."

// errPending is what check returns when changes are pending, so run exits 1
// without printing anything more (DESIGN.md §8.6).
var errPending = errors.New("changes pending")

// scope is the profiles and addons a bulk command covers: -p, -A and any
// addons named.
type scope struct {
	all bool // -A, --all-profiles
}

// addFlag registers -A on a command that can cover every
// profile, like kubectl's --all-namespaces.
func (s *scope) addFlag(cmd *cobra.Command) {
	cmd.Flags().BoolVarP(&s.all, "all-profiles", "A", false, "act on every profile")
}

// args is the Args validator for a bulk command. -A can't be combined with
// -p or with named addons (DESIGN.md §8.2). It runs before the App is
// built, so a usage error never reaches the network.
func (s *scope) args(namesAllowed bool) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if !namesAllowed {
			if err := noArgs(cmd, args); err != nil {
				return err
			}
		}
		if !s.all {
			return nil
		}
		if cmd.Flags().Changed("profile") {
			return errors.New("--all-profiles and --profile can't be used together")
		}
		if len(args) > 0 {
			return errors.New("--all-profiles can't be combined with named addons")
		}
		return nil
	}
}

// selector builds the app.Selector for a bulk command. With -A it lists
// every profile, so profiles holds their names in order; otherwise it's
// the one profile, "" meaning the current one. KNAPSACK_PROFILE applies
// only without -A.
func (s *scope) selector(ctx context.Context, c *cli, names []string) (app.Selector, error) {
	if !s.all {
		sel := app.Selector{Addons: names}
		if p := c.profileName(); p != "" {
			sel.Profiles = []string{p}
		}
		return sel, nil
	}
	infos, err := c.app.Profiles(ctx)
	if err != nil {
		return app.Selector{}, err
	}
	if len(infos) == 0 {
		// An empty Selector would mean the current profile.
		return app.Selector{}, &app.UserError{
			Msg:  "no profiles",
			Hint: "create one with: knapsack config set-profile <name> --path <AddOns folder>",
		}
	}
	var sel app.Selector
	for _, in := range infos {
		sel.Profiles = append(sel.Profiles, in.Name)
	}
	return sel, nil
}

func newUpdateCmd(c *cli) *cobra.Command {
	var sc scope
	var opts app.UpdateOptions
	cmd := &cobra.Command{
		Use:   "update [addon...]",
		Short: "Install missing addons and upgrade outdated ones",
		Long: `Make the profile match its config: install addons in config that aren't
installed, and upgrade those whose newest release isn't the one installed.
Releases are compared by checksum, not by version.

With no addons named, it covers every addon in the profile. It never
changes config, and never uninstalls anything unless --prune is given.`,
		Example: `  knapsack update
  knapsack update baganator-mirror
  knapsack update -A --prune`,
		Args:    sc.args(true),
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := sc.selector(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			results, err := c.app.Update(cmd.Context(), sel, opts)
			c.stopProgress()
			out := &printer{w: cmd.OutOrStdout()}
			if err == nil && len(results) == 0 {
				return noteEmpty(cmd, sc.all)
			}
			printed := 0
			for _, res := range results {
				prefix := ""
				if sc.all {
					prefix = res.Profile + ": "
				}
				switch res.Action {
				case app.ActionInstalled:
					out.printf("%sInstalled %s %s.\n", prefix, res.Ref, res.Version)
					printed++
				case app.ActionUpgraded:
					out.printf("%sUpgraded %s to %s.\n", prefix, res.Ref, res.Version)
					printed++
				case app.ActionRemoved:
					out.printf("%sRemoved %s.\n", prefix, res.Ref)
					printed++
				case app.ActionUpToDate, app.ActionNotInConfig, app.ActionFailed, app.ActionAdopted, app.ActionUnchanged:
					// Counted in the summary; failures are reported by
					// finish. Update never adopts.
				}
			}
			if err == nil {
				tallies := make([]tally, len(results))
				for i, res := range results {
					tallies[i] = tally{res.Profile, string(res.Action)}
				}
				if printed > 0 {
					out.printf("\n")
				}
				out.printf("%s\n", summarize(sel.Profiles, sc.all, tallies, []string{
					string(app.ActionInstalled), string(app.ActionUpgraded), string(app.ActionRemoved),
					string(app.ActionUpToDate), string(app.ActionNotInConfig), string(app.ActionFailed),
				}))
				if !opts.Prune && slices.ContainsFunc(results, func(r app.Result) bool { return r.Action == app.ActionNotInConfig }) {
					out.printf("%s\n", pruneNote)
				}
			}
			return finish(out, failures(results), err)
		},
	}
	cmd.Flags().BoolVar(&opts.Prune, "prune", false, "uninstall addons that are installed but no longer in config")
	cmd.Flags().BoolVar(&opts.Force, "force", false, "replace folders in AddOns that no addon owns")
	sc.addFlag(cmd)
	c.addProfileFlag(cmd)
	return cmd
}

// planJSON is one addon in check's JSON output.
type planJSON struct {
	Profile   string `json:"profile"`
	Ref       string `json:"ref,omitempty"`
	Name      string `json:"name,omitempty"`
	Action    string `json:"action"`
	Installed string `json:"installed,omitempty"`
	Available string `json:"available,omitempty"`
	Error     string `json:"error,omitempty"`
}

func newCheckCmd(c *cli) *cobra.Command {
	var sc scope
	var output string
	var prune bool
	cmd := &cobra.Command{
		Use:   "check [addon...]",
		Short: "Show what update would do, without changing anything",
		Long: `Resolve each addon's newest release, as update would, and show the
changes it would make: install, upgrade, or prune (installed but no longer
in config, which update removes only with --prune).

It exits 0 when everything is up to date, 1 when changes are pending, and 2
on errors, like kubectl diff. Prune candidates count as pending only with
--prune, matching what update --prune would do.`,
		Example: `  knapsack check
  knapsack check -A
  knapsack check baganator-mirror -o json`,
		Args:    sc.args(true),
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			sel, err := sc.selector(cmd.Context(), c, args)
			if err != nil {
				return err
			}
			plans, err := c.app.Check(cmd.Context(), sel)
			c.stopProgress()
			var failed []error
			pending := false
			for _, p := range plans {
				switch p.Action {
				case app.PlanFailed:
					failed = append(failed, nameAddon(p.Name, p.Ref, p.Err))
				case app.PlanInstall, app.PlanUpgrade:
					pending = true
				case app.PlanPrune:
					// Plain update leaves these alone, so without --prune
					// they'd keep check failing after every update.
					pending = pending || prune
				case app.PlanNone:
				}
			}
			w := cmd.OutOrStdout()
			out := &printer{w: w}
			switch {
			case output == "json":
				js := make([]planJSON, len(plans))
				for i, p := range plans {
					js[i] = planJSON{Profile: p.Profile, Name: p.Name, Action: string(p.Action), Installed: p.Installed, Available: p.Available}
					if p.Ref != (ref.Ref{}) {
						js[i].Ref = p.Ref.String()
					}
					if p.Err != nil {
						js[i].Error = p.Err.Error()
					}
				}
				if werr := writeJSON(w, js); werr != nil {
					out.err = werr
				}
			case len(plans) == 0 && err == nil:
				return noteEmpty(cmd, sc.all)
			case err == nil:
				tallies := make([]tally, len(plans))
				for i, p := range plans {
					tallies[i] = tally{p.Profile, string(p.Action)}
				}
				if len(plans) > 0 {
					printPlans(out, plans, sc.all)
					out.printf("\n")
				}
				out.printf("%s\n", summarize(sel.Profiles, sc.all, tallies, []string{
					string(app.PlanInstall), string(app.PlanUpgrade), string(app.PlanPrune), string(app.PlanFailed),
				}))
				if !prune && slices.ContainsFunc(plans, func(p app.Plan) bool { return p.Action == app.PlanPrune }) {
					out.printf("%s\n", pruneNote)
				}
			default:
				// Stopped partway: show what was checked, then the error.
				if len(plans) > 0 {
					printPlans(out, plans, sc.all)
				}
			}
			if ferr := finish(out, failed, err); ferr != nil {
				return ferr
			}
			if pending {
				return errPending
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&prune, "prune", false, "count addons no longer in config as pending changes, as update --prune would remove them")
	addOutputFlag(cmd, &output, "json")
	sc.addFlag(cmd)
	c.addProfileFlag(cmd)
	return cmd
}

// printPlans writes check's table, with a PROFILE column under -A.
func printPlans(out *printer, plans []app.Plan, all bool) {
	tw := newTable(out.w)
	row := &printer{w: tw}
	if all {
		row.printf("PROFILE\t")
	}
	row.printf("ADDON\tINSTALLED\tAVAILABLE\tACTION\n")
	for _, p := range plans {
		if all {
			row.printf("%s\t", p.Profile)
		}
		addon := p.Name
		if p.Ref != (ref.Ref{}) {
			addon = p.Ref.String()
		}
		action := string(p.Action)
		if p.Action == app.PlanNone {
			action = "-"
		}
		row.printf("%s\t%s\t%s\t%s\n", addon, orDash(p.Installed), orDash(p.Available), action)
	}
	if row.err != nil {
		out.err = row.err
		return
	}
	if err := tw.Flush(); err != nil && out.err == nil {
		out.err = err
	}
}

// tally is one addon's profile and action, for summarize.
type tally struct{ profile, action string }

// summarize counts addons by action, for the line after update's and
// check's output: "1 upgrade, 2 up to date", or under -A, one group per
// profile in the order given: "forever: 1 upgrade · retail: up to date".
// Only the actions in order are counted, in that order; a profile with
// none of them is "up to date", and one with no addons says so.
func summarize(profiles []string, all bool, tallies []tally, order []string) string {
	counts := map[string]map[string]int{}
	for _, t := range tallies {
		if counts[t.profile] == nil {
			counts[t.profile] = map[string]int{}
		}
		counts[t.profile][t.action]++
	}
	line := func(p string) string {
		if counts[p] == nil {
			return "no addons"
		}
		var parts []string
		for _, a := range order {
			if n := counts[p][a]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, a))
			}
		}
		if len(parts) == 0 {
			return "up to date"
		}
		return strings.Join(parts, ", ")
	}
	if !all {
		l := "no addons"
		if len(tallies) > 0 {
			l = line(tallies[0].profile) // one profile, so any tally names it
		}
		if l == "up to date" {
			return "Everything is up to date."
		}
		return l
	}
	groups := make([]string, len(profiles))
	for i, p := range profiles {
		groups[i] = p + ": " + line(p)
	}
	return strings.Join(groups, " · ")
}
