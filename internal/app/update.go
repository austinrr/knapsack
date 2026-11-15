package app

import (
	"context"
	"fmt"
	"slices"

	"github.com/austinrr/knapsack/internal/ref"
)

// UpdateOptions are update's flags (DESIGN.md §8.4).
type UpdateOptions struct {
	// Prune uninstalls addons that are in state but no longer in config.
	Prune bool
	// Force replaces folders in AddOns that no addon owns.
	Force bool
}

// target is one addon a bulk use case covers.
type target struct {
	name string // what the user typed, or "" when no addons were named
	ref  ref.Ref
	// prune means it's installed but not in config.
	prune bool
	err   error // set when name matched no addon
}

// targets picks the addons a bulk use case covers in one profile: those
// named, in the order given, or else every addon in config, in config
// order, then any only in state.
func targets(s *session, names []string) []target {
	inConfig := func(r ref.Ref) bool { return slices.Contains(s.profile.Addons, r) }
	known := s.known()
	if len(names) == 0 {
		out := make([]target, len(known))
		for i, r := range known {
			out[i] = target{ref: r, prune: !inConfig(r)}
		}
		return out
	}
	var out []target
	var seen []ref.Ref
	for _, name := range names {
		r, err := findAddon(s, known, name)
		if err == nil && slices.Contains(seen, r) {
			continue // named twice
		}
		seen = append(seen, r)
		out = append(out, target{name: name, ref: r, prune: err == nil && !inConfig(r), err: err})
	}
	return out
}

// eachProfile runs fn once for each profile in turn, "" meaning the current
// one, and none meaning just the current one. A failure that stops one
// profile, such as its lock being held, stops the rest too (DESIGN.md
// §6.5).
func (a *App) eachProfile(ctx context.Context, profiles []string, write bool, fn func(*session) error) error {
	if len(profiles) == 0 {
		profiles = []string{""}
	}
	var seen []string
	for _, p := range profiles {
		if p == "" {
			// Resolved here, so "" and the current profile's name don't
			// run it twice.
			current, err := a.CurrentProfile(ctx)
			if err != nil {
				return err
			}
			p = current
		}
		if slices.Contains(seen, p) {
			continue
		}
		seen = append(seen, p)
		if err := a.withProfile(ctx, p, write, fn); err != nil {
			return err
		}
	}
	return nil
}

// Update makes each selected profile match its config (DESIGN.md §8.4):
// addons in config but not installed are installed, and installed ones
// whose newest release isn't the installed one are upgraded. Releases are
// compared by SHA-256, never by version (§5.3). With opts.Prune, addons in
// state but not in config are uninstalled; without it, Update never
// removes anything, and reports them as ActionNotInConfig. Config is never
// changed.
//
// Results are grouped by profile, in the order the profiles were given,
// and within each in the order of targets. They're returned even when err
// is set, for the profiles done so far.
func (a *App) Update(ctx context.Context, sel Selector, opts UpdateOptions) ([]Result, error) {
	var results []Result
	err := a.eachProfile(ctx, sel.Profiles, writes, func(s *session) error {
		ts := targets(s, sel.Addons)
		out := make([]Result, len(ts))
		var toInstall []ref.Ref
		var at []int // index in out of each ref in toInstall

		// Prune first, so the folders an addon gave up are free for those
		// that follow.
		for i, t := range ts {
			res := Result{Profile: s.name, Name: t.name, Ref: t.ref}
			switch {
			case t.err != nil:
				res.Action, res.Err = ActionFailed, t.err
			case t.prune && !opts.Prune && t.name == "":
				// Left alone, but returned, so the caller can say why.
				res.Action, res.Folders = ActionNotInConfig, s.state.Addons[t.ref].Folders
				res.Version = s.state.Addons[t.ref].Version
				a.report(Event{Kind: EventSkipped, Profile: s.name, Ref: t.ref, Message: "not in config; --prune removes it"})
				out[i] = res
				continue
			case t.prune && !opts.Prune:
				res.Action, res.Err = ActionFailed, notInConfig(t.name, s.name)
			case t.prune:
				res.Action = ActionRemoved
				if err := ctx.Err(); err != nil {
					res.Action, res.Err = ActionFailed, err
				} else if err := a.removeOne(s, t.ref); err != nil {
					res.Action, res.Err = ActionFailed, explain(err, t.ref)
				}
			default:
				toInstall = append(toInstall, t.ref)
				at = append(at, i)
				continue
			}
			if res.Err != nil {
				a.report(Event{Kind: EventFailed, Profile: s.name, Name: t.name, Ref: t.ref, Err: res.Err})
			} else {
				a.report(Event{Kind: EventRemoved, Profile: s.name, Name: t.name, Ref: t.ref})
			}
			out[i] = res
		}
		for j, res := range a.installAll(ctx, s, toInstall, opts.Force) {
			res.Name = ts[at[j]].name
			out[at[j]] = res
		}
		results = append(results, out...)
		return nil
	})
	return results, err
}

