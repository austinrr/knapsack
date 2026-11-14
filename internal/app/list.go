package app

import (
	"context"
	"path/filepath"
	"slices"
	"time"

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
	profiles := sel.Profiles
	if len(profiles) == 0 {
		profiles = []string{""}
	}
	var out []Installed
	for _, profile := range profiles {
		err := a.withProfile(ctx, profile, func(s *session) error {
			refs := dedupe(append(slices.Clone(s.profile.Addons), s.state.Refs()...))
			if len(sel.Addons) > 0 {
				refs = nil
				for _, name := range sel.Addons {
					r, err := findAddon(s, name)
					if err != nil {
						return err
					}
					refs = dedupe(append(refs, r))
				}
			}
			for _, r := range refs {
				out = append(out, installedInfo(s, r))
			}
			return nil
		})
		if err != nil {
			return out, err
		}
	}
	return out, nil
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
	err := a.withProfile(ctx, profile, func(s *session) error {
		r, err := findAddon(s, name)
		if err != nil {
			return err
		}
		d.Installed = installedInfo(s, r)
		if len(d.Folders) == 0 {
			return nil
		}
		// If the folder was changed outside Knapsack, show what state knows.
		if _, file, err := install.TOC(filepath.Join(s.addonsDir, d.Folders[0]), s.profile.Flavor); err == nil {
			d.Title, d.Author, d.Notes, d.Interfaces = file.Get("Title"), file.Get("Author"), file.Get("Notes"), file.Interfaces
		}
		return nil
	})
	return d, err
}
