package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/discover"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

// ProfileInfo describes one profile, for listing.
type ProfileInfo struct {
	Name    string
	Path    string
	Flavor  flavor.Flavor
	Addons  []ref.Ref
	Current bool
}

// SetProfileRequest is what `profiles create` and `profiles set` ask for. Empty fields are
// left as they are on an existing profile.
type SetProfileRequest struct {
	// Name is the profile's name. "" names it after Path (DESIGN.md §8.4):
	// the profile that already uses that folder, else its flavor.
	Name   string
	Path   string // relative paths are resolved against the working directory
	Flavor string // "" infers it from the client folder in the path
	// Must says whether the profile must be new, must exist already, or
	// either, which SetProfile otherwise allows.
	Must ProfileMust
}

// ProfileMust is what a SetProfileRequest requires of the profile.
type ProfileMust int

// What a SetProfileRequest can require: anything, as for --auto, a new
// profile, as `profiles create` does, or an existing one, as `profiles
// set` does.
const (
	MustAny ProfileMust = iota
	MustBeNew
	MustExist
)

// SetProfileResult says what SetProfile did.
type SetProfileResult struct {
	Profile ProfileInfo
	Created bool
	// Changed is false when the profile already had these settings; config
	// isn't rewritten then, so its comments survive.
	Changed bool
	// MadeCurrent is true when this became the current profile, which
	// happens for the first profile created.
	MadeCurrent bool
}

// SetProfile creates a profile or updates an existing one. On update, only
// the fields given change, as with kubectl's set-context. The flavor is
// inferred from the path when not given; an explicit flavor that contradicts
// the path's client folder is an error.
func (a *App) SetProfile(ctx context.Context, req SetProfileRequest) (SetProfileResult, error) {
	if err := ctx.Err(); err != nil {
		return SetProfileResult{}, err
	}
	named := req.Name != ""
	if !named {
		name, path, err := a.nameForPath(req)
		if err != nil {
			return SetProfileResult{}, err
		}
		req.Name, req.Path = name, path
	}
	if err := config.ValidateProfileName(req.Name); err != nil {
		return SetProfileResult{}, &UserError{Msg: err.Error()}
	}

	var res SetProfileResult
	err := a.withProfileLock(req.Name, func() error {
		changed, err := a.changeConfig(func(c *config.Config) error {
			p, exists := c.Profiles[req.Name]
			switch {
			case exists && req.Must == MustBeNew:
				return profileExists(req.Name, p.Path, named)
			case !exists && req.Must == MustExist:
				return &UserError{
					Msg:  fmt.Sprintf("profile %q doesn't exist", req.Name),
					Hint: fmt.Sprintf("make it with `knapsack profiles create %s --path <client folder>`, or list profiles with `knapsack profiles list`", req.Name),
				}
			}
			// A name taken from the flavor only ever creates a profile or
			// matches the one already using the folder; it never moves
			// another to a new folder. Checked again under the lock, in
			// case another process made the profile meanwhile.
			if !named && exists && !config.SamePath(p.Path, req.Path) {
				return flavorNameTaken(req.Name, p.Path)
			}
			if !exists && req.Path == "" {
				return &UserError{
					Msg:  fmt.Sprintf("profile %q doesn't exist yet, so --path is required", req.Name),
					Hint: pathHint(),
				}
			}
			if req.Path != "" {
				path, err := addonsDir(req.Path, a.home)
				if err != nil {
					return err
				}
				p.Path = path
			}
			f, err := chooseFlavor(req.Flavor, p.Path, p.Flavor, req.Path != "")
			if err != nil {
				return err
			}
			p.Flavor = f
			if other := profileWithPath(c, p.Path, req.Name); other != "" {
				return &UserError{
					Msg:  fmt.Sprintf("profile %q already uses %s", other, p.Path),
					Hint: "each AddOns folder can belong to only one profile",
				}
			}

			c.Profiles[req.Name] = p
			res.Created = !exists
			// Only the first profile created becomes current. Changing an
			// existing one never does, even when none is current.
			if !exists && len(c.Profiles) == 1 && c.CurrentProfile == "" {
				c.CurrentProfile = req.Name
				res.MadeCurrent = true
			}
			res.Profile = profileInfo(c, req.Name)
			return nil
		})
		res.Changed = changed
		return err
	})
	return res, err
}

