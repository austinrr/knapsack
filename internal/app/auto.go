package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/austinrr/knapsack/internal/discover"
	"github.com/austinrr/knapsack/internal/flavor"
)

// SkipReason is why AutoProfiles made no profile for a client folder.
type SkipReason string

// The reasons a client folder is skipped.
const (
	NotSkipped SkipReason = ""
	// SkipNoAddOns is a client never started, so it has no
	// Interface/AddOns folder yet.
	SkipNoAddOns SkipReason = "no-addons"
	// SkipNameTaken is a second copy of a client: the profile named after
	// it already uses the copy in TakenBy.
	SkipNameTaken SkipReason = "name-taken"
	// SkipFailed is any other problem with the folder; Err says what.
	SkipFailed SkipReason = "failed"
)

// AutoProfile is what AutoProfiles did with one client folder.
type AutoProfile struct {
	Game   string // the World of Warcraft folder
	Client string // the client folder in it, as named on disk: "_retail_"
	// Result is set when a profile was created or already existed. Skip
	// says why one wasn't instead.
	Result SetProfileResult
	Skip   SkipReason
	// TakenBy is, for SkipNameTaken, the profile with the name this client
	// would get, and TakenPath the AddOns folder it uses.
	TakenBy, TakenPath string
	// Err is the error that skipped it, with a hint, for SkipFailed and
	// SkipNameTaken.
	Err error
}

// AutoProfilesResult is what AutoProfiles found and did.
type AutoProfilesResult struct {
	// Searched are the places looked in when no game folder was given,
	// for -v and the error when nothing was found.
	Searched []string
	Profiles []AutoProfile
}

// AutoProfiles finds World of Warcraft and makes a profile for each client
// folder in it that has Interface/AddOns, named as SetProfile names one
// without a name: retail, forever, retail-ptr (DESIGN.md §8.4). game is
// the game folder to use; "" looks in the usual places for this OS. A
// profile that already uses a folder is left as it is, and a client whose
// default name another folder has is skipped with that error.
func (a *App) AutoProfiles(ctx context.Context, game string) (AutoProfilesResult, error) {
	var res AutoProfilesResult
	var games []string
	if game != "" {
		abs, err := filepath.Abs(expandHome(game, a.home))
		if err != nil {
			return res, fmt.Errorf("resolve path: %w", err)
		}
		if len(discover.Clients(abs)) == 0 {
			return res, &UserError{
				Msg:  fmt.Sprintf("%s has no game client folders (_retail_, _classic_, …) in it", abs),
				Hint: "pass the World of Warcraft folder, or a client folder without --auto",
			}
		}
		games = []string{abs}
	} else {
		var drives []string
		if runtime.GOOS == "windows" {
			drives = discover.Drives()
		}
		res.Searched = discover.Candidates(runtime.GOOS, a.home, drives)
		games = discover.Find(res.Searched)
		if len(games) == 0 {
			return res, &UserError{
				Msg:  "couldn't find World of Warcraft in the usual places",
				Hint: "pass its folder: --auto --path " + Quoted(filepath.Dir(ExampleClientFolder())) + " (-v lists where Knapsack looked)",
			}
		}
	}
	// Live clients first, across every game folder, so the profile that
	// becomes current, the first one created, isn't a PTR or beta. The
	// results are then put back in game and client order for showing.
	type found struct {
		game, client string
		order        int
	}
	var all []found
	for _, g := range games {
		for _, client := range discover.Clients(g) {
			all = append(all, found{g, client, len(all)})
		}
	}
	isTest := func(f found) bool {
		l, _ := flavor.Locate(f.client)
		return l.Test != ""
	}
	byLiveFirst := slices.Clone(all)
	slices.SortStableFunc(byLiveFirst, func(x, y found) int {
		switch tx, ty := isTest(x), isTest(y); {
		case tx == ty:
			return 0
		case ty:
			return -1
		}
		return 1
	})
	done := make([]AutoProfile, len(all))
	// What's done so far, in order, for an early return: the profiles
	// already made are reported even when a later one fails.
	partial := func() []AutoProfile {
		var out []AutoProfile
		for _, p := range done {
			if p.Client != "" {
				out = append(out, p)
			}
		}
		return out
	}
	for _, f := range byLiveFirst {
		if err := ctx.Err(); err != nil {
			res.Profiles = partial()
			return res, err
		}
		p := AutoProfile{Game: f.game, Client: f.client}
		dir := filepath.Join(f.game, f.client)
		if !isDir(filepath.Join(dir, "Interface", "AddOns")) {
			p.Skip = SkipNoAddOns
			done[f.order] = p
			continue
		}
		r, err := a.SetProfile(ctx, SetProfileRequest{Path: dir})
		var ue *UserError
		var taken *nameTakenError
		switch {
		case errors.As(err, &taken):
			p.Skip, p.TakenBy, p.TakenPath, p.Err = SkipNameTaken, taken.name, taken.path, err
		case errors.As(err, &ue):
			// One client can't be added; the others still can.
			p.Skip, p.Err = SkipFailed, err
		case err != nil:
			res.Profiles = partial()
			return res, err
		default:
			p.Result = r
		}
		done[f.order] = p
	}
	res.Profiles = done
	// Every client skipped: say so as an error, so a script that goes on
	// to add addons stops here.
	if !slices.ContainsFunc(done, func(p AutoProfile) bool { return p.Skip == NotSkipped }) {
		return res, &UserError{
			Msg:  "no profiles made: every client folder found was skipped",
			Hint: "see why above; a client that's never been started needs its Interface/AddOns folder first",
		}
	}
	return res, nil
}
