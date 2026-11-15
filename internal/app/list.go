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
	"github.com/austinrr/knapsack/internal/toc"
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

// The statuses List reports (DESIGN.md §8.4). With ListOptions.Check, an
// installed addon is up to date, has an update available, or is unknown
// when the check failed; without it, it's just installed.
const (
	StatusInstalled       Status = "installed"        // in config and state, not checked
	StatusUpToDate        Status = "up to date"       // installed, and the newest release
	StatusUpdateAvailable Status = "update available" // installed, and a newer release exists
	StatusUnknown         Status = "unknown"          // the check failed; Err says why
	StatusMissing         Status = "not installed"    // in config, never installed
	StatusNotInConfig     Status = "not in config"    // installed, but not in config
	StatusUnmanaged       Status = "unmanaged"        // a folder in AddOns no addon owns
)

// StatusChecking is only for ListWatcher.Listed: the addon is being
// checked. List's result never has it.
const StatusChecking Status = "checking"

// Installed is one addon in a profile, as List reports it. For an
// unmanaged folder, Ref and InstalledAt are zero, Folders is that one
// folder, and Version is from its TOC; Name gives the folder.
type Installed struct {
	Profile string
	Ref     ref.Ref
	// Title is the addon's name from its TOC, "" when it isn't installed
	// or has none. For an unmanaged folder it's the folder's TOC title,
	// though Name shows the folder, which is what's in AddOns.
	Title       string
	Status      Status
	Version     string // "" when missing, or unknown for an unmanaged folder
	InstalledAt time.Time
	Folders     []string
	// Available is the newest release's version, and Err why checking
	// failed, when List checked (ListOptions.Check).
	Available string
	Err       error
}

// Name is how the addon is shown (displayName). An unmanaged folder is
// shown as the folder, which is what's in AddOns and what add --force
// replaces.
func (in Installed) Name() string {
	if in.Status == StatusUnmanaged {
		return in.Folders[0]
	}
	return displayName(in.Title, in.Ref, "")
}

// displayName is the one rule for naming an addon in output: its TOC
// title, else its repo or project name, else fallback, what the user typed
// for one that isn't in the profile. Commands accept all three (findAddon).
func displayName(title string, r ref.Ref, fallback string) string {
	switch {
	case title != "":
		return title
	case r != (ref.Ref{}):
		return r.ShortName()
	}
	return fallback
}

// ListOptions configure List.
type ListOptions struct {
	// Unmanaged includes each profile's unmanaged folders in the result.
	// Without it they're only counted.
	Unmanaged bool
	// Check resolves each addon as Check does, so statuses say whether
	// it's up to date. It needs the network and the App's sources.
	Check bool
	// Watch, if not nil, sees each profile's addons as they're checked,
	// so a terminal can show them before checking is done. It's used
	// only with Check.
	Watch ListWatcher
}

// ListWatcher sees List's addons before they're checked, and each as its
// check finishes. Profiles are listed one after another, each in full
// before the next.
type ListWatcher interface {
	// Listed is a profile's addons, in the order List returns them. Those
	// still to be checked have StatusChecking.
	Listed(addons []Installed)
	// Checked is the addon at index i of the profile's Listed addons, now
	// checked. It's called from several goroutines at once.
	Checked(profile string, i int, in Installed)
}

// Listing is what List reports.
type Listing struct {
	Addons []Installed
	// Hidden is how many unmanaged folders were left out of Addons
	// because ListOptions.Unmanaged wasn't set.
	Hidden int
}

// List reports the addons in each selected profile from config, state and
// a listing of AddOns (DESIGN.md §8.4). Without opts.Check it doesn't use
// the network, so it's instant; with it, each addon is checked as Check
// does. Config's addons come first, in config order, then any only in
// state, then the profile's unmanaged folders. Those aren't addons, so
// they're left out when sel names addons.
func (a *App) List(ctx context.Context, sel Selector, opts ListOptions) (Listing, error) {
	var out Listing
	err := a.eachProfile(ctx, sel.Profiles, readOnly, func(s *session) error {
		ts := targets(s, sel.Addons)
		for _, t := range ts {
			if t.err != nil {
				return t.err
			}
		}
		plans, at := a.planSession(s, ts)
		addons := make([]Installed, len(ts), len(ts)+1)
		for i, t := range ts {
			// The plan already read each title.
			addons[i] = installedInfo(s, t.ref, plans[i].Title)
		}
		if len(sel.Addons) == 0 {
			// Unmanaged folders aren't addons, so they're left out when
			// addons are named.
			names := unmanagedFolders(s)
			if opts.Unmanaged {
				addons = append(addons, unmanagedInfo(s, names)...)
			} else {
				out.Hidden += len(names)
			}
		}
		if opts.Check {
			a.checkListed(ctx, s, addons, plans, at, opts.Watch)
		}
		out.Addons = append(out.Addons, addons...)
		return nil
	})
	return out, err
}