// profileExists is the error for creating a profile that's already there:
// named, or named after a folder that already has one.
func profileExists(name, path string, named bool) error {
	msg := fmt.Sprintf("profile %q already exists", name)
	if !named {
		msg = fmt.Sprintf("%s already has profile %q", path, name)
	}
	return &UserError{
		Msg:  msg,
		Hint: fmt.Sprintf("change it with `knapsack profiles set %s --path <folder>` or `--flavor <flavor>`", name),
	}
}

// nameForPath names the profile for a create without a name: the
// profile that already uses req.Path, so running it again changes nothing,
// or else the flavor, which is what most people have one profile per.
// It also returns req.Path made absolute.
func (a *App) nameForPath(req SetProfileRequest) (name, path string, err error) {
	if req.Path == "" {
		return "", "", &UserError{
			Msg:  "profiles create needs --path, or --auto",
			Hint: "find the game with `knapsack profiles create --auto`, or pass a client folder, quoted: --path " + Quoted(ExampleClientFolder()),
		}
	}
	if path, err = addonsDir(req.Path, a.home); err != nil {
		return "", "", err
	}
	c, err := a.config.Load()
	if err != nil {
		return "", "", err
	}
	if name := profileWithPath(c, path, ""); name != "" {
		return name, path, nil
	}
	f, err := chooseFlavor(req.Flavor, path, "", true)
	if err != nil {
		return "", "", err
	}
	// A test client sits beside its live client, which should get the
	// plain flavor name, so it's named after both: retail-ptr.
	name = string(f)
	if l, ok := flavor.Locate(path); ok && l.Test != "" {
		name += "-" + l.Test
	}
	if p, exists := c.Profiles[name]; exists {
		return "", "", flavorNameTaken(name, p.Path)
	}
	return name, path, nil
}

// flavorNameTaken is the error when a profile would be named after its
// flavor, but a profile of that name already uses another folder: a second
// install of the same client, such as a test copy, needs its own name.
func flavorNameTaken(name, existing string) error {
	return &UserError{
		Msg: fmt.Sprintf("profile %q already uses %s", name, existing),
		Hint: fmt.Sprintf("give this one its own name with `knapsack profiles create <name> --path …`, or move %q here: `knapsack profiles set %s --path …`",
			name, name),
		Err: &NameTakenError{Name: name, Path: existing},
	}
}

// NameTakenError is the cause of the error for a profile that would be
// named after its flavor when that name is a profile for another folder:
// Name is the profile, and Path the AddOns folder it uses. Match it with
// errors.As.
type NameTakenError struct{ Name, Path string }

func (e *NameTakenError) Error() string { return fmt.Sprintf("profile %q uses %s", e.Name, e.Path) }

// ExampleClientFolder is a client folder where the game usually is on
// this OS, for hints and help: a profile's --path can be the client
// folder, and Knapsack finds Interface/AddOns in it.
func ExampleClientFolder() string {
	switch runtime.GOOS {
	case "windows":
		return `C:\Program Files (x86)\World of Warcraft\_retail_`
	case "darwin":
		return "/Applications/World of Warcraft/_retail_"
	}
	// Linux runs the game under Wine; this is Lutris's default prefix.
	// $HOME, not ~: shells expand it inside the quotes the hint adds.
	return "$HOME/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft/_retail_"
}

// pathHint is the hint for a --path that isn't usable, with the example
// quoted, since the usual path has spaces in it.
func pathHint() string {
	return "pass the game's client folder, quoted: --path " + Quoted(ExampleClientFolder())
}

// Quoted puts path in double quotes for a hint, as every shell takes it,
// Windows' included. Unlike %q, it leaves backslashes alone: C:\Program
// Files stays as it's typed.
func Quoted(path string) string { return `"` + path + `"` }

