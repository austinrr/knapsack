package app

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/install"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/state"
)

// parallelism bounds concurrent resolves and downloads (DESIGN.md §6.5).
const parallelism = 6

// session is one use case's view of a profile, loaded under its lock.
type session struct {
	name      string
	profile   config.Profile
	state     *state.State
	addonsDir string
}

// known returns every addon in the profile: config's, in config order, then
// any only in state.
func (s *session) known() []ref.Ref {
	return dedupe(append(slices.Clone(s.profile.Addons), s.state.Refs()...))
}

// Whether a use case changes AddOns. Only those that do clean it up first,
// so List and Info work on a read-only or missing AddOns folder.
const (
	readOnly = false
	writes   = true
)

// withProfile runs fn for the named profile, or the current one when name
// is "", holding the profile's lock. It loads config and state fresh under
// the lock. When the use case writes, it first cleans up anything an
// interrupted run left in AddOns.
func (a *App) withProfile(ctx context.Context, name string, write bool, fn func(*session) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if name == "" {
		current, err := a.CurrentProfile(ctx)
		if err != nil {
			return err
		}
		name = current
	}
	if err := config.ValidateProfileName(name); err != nil {
		return unknownProfile(name)
	}
	return a.withProfileLock(name, func() error {
		c, err := a.config.Load()
		if err != nil {
			return err
		}
		p, ok := c.Profiles[name]
		if !ok {
			return unknownProfile(name)
		}
		if err := cleanup(write, p.Path); err != nil {
			return &UserError{
				Msg:  fmt.Sprintf("profile %q: %v", name, err),
				Hint: "check the profile's AddOns folder exists and is writable",
				Err:  err,
			}
		}
		st, err := a.state.Load(name)
		if err != nil {
			return err
		}
		return fn(&session{name: name, profile: p, state: st, addonsDir: p.Path})
	})
}

// Add installs each ref into the profile ("" for the current one), then
// records it in state and config, in that order (DESIGN.md §5.4). A ref
// already in config is left alone. One that's installed but not in config,
// as after an interrupted add, is adopted: recorded in config without
// reinstalling. force replaces folders that no addon owns.
func (a *App) Add(ctx context.Context, profile string, refs []ref.Ref, force bool) ([]Result, error) {
	var results []Result
	err := a.withProfile(ctx, profile, writes, func(s *session) error {
		refs := dedupe(refs)
		results = make([]Result, len(refs))
		var toInstall []ref.Ref
		var at []int // index in results of each ref in toInstall
		for i, r := range refs {
			switch {
			case slices.Contains(s.profile.Addons, r):
				a.report(Event{Kind: EventSkipped, Profile: s.name, Ref: r, Message: "already added"})
				results[i] = Result{Ref: r, Action: ActionUnchanged, Version: s.state.Addons[r].Version}
			case hasKey(s.state.Addons, r):
				a.report(Event{Kind: EventSkipped, Profile: s.name, Ref: r, Message: "already installed; adding it to config"})
				results[i] = Result{Ref: r, Action: ActionAdopted, Version: s.state.Addons[r].Version, Folders: s.state.Addons[r].Folders}
			default:
				toInstall = append(toInstall, r)
				at = append(at, i)
			}
		}
		// Results stay in the order the refs were given.
		for j, res := range a.installAll(ctx, s, toInstall, force) {
			results[at[j]] = res
		}

		var record []ref.Ref
		for _, res := range results {
			if res.Action == ActionInstalled || res.Action == ActionAdopted {
				record = append(record, res.Ref)
			}
		}
		_, err := a.changeConfig(func(c *config.Config) error {
			p := c.Profiles[s.name]
			for _, r := range record {
				if !slices.Contains(p.Addons, r) {
					p.Addons = append(p.Addons, r)
				}
			}
			c.Profiles[s.name] = p
			return nil
		})
		return err
	})
	return results, err
}

