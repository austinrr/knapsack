package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

func newAddCmd(c *cli) *cobra.Command {
	var force bool
	var refs []ref.Ref
	cmd := &cobra.Command{
		Use:   "add <ref|url>...",
		Short: "Add addons to the profile and install them",
		Long: `Add addons to the profile's config and install them.

Each addon is a ref (github:owner/repo) or a URL Knapsack recognizes. An
addon already in config is left alone. Folders already in AddOns that no
addon owns are refused unless --force is given; folders another addon owns
are never replaced.`,
		Example: `  knapsack add github:austinrr/baganator-mirror
  knapsack add https://github.com/austinrr/syndicator-mirror -p forever`,
		// Refs are parsed with the arguments, before any work: a typo is a
		// usage error, so nothing is installed and gh is never asked for a
		// token.
		Args: func(cmd *cobra.Command, args []string) error {
			if err := minArgs("addon")(cmd, args); err != nil {
				return err
			}
			refs = make([]ref.Ref, len(args))
			for i, a := range args {
				r, err := ref.Parse(a)
				if err != nil {
					return err
				}
				refs[i] = r
			}
			return nil
		},
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Even when err is set, results may hold addons already
			// installed, so they're printed first.
			results, err := c.app.Add(cmd.Context(), c.profileName(), refs, force)
			c.stopProgress()
			out := &printer{w: cmd.OutOrStdout()}
			for _, res := range results {
				switch res.Action {
				case app.ActionInstalled:
					out.printf("Added %s %s.\n", res.Ref, res.Version)
				case app.ActionAdopted:
					out.printf("Added %s %s, which was already installed.\n", res.Ref, res.Version)
				case app.ActionUnchanged:
					out.printf("%s is already added.\n", res.Ref)
				case app.ActionFailed, app.ActionRemoved, app.ActionUpgraded, app.ActionUpToDate, app.ActionNotInConfig:
					// Failures are reported by finish; the rest are update's.
				}
			}
			return finish(out, failures(results), err)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "replace folders in AddOns that no addon owns")
	c.addProfileFlag(cmd)
	return cmd
}

func newRemoveCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "remove <addon>...",
		Aliases: []string{"rm"},
		Short:   "Uninstall addons and remove them from the profile",
		Long: `Uninstall addons and remove them from the profile's config.

Only the folders Knapsack installed for each addon are deleted. An addon is
named by its ref or URL, or by its short name (baganator-mirror) when that
matches one addon.`,
		Example: `  knapsack remove baganator-mirror
  knapsack rm github:austinrr/syndicator-mirror`,
		Args:    minArgs("addon"),
		PreRunE: c.buildApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			results, err := c.app.Remove(cmd.Context(), c.profileName(), args)
			c.stopProgress()
			out := &printer{w: cmd.OutOrStdout()}
			for _, res := range results {
				if res.Action == app.ActionRemoved {
					out.printf("Removed %s.\n", res.Ref)
				}
			}
			return finish(out, failures(results), err)
		},
	}
	c.addProfileFlag(cmd)
	return cmd
}

// finish collects a multi-addon command's errors for run to render after
// the successes, each with its hint (DESIGN.md §8.6): one per failed addon,
// then err, which stopped the command, then any failure writing stdout. A
// closed stdout doesn't hide the failures. Any error makes the command exit
// 2.
func finish(out *printer, failed []error, err error) error {
	errs := addonErrors(failed)
	if err != nil {
		errs = append(errs, err)
	}
	if out.err != nil {
		errs = append(errs, out.err)
	}
	if len(errs) == 0 {
		// Return a literal nil: a nil addonErrors in an error interface
		// would not be == nil.
		return nil
	}
	return errs
}

// failures returns the errors among results, each naming its addon.
func failures(results []app.Result) []error {
	var errs []error
	for _, res := range results {
		if res.Err != nil {
			errs = append(errs, nameAddon(res.Name, res.Ref, res.Err))
		}
	}
	return errs
}

// nameAddon prefixes err with the addon it's for: r, or name, what the
// user typed, when r is zero. It doesn't when err already starts with it:
// source errors begin "github owner/repo", and errors about a name the
// user typed begin with it quoted.
func nameAddon(name string, r ref.Ref, err error) error {
	msg := err.Error()
	if name != "" && strings.HasPrefix(msg, strconv.Quote(name)) {
		return err
	}
	if r == (ref.Ref{}) {
		return fmt.Errorf("%s: %w", name, err)
	}
	if strings.HasPrefix(msg, r.Source+" "+r.ID) {
		return err
	}
	return fmt.Errorf("%s: %w", r, err)
}