// addonsDir makes path absolute and finds the AddOns folder it means: the
// path itself, or Interface/AddOns in a client folder (…/_retail_), or
// AddOns in its Interface folder, so the shortest path to type works. It
// checks the folder exists, which catches typos before they're saved. Given
// the World of Warcraft folder itself, the error lists its clients.
func addonsDir(path, home string) (string, error) {
	abs, err := filepath.Abs(expandHome(path, home))
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir():
		return "", &UserError{
			Msg:  fmt.Sprintf("%s is not a directory", abs),
			Hint: pathHint(),
			Err:  err,
		}
	case err != nil:
		// Permission denied and the like: the folder may well exist, so
		// say what actually went wrong.
		return "", fmt.Errorf("check --path: %w", err)
	}
	if sub := addonsSubdir(abs); sub != "" {
		addons := filepath.Join(abs, sub)
		if isDir(addons) {
			return addons, nil
		}
		// A client that's never been started has no AddOns folder yet.
		// The client folder itself must never be the AddOns folder: WoW
		// wouldn't load addons from it.
		return "", &UserError{
			Msg:  fmt.Sprintf("%s has no %s folder yet", abs, sub),
			Hint: "start that client once, or make the folder: " + Quoted(addons),
		}
	}
	if clients := discover.Clients(abs); len(clients) > 0 {
		return "", &UserError{
			Msg:  fmt.Sprintf("%s is the game folder, with %s in it", abs, strings.Join(clients, ", ")),
			Hint: "pass one client folder: --path " + Quoted(filepath.Join(abs, clients[0])),
		}
	}
	return abs, nil
}

// expandHome expands a leading ~ to home, the App's home directory. Shells
// don't expand it inside quotes, which a path with spaces needs.
func expandHome(path, home string) string { return ExpandHome(path, home) }

// ExpandHome replaces a leading ~ in path with home, as a shell would
// outside quotes, so a quoted "~/…" works too. Without a home, path is
// returned as it is.
func ExpandHome(path, home string) string {
	if rest, ok := strings.CutPrefix(path, "~"); ok && home != "" && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		return home + rest
	}
	return path
}

// ShortenHome shows a path under home as ~/…, which ExpandHome, and so
// --path, reads back. Windows paths are left alone: ~ isn't how anyone
// writes them there.
func ShortenHome(path, home string) string {
	if home == "" || runtime.GOOS == "windows" {
		return path
	}
	if rest, ok := strings.CutPrefix(path, home); ok && (rest == "" || rest[0] == '/') {
		return "~" + rest
	}
	return path
}