// checkListed checks the addons listed for one profile, which start with
// one for each plan, telling watch, if not nil, as each finishes.
func (a *App) checkListed(ctx context.Context, s *session, addons []Installed, plans []Plan, at []int, watch ListWatcher) {
	if watch != nil {
		shown := slices.Clone(addons)
		for _, i := range at {
			shown[i].Status = StatusChecking
		}
		watch.Listed(shown)
	}
	a.resolvePlans(ctx, s, plans, at, func(i int) {
		// Each addon is set by only its own check, so they don't race.
		addons[i].withPlan(plans[i])
		if watch != nil {
			watch.Checked(s.name, i, addons[i])
		}
	})
}

// unmanagedInfo describes the unmanaged folders names.
func unmanagedInfo(s *session, names []string) []Installed {
	out := make([]Installed, len(names))
	for i, name := range names {
		in := Installed{Profile: s.name, Status: StatusUnmanaged, Folders: []string{name}}
		// A folder without a TOC for this client is still listed, as
		// something the user put there.
		if _, file, err := install.TOC(filepath.Join(s.addonsDir, name), s.profile.Flavor); err == nil {
			in.Version, in.Title = file.Get("Version"), toc.Plain(file.Get("Title"))
		}
		out[i] = in
	}
	return out
}

// unmanagedFolders lists the folders in the profile's AddOns that no addon
// in state owns. They're extra information, so an AddOns folder that
// can't be read has none rather than failing List; commands that write to
// it report the error.
func unmanagedFolders(s *session) []string {
	var owned []string
	for _, addon := range s.state.Addons {
		owned = append(owned, addon.Folders...)
	}
	names, err := install.Unmanaged(s.addonsDir, owned)
	if err != nil {
		return nil
	}
	return names
}

// withPlan turns an installed addon's status into what checking found.
func (in *Installed) withPlan(p Plan) {
	in.Available = p.Available
	switch p.Action {
	case PlanFailed:
		in.Err = p.Err
		if in.Status == StatusInstalled {
			in.Status = StatusUnknown
		}
	case PlanNone:
		in.Status = StatusUpToDate
	case PlanUpgrade:
		in.Status = StatusUpdateAvailable
	case PlanInstall, PlanPrune:
		// Not installed or not in config either way.
	}
}

// addonTitle is the title in an installed addon's TOC, as the client
// loads it from the addon's main folder, without color codes; "" when it
// isn't installed or the TOC can't be read.
func addonTitle(s *session, r ref.Ref) string {
	addon, ok := s.state.Addons[r]
	if !ok || len(addon.Folders) == 0 {
		return ""
	}
	_, file, err := install.TOC(filepath.Join(s.addonsDir, mainFolder(r, addon.Folders)), s.profile.Flavor)
	if err != nil {
		return ""
	}
	return toc.Plain(file.Get("Title"))
}

// installedInfo is what config and state say about r, with its title.
func installedInfo(s *session, r ref.Ref, title string) Installed {
	in := Installed{Profile: s.name, Ref: r, Status: StatusMissing, Title: title}
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
	// Author, Notes and Interfaces come from the TOC the client loads
	// from the addon's main folder, as Installed.Title does. They're empty
	// when it isn't installed or the TOC can't be read.
	Author     string
	Notes      string
	Interfaces []int
}

// Info describes one addon in the profile ("" for the current one). name is
// a ref, short name or title. It doesn't use the network; the title and
// notes are read from the installed TOC.
func (a *App) Info(ctx context.Context, profile, name string) (Details, error) {
	var d Details
	err := a.withProfile(ctx, profile, readOnly, func(s *session) error {
		r, err := findAddon(s, s.known(), name)
		if err != nil {
			return err
		}
		d.Installed = installedInfo(s, r, "")
		if len(d.Folders) == 0 {
			return nil
		}
		// One read for every TOC field. If the folder was changed outside
		// Knapsack, show what state knows.
		main := mainFolder(r, d.Folders)
		if _, file, err := install.TOC(filepath.Join(s.addonsDir, main), s.profile.Flavor); err == nil {
			d.Title, d.Author, d.Notes, d.Interfaces = toc.Plain(file.Get("Title")), file.Get("Author"), file.Get("Notes"), file.Interfaces
		}
		return nil
	})
	return d, err
}
