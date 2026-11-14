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

// SetProfileRequest is what `config set-profile` asks for. Empty fields are
// left as they are on an existing profile.
type SetProfileRequest struct {
	// Name is the profile's name. "" names it after Path (DESIGN.md §8.4):
	// the profile that already uses that folder, else its flavor.
	Name   string
	Path   string // relative paths are resolved against the working directory
	Flavor string // "" infers it from the client folder in the path
}

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
					Hint: "pass --path with the game's Interface/AddOns folder",
				}
			}
			if req.Path != "" {
				path, err := addonsDir(req.Path)
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

// nameForPath names the profile for a set-profile without a name: the
// profile that already uses req.Path, so running it again changes nothing,
// or else the flavor, which is what most people have one profile per.
// It also returns req.Path made absolute.
func (a *App) nameForPath(req SetProfileRequest) (name, path string, err error) {
	if req.Path == "" {
		return "", "", &UserError{
			Msg:  "set-profile needs --path, or the name of a profile to change",
			Hint: "pass --path with the game's Interface/AddOns folder",
		}
	}
	if path, err = addonsDir(req.Path); err != nil {
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
		Hint: fmt.Sprintf("give this one its own name with `knapsack config set-profile <name> --path …`, or move %q here by naming it: `knapsack config set-profile %s --path …`",
			name, name),
	}
}

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
func addonsDir(path string) (string, error) {
	// Shells don't expand ~ inside quotes, which a path with spaces needs,
	// so a leading ~ is expanded here.
	if rest, ok := strings.CutPrefix(path, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == filepath.Separator) {
		if home, err := os.UserHomeDir(); err == nil {
			path = home + rest
		}
	}
	abs, err := filepath.Abs(path)
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
	for _, sub := range addonsSubdirs(abs) {
		if isDir(filepath.Join(abs, sub)) {
			return filepath.Join(abs, sub), nil
		}
	}
	if clients := clientFolders(abs); len(clients) > 0 {
		return "", &UserError{
			Msg:  fmt.Sprintf("%s is the game folder, with %s in it", abs, strings.Join(clients, ", ")),
			Hint: "pass one client folder: --path " + Quoted(filepath.Join(abs, clients[0])),
		}
	}
	return abs, nil
}

// addonsSubdirs are where AddOns may be below path: Interface/AddOns in a
// client folder, AddOns in an Interface folder.
func addonsSubdirs(path string) []string {
	switch {
	case strings.EqualFold(filepath.Base(path), "Interface"):
		return []string{"AddOns"}
	case flavor.IsClientFolder(filepath.Base(path)):
		return []string{filepath.Join("Interface", "AddOns")}
	}
	return nil
}

// clientFolders lists the client folders in dir, sorted, when dir is a
// World of Warcraft folder.
func clientFolders(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && flavor.IsClientFolder(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
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
		Hint: "list profiles with: knapsack config get-profiles",
	}
}

func noCurrentProfile(c *config.Config) error {
	if len(c.Profiles) == 0 {
		return &UserError{
			Msg:  "no profiles yet",
			Hint: "create one with: knapsack config set-profile --path " + Quoted(ExampleClientFolder()),
		}
	}
	return &UserError{
		Msg:  "no current profile",
		Hint: "choose one with: knapsack config use-profile <name>",
	}
}