// addonsSubdir is where AddOns is below path: Interface/AddOns in a client
// folder, AddOns in an Interface folder, or "" for any other folder.
func addonsSubdir(path string) string {
	switch {
	case strings.EqualFold(filepath.Base(path), "Interface"):
		return "AddOns"
	case flavor.IsClientFolder(filepath.Base(path)):
		return filepath.Join("Interface", "AddOns")
	}
	return ""
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// chooseFlavor picks the flavor: the explicit one if given; otherwise
// current, the profile's flavor, unless pathGiven, a new path to infer
// from; otherwise the one the path's client folder implies; otherwise
// current (a new path with no client folder). An explicit flavor must match
// a live client folder, but a test client folder's is only a default,
// which neither --flavor nor a stored flavor has to match: Blizzard has
// used _classic_beta_ for more than one game.
func chooseFlavor(explicit, path string, current flavor.Flavor, pathGiven bool) (flavor.Flavor, error) {
	l, ok := flavor.Locate(path)
	if explicit != "" {
		f, err := flavor.Parse(explicit)
		if err != nil {
			return "", &UserError{Msg: err.Error()}
		}
		if ok && l.Test == "" && f != l.Flavor {
			return "", &UserError{
				Msg:  fmt.Sprintf("--flavor %s doesn't match the path, which is in %s", f, l.Folder),
				Hint: fmt.Sprintf("leave out --flavor to use %s", l.Flavor),
			}
		}
		return f, nil
	}
	switch {
	case current != "" && (!pathGiven || ok && l.Test != ""):
		return current, nil
	case ok:
		return l.Flavor, nil
	case current != "":
		return current, nil
	}
	return "", &UserError{
		Msg:  fmt.Sprintf("can't tell the flavor from %s: it's not inside a client folder such as _retail_", path),
		Hint: "pass --flavor: one of " + strings.Join(flavor.Names(), ", "),
	}
}

// profileWithPath returns another profile already using path, or "".
func profileWithPath(c *config.Config, path, except string) string {
	for _, name := range c.ProfileNames() {
		if name != except && config.SamePath(c.Profiles[name].Path, path) {
			return name
		}
	}
	return ""
}

// Profiles lists every profile in name order.
func (a *App) Profiles(ctx context.Context) ([]ProfileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := a.config.Load()
	if err != nil {
		return nil, err
	}
	infos := make([]ProfileInfo, 0, len(c.Profiles))
	for _, name := range c.ProfileNames() {
		infos = append(infos, profileInfo(c, name))
	}
	return infos, nil
}

func profileInfo(c *config.Config, name string) ProfileInfo {
	p := c.Profiles[name]
	return ProfileInfo{
		Name:    name,
		Path:    p.Path,
		Flavor:  p.Flavor,
		Addons:  p.Addons,
		Current: c.CurrentProfile == name,
	}
}

// UseProfile makes name the current profile.
func (a *App) UseProfile(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := a.changeConfig(func(c *config.Config) error {
		if _, ok := c.Profiles[name]; !ok {
			return unknownProfile(name)
		}
		c.CurrentProfile = name
		return nil
	})
	return err
}

// CurrentProfile returns the current profile's name.
func (a *App) CurrentProfile(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	c, err := a.config.Load()
	if err != nil {
		return "", err
	}
	if c.CurrentProfile == "" {
		return "", noCurrentProfile(c)
	}
	return c.CurrentProfile, nil
}

// DeleteProfileResult says what DeleteProfile did.
type DeleteProfileResult struct {
	// WasCurrent is true when the deleted profile was current. No profile is
	// current afterwards; Knapsack doesn't guess which one should be.
	WasCurrent bool
}

// DeleteProfile removes a profile's state file, then the profile itself
// from config. It never touches addon files on disk.
//
// State goes first, while the config lock is held: if deleting it fails,
// config is left unchanged and the command can simply be retried. The other
// order could leave a stale state file that a new profile of the same name
// would inherit.
func (a *App) DeleteProfile(ctx context.Context, name string) (DeleteProfileResult, error) {
	if err := ctx.Err(); err != nil {
		return DeleteProfileResult{}, err
	}
	var res DeleteProfileResult
	err := a.withProfileLock(name, func() error {
		_, err := a.changeConfig(func(c *config.Config) error {
			if _, ok := c.Profiles[name]; !ok {
				return unknownProfile(name)
			}
			if err := a.state.Delete(name); err != nil {
				return err
			}
			delete(c.Profiles, name)
			if c.CurrentProfile == name {
				c.CurrentProfile = ""
				res.WasCurrent = true
			}
			return nil
		})
		return err
	})
	return res, err
}

// ViewConfig returns the config with API keys redacted, for `config view`.
func (a *App) ViewConfig(ctx context.Context) (*config.Config, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := a.config.Load()
	if err != nil {
		return nil, err
	}
	c.Sources = c.Sources.Redacted()
	return c, nil
}

func unknownProfile(name string) error {
	return &UserError{
		Msg:  fmt.Sprintf("profile %q doesn't exist", name),
		Hint: "list profiles with: knapsack profiles list",
	}
}

func noCurrentProfile(c *config.Config) error {
	if len(c.Profiles) == 0 {
		return &UserError{
			Msg:  "no profiles yet",
			Hint: "find the game with `knapsack profiles create --auto`, or name its folder: `knapsack profiles create --path " + Quoted(ExampleClientFolder()) + "`",
		}
	}
	return &UserError{
		Msg:  "no current profile",
		Hint: "choose one with: knapsack profiles use <name>",
	}
}
