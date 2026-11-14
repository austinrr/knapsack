package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

func newAddCmd(c *cli) *cobra.Command {
	var force bool
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
		Args:    minArgs("addon"),
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			refs := make([]ref.Ref, len(args))
			for i, a := range args {
				r, err := ref.Parse(a)
				if err != nil {
					return err
				}
				refs[i] = r
			}
			results, err := c.app.Add(cmd.Context(), c.profileName(), refs, force)
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			for _, res := range results {
				switch res.Action {
				case app.ActionInstalled:
					out.printf("Added %s %s.\n", res.Ref, res.Version)
				case app.ActionAdopted:
					out.printf("Added %s %s, which was already installed.\n", res.Ref, res.Version)
				case app.ActionUnchanged:
					out.printf("%s is already added.\n", res.Ref)
				case app.ActionFailed, app.ActionRemoved:
					// Failures are reported by finish; add never removes.
				}
			}
			return c.finish(cmd, out, results)
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
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			for _, res := range results {
				if res.Action == app.ActionRemoved {
					out.printf("Removed %s.\n", res.Ref)
				}
			}
			return c.finish(cmd, out, results)
		},
	}
	c.addProfileFlag(cmd)
	return cmd
}

// finish reports the failures among results, one error and hint each, on
// stderr after the successes. Any failure makes the command exit 2 (DESIGN.md
// §8.6).
func (c *cli) finish(cmd *cobra.Command, out *printer, results []app.Result) error {
	if out.err != nil {
		return out.err
	}
	failed := false
	for _, res := range results {
		if res.Err == nil {
			continue
		}
		failed = true
		err := res.Err
		// Name the addon, unless the error already does, as source errors
		// do ("github owner/repo: …").
		subject, id := res.Ref.String(), res.Ref.ID
		if res.Ref == (ref.Ref{}) {
			subject, id = res.Name, res.Name
		}
		if !strings.Contains(err.Error(), id) {
			err = fmt.Errorf("%s: %w", subject, err)
		}
		renderError(cmd.ErrOrStderr(), err, c.verbose)
	}
	if failed {
		return errReported
	}
	return nil
}

// installedJSON is one addon in list and info's JSON output.
type installedJSON struct {
	Ref         string     `json:"ref"`
	Status      string     `json:"status"`
	Version     string     `json:"version,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	Folders     []string   `json:"folders,omitempty"`
}

func toInstalledJSON(in app.Installed) installedJSON {
	j := installedJSON{Ref: in.Ref.String(), Status: string(in.Status), Version: in.Version, Folders: in.Folders}
	if !in.InstalledAt.IsZero() {
		t := in.InstalledAt.UTC()
		j.InstalledAt = &t
	}
	return j
}

func newListCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List the profile's addons",
		Long: `List the profile's addons and whether each is installed.

It reads only config and state, never the network, so it's instant. An
addon is installed, missing (in config but not installed), or not in config
(installed, but no longer in config). Use check to see what's outdated.`,
		Args:    noArgs,
		PreRunE: c.buildApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var sel app.Selector
			if p := c.profileName(); p != "" {
				sel.Profiles = []string{p}
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
				// Like kubectl, an empty result is a note on stderr, so
				// stdout stays empty for scripts.
				_, err := fmt.Fprintln(cmd.ErrOrStderr(), "No addons in this profile.")
				return err
			}
			tw := newTable(w)
			out := &printer{w: tw}
			wide := output == "wide"
			if wide {
				out.printf("ADDON\tSTATUS\tVERSION\tINSTALLED\tFOLDERS\n")
			} else {
				out.printf("ADDON\tSTATUS\tVERSION\n")
			}
			for _, in := range list {
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

// lineReporter prints progress on stderr as lines: warnings always, and
// each step with --verbose. Failures aren't printed here; the command
// reports them at the end with their hints. A mutex keeps lines from
// interleaving, since events arrive from parallel downloads.
type lineReporter struct {
	mu      sync.Mutex
	w       io.Writer
	verbose bool
}

func (r *lineReporter) Event(e app.Event) {
	var line string
	switch {
	case e.Kind == app.EventWarned:
		line = "warning: " + e.Message
	case !r.verbose:
		return
	case e.Kind == app.EventResolving:
		line = "resolving " + e.Ref.String()
	case e.Kind == app.EventDownloading:
		line = "downloading " + e.Ref.String() + " " + e.Version
	case e.Kind == app.EventSkipped:
		line = e.Ref.String() + ": " + e.Message
	default:
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Progress that can't be written isn't worth failing the command for.
	_, _ = fmt.Fprintln(r.w, line)
}