// fetched is a release resolved and downloaded, ready to install.
type fetched struct {
	release source.Release
	path    string // the downloaded archive, removed after installing
	sum     string
	err     error
}

// installAll resolves and downloads refs in parallel, then installs them
// one at a time, in order, so folder checks never race. Each installed
// addon is saved to state at once, so files on disk are never left
// unrecorded for longer than one install.
func (a *App) installAll(ctx context.Context, s *session, refs []ref.Ref, force bool) []Result {
	got := make([]fetched, len(refs))
	var g errgroup.Group
	g.SetLimit(parallelism)
	for i, r := range refs {
		g.Go(func() error {
			got[i] = a.fetch(ctx, s, r)
			return nil // failures are per addon, in got[i].err
		})
	}
	_ = g.Wait()
	defer func() {
		for _, f := range got {
			if f.path != "" {
				_ = os.Remove(f.path) // best effort; the cache is always safe to delete
			}
		}
	}()

	clientInterface, _ := flavor.ClientInterface(s.addonsDir, s.profile.Flavor)
	results := make([]Result, len(refs))
	for i, r := range refs {
		res := Result{Ref: r, Version: got[i].release.Version}
		err := got[i].err
		if err == nil {
			res.Folders, res.Warnings, err = a.installOne(ctx, s, r, got[i], clientInterface, force)
		}
		if err != nil {
			res.Action, res.Err = ActionFailed, explain(err, r)
			a.report(Event{Kind: EventFailed, Profile: s.name, Ref: r, Version: res.Version, Err: res.Err})
		} else {
			res.Action = ActionInstalled
			for _, w := range res.Warnings {
				a.report(Event{Kind: EventWarned, Profile: s.name, Ref: r, Version: res.Version, Message: w})
			}
			a.report(Event{Kind: EventInstalled, Profile: s.name, Ref: r, Version: res.Version})
		}
		results[i] = res
	}
	return results
}

// fetch resolves r's newest release and downloads it into the cache.
func (a *App) fetch(ctx context.Context, s *session, r ref.Ref) fetched {
	a.report(Event{Kind: EventResolving, Profile: s.name, Ref: r})
	rel, err := a.sources.Resolve(ctx, r, s.profile.Flavor)
	if err != nil {
		return fetched{err: err}
	}
	a.report(Event{Kind: EventDownloading, Profile: s.name, Ref: r, Version: rel.Version})
	path, sum, err := httpx.Download(ctx, a.http, rel.DownloadURL, a.paths.CacheDir, rel.SHA256)
	return fetched{release: rel, path: path, sum: sum, err: err}
}

// installOne installs one downloaded release and records it in state.
func (a *App) installOne(ctx context.Context, s *session, r ref.Ref, f fetched, clientInterface int, force bool) ([]string, []string, error) {
	folders, warnings, err := install.Install(ctx, f.path, s.addonsDir, install.Options{
		Flavor:          s.profile.Flavor,
		ClientInterface: clientInterface,
		Previous:        s.state.Addons[r].Folders,
		Others:          othersFolders(s.state, r),
		Force:           force,
	})
	if err != nil {
		return nil, nil, err
	}
	previous, had := s.state.Addons[r]
	s.state.Addons[r] = state.Addon{
		Version:     f.release.Version,
		SHA256:      f.sum,
		InstalledAt: time.Now(),
		Folders:     folders,
	}
	if err := a.state.Save(s.name, s.state); err != nil {
		// Undo the change in memory too, so a bad entry can't make every
		// later save in this command fail, or be saved by one that works.
		if had {
			s.state.Addons[r] = previous
		} else {
			delete(s.state.Addons, r)
		}
		return nil, nil, fmt.Errorf("installed, but saving state failed: %w", err)
	}
	return folders, warnings, nil
}

// othersFolders lists the folders every addon but r owns.
func othersFolders(st *state.State, r ref.Ref) []string {
	var out []string
	for other, addon := range st.Addons {
		if other != r {
			out = append(out, addon.Folders...)
		}
	}
	return out
}

