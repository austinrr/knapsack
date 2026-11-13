package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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
	if err := config.ValidateProfileName(req.Name); err != nil {
		return SetProfileResult{}, &UserError{Msg: err.Error()}
	}

	var res SetProfileResult
	err := a.withProfileLock(req.Name, func() error {
		changed, err := a.changeConfig(func(c *config.Config) error {
			p, exists := c.Profiles[req.Name]
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
			f, err := chooseFlavor(req.Flavor, p.Path, p.Flavor)
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

// addonsDir makes path absolute and checks that it's a directory, which
// catches typos before they're saved.
func addonsDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path: %w", err)
	}
	info, err := os.Stat(abs)
	switch {
	case errors.Is(err, fs.ErrNotExist) || err == nil && !info.IsDir():
		return "", &UserError{
			Msg:  fmt.Sprintf("%s is not a directory", abs),
			Hint: "pass --path with the game's Interface/AddOns folder",
			Err:  err,
		}
	case err != nil:
		// Permission denied and the like: the folder may well exist, so
		// say what actually went wrong.
		return "", fmt.Errorf("check --path: %w", err)
	}
	return abs, nil
}

// chooseFlavor picks the flavor: the explicit one if given, otherwise the
// one the path's client folder implies, otherwise current (when updating a
// profile whose new path has no client folder).
func chooseFlavor(explicit, path string, current flavor.Flavor) (flavor.Flavor, error) {
	inferred, ok := flavor.FromPath(path)
	if explicit != "" {
		f, err := flavor.Parse(explicit)
		if err != nil {
			return "", &UserError{Msg: err.Error()}
		}
		if ok && f != inferred {
			c, _ := inferred.Client()
			return "", &UserError{
				Msg:  fmt.Sprintf("--flavor %s doesn't match the path, which is in %s", f, c.Folder),
				Hint: fmt.Sprintf("leave out --flavor to use %s", inferred),
			}
		}
		return f, nil
	}
	if ok {
		return inferred, nil
	}
	if current != "" {
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

// DeleteProfile removes a profile from config. It never touches addon files
// on disk.
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
			Hint: "create one with: knapsack config set-profile <name> --path <AddOns folder>",
		}
	}
	return &UserError{
		Msg:  "no current profile",
		Hint: "choose one with: knapsack config use-profile <name>",
	}
}