// noteEmpty tells the user there's nothing to show, on stderr, so stdout
// stays empty for scripts, like kubectl.
func noteEmpty(cmd *cobra.Command, all bool) error {
	note := "No addons in this profile."
	if all {
		note = "No addons in any profile."
	}
	_, err := fmt.Fprintln(cmd.ErrOrStderr(), note)
	return err
}

// installedJSON is one addon in list and info's JSON output.
type installedJSON struct {
	Profile     string     `json:"profile"`
	Ref         string     `json:"ref"`
	Status      string     `json:"status"`
	Version     string     `json:"version,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	Folders     []string   `json:"folders,omitempty"`
}

func toInstalledJSON(in app.Installed) installedJSON {
	j := installedJSON{Profile: in.Profile, Ref: in.Ref.String(), Status: string(in.Status), Version: in.Version, Folders: in.Folders}
	if !in.InstalledAt.IsZero() {
		t := in.InstalledAt.UTC()
		j.InstalledAt = &t
	}
	return j
}

func newListCmd(c *cli) *cobra.Command {
	var output string
	var sc scope
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the profile's addons",
		Long: `List the profile's addons and whether each is installed.

It reads only config and state, never the network, so it's instant. An
addon is installed, missing (in config but not installed), or not in config
(installed, but no longer in config). Use check to see what's outdated.`,
		Args:    sc.args(false),
		PreRunE: c.buildApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sel, err := sc.selector(cmd.Context(), c, nil)
			if err != nil {
				return err
			}
			list, err := c.app.List(cmd.Context(), sel)
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				out := make([]installedJSON, len(list))
				for i, in := range list {
					out[i] = toInstalledJSON(in)
				}
				return writeJSON(w, out)
			}
			if len(list) == 0 {
				return noteEmpty(cmd, sc.all)
			}
			tw := newTable(w)
			out := &printer{w: tw}
			wide := output == "wide"
			if sc.all {
				out.printf("PROFILE\t")
			}
			if wide {
				out.printf("ADDON\tSTATUS\tVERSION\tINSTALLED\tFOLDERS\n")
			} else {
				out.printf("ADDON\tSTATUS\tVERSION\n")
			}
			for _, in := range list {
				if sc.all {
					out.printf("%s\t", in.Profile)
				}
				version := orDash(in.Version)
				if wide {
					out.printf("%s\t%s\t%s\t%s\t%s\n", in.Ref, in.Status, version, formatTime(in.InstalledAt), orDash(strings.Join(in.Folders, ",")))
				} else {
					out.printf("%s\t%s\t%s\n", in.Ref, in.Status, version)
				}
			}
			if out.err != nil {
				return out.err
			}
			return tw.Flush()
		},
	}
	addOutputFlag(cmd, &output, "wide", "json")
	sc.addFlag(cmd)
	c.addProfileFlag(cmd)
	return cmd
}

// detailsJSON is info's JSON output.
type detailsJSON struct {
	installedJSON
	Title      string `json:"title,omitempty"`
	Author     string `json:"author,omitempty"`
	Notes      string `json:"notes,omitempty"`
	Interfaces []int  `json:"interfaces,omitempty"`
}

func newInfoCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "info <addon>",
		Short: "Show details of one addon",
		Long: `Show one addon's ref, status, version, folders and install time, with
the title, author, notes and Interface numbers from its TOC. Like list, it
doesn't use the network.`,
		Example: `  knapsack info baganator-mirror`,
		Args:    exactArgs("addon"),
		PreRunE: c.buildApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := c.app.Info(cmd.Context(), c.profileName(), args[0])
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				return writeJSON(w, detailsJSON{
					installedJSON: toInstalledJSON(d.Installed),
					Title:         d.Title, Author: d.Author, Notes: d.Notes, Interfaces: d.Interfaces,
				})
			}
			tw := newTable(w)
			out := &printer{w: tw}
			field := func(name, value string) {
				if value != "" {
					out.printf("%s:\t%s\n", name, value)
				}
			}
			field("Ref", d.Ref.String())
			field("Status", string(d.Status))
			field("Version", d.Version)
			if !d.InstalledAt.IsZero() {
				field("Installed", formatTime(d.InstalledAt))
			}
			field("Folders", strings.Join(d.Folders, ", "))
			field("Title", d.Title)
			field("Author", d.Author)
			field("Notes", d.Notes)
			if len(d.Interfaces) > 0 {
				nums := make([]string, len(d.Interfaces))
				for i, n := range d.Interfaces {
					nums[i] = strconv.Itoa(n)
				}
				field("Interface", strings.Join(nums, ", "))
			}
			if out.err != nil {
				return out.err
			}
			return tw.Flush()
		},
	}
	addOutputFlag(cmd, &output, "json")
	c.addProfileFlag(cmd)
	return cmd
}

// formatTime shows a time in UTC to the second, the same on every machine.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format(time.RFC3339)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