// Remove uninstalls addons from the profile ("" for the current one). Each
// name is a ref or a short name (DESIGN.md §8.5). Only the folders state
// records are deleted, then the addon leaves state and config, in that
// order. An addon in config that was never installed just leaves config.
func (a *App) Remove(ctx context.Context, profile string, names []string) ([]Result, error) {
	var results []Result
	err := a.withProfile(ctx, profile, writes, func(s *session) error {
		known := s.known()
		var removed []ref.Ref
		for _, name := range names {
			// Ctrl-C stops before the next addon; what's removed so far
			// still leaves config below.
			err := ctx.Err()
			var r ref.Ref
			if err == nil {
				r, err = findAddon(s, known, name)
			}
			if err == nil {
				err = a.removeOne(s, r)
			}
			res := Result{Name: name, Ref: r, Action: ActionRemoved}
			if err != nil {
				res.Action, res.Err = ActionFailed, explain(err, r)
				a.report(Event{Kind: EventFailed, Profile: s.name, Name: name, Ref: r, Err: res.Err})
			} else {
				removed = append(removed, r)
				a.report(Event{Kind: EventRemoved, Profile: s.name, Name: name, Ref: r})
			}
			results = append(results, res)
		}
		_, err := a.changeConfig(func(c *config.Config) error {
			p := c.Profiles[s.name]
			p.Addons = slices.DeleteFunc(p.Addons, func(r ref.Ref) bool { return slices.Contains(removed, r) })
			c.Profiles[s.name] = p
			return nil
		})
		return err
	})
	return results, err
}

// removeOne deletes r's folders and drops it from state.
func (a *App) removeOne(s *session, r ref.Ref) error {
	addon, ok := s.state.Addons[r]
	if !ok {
		return nil // only in config
	}
	if err := install.Remove(s.addonsDir, addon.Folders); err != nil {
		return err
	}
	delete(s.state.Addons, r)
	if err := a.state.Save(s.name, s.state); err != nil {
		return fmt.Errorf("removed the folders, but saving state failed: %w", err)
	}
	return nil
}

// findAddon resolves a name the user typed to an addon in the profile's
// config or state: a ref or URL, or a short name that matches exactly one
// addon (DESIGN.md §8.5).
// known is s.known(), passed in so a lookup of several names builds it
// once. On error the returned Ref is zero.
func findAddon(s *session, known []ref.Ref, name string) (ref.Ref, error) {
	if r, err := ref.Parse(name); err == nil {
		if slices.Contains(known, r) {
			return r, nil
		}
		return ref.Ref{}, notInProfile(name, s.name)
	}
	var matches []ref.Ref
	for _, r := range known {
		if strings.EqualFold(r.ShortName(), name) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return ref.Ref{}, notInProfile(name, s.name)
	}
	listed := make([]string, len(matches))
	for i, r := range matches {
		listed[i] = r.String()
	}
	return ref.Ref{}, &UserError{
		Msg:  fmt.Sprintf("%q matches several addons: %s", name, strings.Join(listed, ", ")),
		Hint: "use the full ref",
	}
}

func notInProfile(name, profile string) error {
	return &UserError{
		Msg:  fmt.Sprintf("%q isn't in profile %q", name, profile),
		Hint: "list its addons with: knapsack list",
	}
}

func (a *App) report(e Event) { a.reporter.Event(e) }

// cleanup runs install.Cleanup on AddOns when the use case writes to it.
func cleanup(write bool, addonsDir string) error {
	if !write {
		return nil
	}
	return install.Cleanup(addonsDir)
}

// dedupe returns refs without repeats, in their first order.
func dedupe(refs []ref.Ref) []ref.Ref {
	var out []ref.Ref
	for _, r := range refs {
		if !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	return out
}

func hasKey[K comparable, V any](m map[K]V, k K) bool {
	_, ok := m[k]
	return ok
}