func notInConfig(name, profile string) error {
	return &UserError{
		Msg:  fmt.Sprintf("%q is installed in profile %q but isn't in its config", name, profile),
		Hint: "add it back with `knapsack add`, or uninstall it with `knapsack update --prune`",
	}
}

// PlanAction is the change Check found for one addon.
type PlanAction string

// The actions in a Plan.
const (
	PlanInstall PlanAction = "install" // in config, not installed
	PlanUpgrade PlanAction = "upgrade" // the newest release isn't the installed one
	PlanPrune   PlanAction = "prune"   // installed, not in config; update --prune removes it
	PlanNone    PlanAction = "none"    // up to date
	PlanFailed  PlanAction = "failed"  // couldn't tell; Err says why
)

// PlanChecking is only for Watcher.Listed: the addon is being resolved.
// Check's result never has it.
const PlanChecking PlanAction = "checking"

// Plan is what update would do to one addon, as Check reports it.
type Plan struct {
	Profile string
	Name    string // what the user typed, if addons were named
	Ref     ref.Ref
	// Title is the addon's name from its installed TOC, "" when it isn't
	// installed or has none.
	Title  string
	Action PlanAction
	// Installed is the installed version, "" when it isn't installed.
	// Available is the newest release's, "" for prune or a failure.
	Installed string
	Available string
	Err       error // set when Action is PlanFailed
}

// DisplayName is how the planned addon is shown, by the same rule as
// Installed.Name.
func (p Plan) DisplayName() string { return displayName(p.Title, p.Ref, p.Name) }

// Check resolves each selected addon as Update would and reports what it
// would change, without changing anything (DESIGN.md §8.4). An installed
// addon whose source publishes no SHA-256 is downloaded, only to compare
// it. Prune candidates are reported whether or not update would prune.
// Like Update, plans are returned even when err is set.
func (a *App) Check(ctx context.Context, sel Selector, opts CheckOptions) ([]Plan, error) {
	var plans []Plan
	err := a.eachProfile(ctx, sel.Profiles, readOnly, func(s *session) error {
		ps, at := a.planSession(s, targets(s, sel.Addons))
		var done func(int)
		if w := opts.Watch; w != nil {
			shown := slices.Clone(ps)
			for _, i := range at {
				shown[i].Action = PlanChecking
			}
			w.Listed(shown)
			done = func(i int) { w.Finished(s.name, i, ps[i]) }
		}
		a.resolvePlans(ctx, s, ps, at, done)
		plans = append(plans, ps...)
		return nil
	})
	return plans, err
}

// CheckOptions configure Check.
type CheckOptions struct {
	// Watch, if not nil, sees each profile's plans as they're resolved.
	Watch Watcher[Plan]
}

// planSession is the part of Check that needs no network: a plan for each
// target, with its title and installed version, already final for a
// failed target or a prune candidate. The others, at the indexes in at,
// are for resolvePlans to finish.
func (a *App) planSession(s *session, ts []target) (plans []Plan, at []int) {
	plans = make([]Plan, len(ts))
	for i, t := range ts {
		plans[i] = Plan{Profile: s.name, Name: t.name, Ref: t.ref, Installed: s.state.Addons[t.ref].Version, Title: addonTitle(s, t.ref)}
		switch {
		case t.err != nil:
			plans[i].Action, plans[i].Err = PlanFailed, t.err
			a.report(Event{Kind: EventFailed, Profile: s.name, Name: t.name, Err: t.err})
		case t.prune:
			plans[i].Action = PlanPrune
		default:
			at = append(at, i)
		}
	}
	return plans, at
}

// resolvePlans resolves the plans at the indexes in at, in parallel, and
// sets what each would do. done, if not nil, is called with each plan's
// index as soon as it's set, from several goroutines at once.
func (a *App) resolvePlans(ctx context.Context, s *session, plans []Plan, at []int, done func(i int)) {
	refs := make([]ref.Ref, len(at))
	for j, i := range at {
		refs[j] = plans[i].Ref
	}
	got := a.fetchAll(ctx, s, refs, fetchIdentity, func(j int, f fetched) {
		// Each plan is set by only its own fetch, so they don't race.
		p := &plans[at[j]]
		p.Available = f.release.Version
		_, installed := s.state.Addons[p.Ref]
		switch {
		case f.err != nil:
			p.Action, p.Err, p.Available = PlanFailed, explain(f.err, p.Ref), ""
			a.report(Event{Kind: EventFailed, Profile: s.name, Ref: p.Ref, Err: p.Err})
		case !installed:
			p.Action = PlanInstall
		case f.current:
			p.Action = PlanNone
		default:
			p.Action = PlanUpgrade
		}
		if p.Action != PlanFailed {
			a.report(Event{Kind: EventChecked, Profile: s.name, Ref: p.Ref, Version: p.Available, Message: string(p.Action)})
		}
		if done != nil {
			done(at[j])
		}
	})
	discard(got)
}
