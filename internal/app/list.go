package app

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/austinrr/knapsack/internal/install"
	"github.com/austinrr/knapsack/internal/ref"
)

// Selector picks what a use case covers.
type Selector struct {
	// Profiles are the profiles to cover. Empty means the current one.
	Profiles []string
	// Addons are refs or short names. Empty means every addon in each
	// profile.
	Addons []string
}

// Status is how an addon's config and state compare.
type Status string

// The statuses List reports (DESIGN.md §8.4).
const (
	StatusInstalled   Status = "installed"     // in config and state
	StatusMissing     Status = "missing"       // in config, never installed
	StatusNotInConfig Status = "not in config" // installed, but not in config
)

// Installed is one addon in a profile, as List reports it.
type Installed struct {
	Profile     string
	Ref         ref.Ref
	Status      Status
	Version     string // "" when missing
	InstalledAt time.Time
	Folders     []string
}

// List reports the addons in each selected profile from config and state
// alone, without the network, so it's instant (DESIGN.md §8.4). Config's
// addons come first, in config order, then any only in state.
func (a *App) List(ctx context.Context, sel Selector) ([]Installed, error) {
	var out []Installed
	err := a.eachProfile(ctx, sel.Profiles, readOnly, func(s *session) error {
		for _, t := range targets(s, sel.Addons) {
			if t.err != nil {
				return t.err
			}
			out = append(out, installedInfo(s, t.ref))
		}
		return nil
	})
	return out, err
}

func installedInfo(s *session, r ref.Ref) Installed {
	in := Installed{Profile: s.name, Ref: r, Status: StatusMissing}
	addon, installed := s.state.Addons[r]
	if installed {
		in.Status = StatusInstalled
		if !slices.Contains(s.profile.Addons, r) {
			in.Status = StatusNotInConfig
		}
		in.Version, in.InstalledAt, in.Folders = addon.Version, addon.InstalledAt, addon.Folders
	}
	return in
}

// mainFolder guesses which of an addon's folders is the addon itself rather
// than a bundled library or module: the longest folder name contained in
// the ref's short name (baganator-mirror → Baganator), compared as
// lower-cased letters and digits only. Without one, it's the first folder
// not starting with "!" (the prefix libraries use to load first), then
// simply the first.
func mainFolder(r ref.Ref, folders []string) string {
	short := alnum(r.ShortName())
	best := ""
	for _, f := range folders {
		if name := alnum(f); name != "" && strings.Contains(short, name) && len(f) > len(best) {
			best = f
		}
	}
	if best != "" {
		return best
	}
	for _, f := range folders {
		if !strings.HasPrefix(f, "!") {
			return f
		}
	}
	return folders[0]
}

// alnum lower-cases s and keeps only letters and digits.
func alnum(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// Details is everything Info knows about an addon.
type Details struct {
	Installed
	// Title, Author, Notes and Interfaces come from the TOC the client
	// loads from the addon's first folder. They're empty when it isn't
	// installed or the TOC can't be read.
	Title      string
	Author     string
	Notes      string
	Interfaces []int
}

// Info describes one addon in the profile ("" for the current one). name is
// a ref or short name. Like List, it doesn't use the network; the title and
// notes are read from the installed TOC.
func (a *App) Info(ctx context.Context, profile, name string) (Details, error) {
	var d Details
	err := a.withProfile(ctx, profile, readOnly, func(s *session) error {
		r, err := findAddon(s, s.known(), name)
		if err != nil {
			return err
		}
		d.Installed = installedInfo(s, r)
		if len(d.Folders) == 0 {
			return nil
		}
		// If the folder was changed outside Knapsack, show what state knows.
		main := mainFolder(r, d.Folders)
		if _, file, err := install.TOC(filepath.Join(s.addonsDir, main), s.profile.Flavor); err == nil {
			d.Title, d.Author, d.Notes, d.Interfaces = file.Get("Title"), file.Get("Author"), file.Get("Notes"), file.Interfaces
		}
		return nil
	})
	return d, err
}
