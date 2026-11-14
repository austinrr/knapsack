package main

import (
	"fmt"
	"io"
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

Each addon is a ref (github:owner/repo, wago:<id>) or its GitHub or
addons.wago.io link. Wago needs an API key: see knapsack sources set-key.
An addon already in config is left alone. Folders already in AddOns that
no addon owns are refused unless --force is given; folders another addon
owns are never replaced.`,
		Example: `  knapsack add github:austinrr/baganator-mirror
  knapsack add https://github.com/austinrr/syndicator-mirror -p forever
  knapsack add https://addons.wago.io/addons/plater`,
		// Refs are parsed with the arguments, before any work: a typo is a
		// usage error, so nothing is installed and gh is never asked for a
		// token.
		Args: func(cmd *cobra.Command, args []string) error {
			if err := minArgs("addon ref or URL")(cmd, args); err != nil {
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
					out.printf("Added %s %s to %s.\n", res.Ref, res.Version, res.Profile)
				case app.ActionAdopted:
					out.printf("Added %s %s to %s; it was already installed.\n", res.Ref, res.Version, res.Profile)
				case app.ActionUnchanged:
					out.printf("%s is already in %s.\n", res.Ref, res.Profile)
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
					out.printf("Removed %s from %s.\n", res.Ref, res.Profile)
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
// stays empty for scripts, like kubectl. It names the profile, so a wrong
// -p or $KNAPSACK_PROFILE shows.
func noteEmpty(cmd *cobra.Command, sel app.Selector, all bool) error {
	note := "No addons in any profile."
	if !all {
		note = fmt.Sprintf("No addons in profile %s.", sel.Profiles[0])
	}
	_, err := fmt.Fprintln(cmd.ErrOrStderr(), note)
	return err
}

// noteProfile names the profile a command's table is for, on stderr, so
// stdout stays just the table for scripts. Under -A the table has a
// PROFILE column instead.
func noteProfile(cmd *cobra.Command, name string) error {
	_, err := fmt.Fprintf(cmd.ErrOrStderr(), "Profile %s:\n", name)
	return err
}

// noteUnmanaged says how many unmanaged folders list left out, and how to
// see them, so a profile made over an existing AddOns folder doesn't look
// empty without explanation. It's on stderr, so JSON output gets it too
// without changing what scripts parse.
func noteUnmanaged(cmd *cobra.Command, n int, afterTable bool) error {
	if n == 0 {
		return nil
	}
	if afterTable {
		// A blank line sets it apart from the table above it.
		if _, err := fmt.Fprintln(cmd.ErrOrStderr()); err != nil {
			return err
		}
	}
	note := fmt.Sprintf("%d folders in AddOns aren't managed by Knapsack; see them with --unmanaged.", n)
	if n == 1 {
		note = "1 folder in AddOns isn't managed by Knapsack; see it with --unmanaged."
	}
	_, err := fmt.Fprintln(cmd.ErrOrStderr(), note)
	return err
}

// installedJSON is one addon in list and info's JSON output. Ref is empty
// for an unmanaged folder; Available and Error are set when list checked.
type installedJSON struct {
	Profile     string     `json:"profile"`
	Ref         string     `json:"ref,omitempty"`
	Title       string     `json:"title,omitempty"`
	Status      string     `json:"status"`
	Version     string     `json:"version,omitempty"`
	Available   string     `json:"available,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	Folders     []string   `json:"folders,omitempty"`
	Error       string     `json:"error,omitempty"`
}

func toInstalledJSON(in app.Installed) installedJSON {
	j := installedJSON{Profile: in.Profile, Ref: in.Ref.String(), Title: in.Title, Status: string(in.Status),
		Version: in.Version, Available: in.Available, Folders: in.Folders}
	if in.Ref == (ref.Ref{}) {
		j.Ref = "" // an unmanaged folder
	}
	if !in.InstalledAt.IsZero() {
		t := in.InstalledAt.UTC()
		j.InstalledAt = &t
	}
	if in.Err != nil {
		j.Error = in.Err.Error()
	}
	return j
}

// nameRow is one table row's profile, name and ref, for uniqueNames.
type nameRow struct {
	profile, name string
	ref           ref.Ref
}

// uniqueNames returns each row's name, with its ref's ID added when another
// row in the same profile shows the same name, since titles and repo names
// needn't be unique: "Details! (a/details)" and "Details! (b/details)".
func uniqueNames(rows []nameRow) []string {
	count := map[[2]string]int{}
	for _, r := range rows {
		count[[2]string{r.profile, strings.ToLower(r.name)}]++
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.name
		if count[[2]string{r.profile, strings.ToLower(r.name)}] > 1 && r.ref != (ref.Ref{}) {
			out[i] = fmt.Sprintf("%s (%s)", r.name, r.ref.ID)
		}
	}
	return out
}

// sourceColumn is the SOURCE column: the source's name, or with -o wide
// the full ref. An unmanaged folder has none.
func sourceColumn(r ref.Ref, wide bool) string {
	switch {
	case r == (ref.Ref{}):
		return "-"
	case wide:
		return r.String()
	}
	return r.Source
}

func newListCmd(c *cli) *cobra.Command {
	var output string
	var sc scope
	var unmanaged, noCheck bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the profile's addons and whether they're up to date",
		Long: `List the profile's addons, each named by its title, with its status,
version and source.

It checks each addon's newest release, as check does: an installed addon
is up to date, has an update available, or is unknown when checking
failed. An addon can also be not installed (in config, but not installed)
or not in config (installed, but no longer in config). --no-check skips
the network, so it's instant, and installed addons are just installed.

-o wide shows the full ref (github:owner/repo) as the source, with the
newest version, install time and folders.

Folders in AddOns that Knapsack didn't install, such as addons installed by
hand or by another manager, are left alone. --unmanaged lists them too;
add one with add --force to have Knapsack manage it.`,
		Example: `  knapsack list
  knapsack list --no-check
  knapsack list -A -o wide
  knapsack list --unmanaged`,
		Args: sc.args(false),
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if noCheck {
				return c.buildApp(cmd, args)
			}
			return c.buildNetApp(cmd, args)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sel, err := sc.selector(cmd.Context(), c, nil)
			if err != nil {
				return err
			}
			listing, err := c.app.List(cmd.Context(), sel, app.ListOptions{Unmanaged: unmanaged, Check: !noCheck})
			c.stopProgress()
			if err != nil {
				return err
			}
			list := listing.Addons
			var failed []error
			for _, in := range list {
				if in.Err != nil {
					failed = append(failed, nameAddon("", in.Ref, in.Err))
				}
			}
			w := cmd.OutOrStdout()
			out := &printer{w: w}
			if output == "json" {
				js := make([]installedJSON, len(list))
				for i, in := range list {
					js[i] = toInstalledJSON(in)
				}
				if err := writeJSON(w, js); err != nil {
					return err
				}
				if err := noteUnmanaged(cmd, listing.Hidden, false); err != nil {
					return err
				}
				return finish(out, failed, nil)
			}
			if len(list) == 0 {
				if err := noteEmpty(cmd, sel, sc.all); err != nil {
					return err
				}
				return noteUnmanaged(cmd, listing.Hidden, false)
			}
			if !sc.all {
				if err := noteProfile(cmd, sel.Profiles[0]); err != nil {
					return err
				}
			}
			if err := printList(w, list, sc.all, output == "wide"); err != nil {
				return err
			}
			if err := noteUnmanaged(cmd, listing.Hidden, true); err != nil {
				return err
			}
			return finish(out, failed, nil)
		},
	}
	cmd.Flags().BoolVar(&noCheck, "no-check", false, "don't check for newer releases, so nothing is looked up online")
	cmd.Flags().BoolVar(&unmanaged, "unmanaged", false, "also list folders in AddOns that Knapsack didn't install")
	addOutputFlag(cmd, &output, "wide", "json")
	sc.addFlag(cmd)
	c.addProfileFlag(cmd)
	return cmd
}

// printList writes list's table, with a PROFILE column under -A and more
// columns, and the full ref as the source, with -o wide.
func printList(w io.Writer, list []app.Installed, all, wide bool) error {
	tw := newTable(w)
	out := &printer{w: tw}
	if all {
		out.printf("PROFILE\t")
	}
	if wide {
		out.printf("ADDON\tSTATUS\tVERSION\tAVAILABLE\tSOURCE\tINSTALLED\tFOLDERS\n")
	} else {
		out.printf("ADDON\tSTATUS\tVERSION\tSOURCE\n")
	}
	rows := make([]nameRow, len(list))
	for i, in := range list {
		rows[i] = nameRow{in.Profile, in.Name(), in.Ref}
	}
	names := uniqueNames(rows)
	for i, in := range list {
		if all {
			out.printf("%s\t", in.Profile)
		}
		name, version, src := names[i], orDash(in.Version), sourceColumn(in.Ref, wide)
		if wide {
			out.printf("%s\t%s\t%s\t%s\t%s\t%s\t%s\n", name, in.Status, version, orDash(in.Available), src,
				formatTime(in.InstalledAt), orDash(strings.Join(in.Folders, ",")))
		} else {
			out.printf("%s\t%s\t%s\t%s\n", name, in.Status, version, src)
		}
	}
	if out.err != nil {
		return out.err
	}
	return tw.Flush()
}

// detailsJSON is info's JSON output.
type detailsJSON struct {
	installedJSON
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
the title, author, notes and Interface numbers from its TOC. It looks
nothing up online.`,
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
					Author:        d.Author, Notes: d.Notes, Interfaces: d.Interfaces,
				})
			}
			tw := newTable(w)
			out := &printer{w: tw}
			field := func(name, value string) {
				if value != "" {
					out.printf("%s:\t%s\n", name, value)
				}
			}
			field("Profile", d.Profile)
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
