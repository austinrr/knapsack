package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"slices"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

// ErrInvalidEdit is the cause of an edit that can't be saved as it is:
// TOML that doesn't parse, or a setting that isn't valid. The editor is
// opened again on it, with the error, as kubectl edit does.
var ErrInvalidEdit = errors.New("invalid edit")

// ErrEditConflict is the cause of an edit whose original changed while it
// was being edited, by another command: saving would undo that change.
var ErrEditConflict = errors.New("changed while being edited")

// invalidEdit wraps err, the problem with an edit, in ErrInvalidEdit.
func invalidEdit(err error) error {
	return &UserError{Msg: err.Error(), Err: fmt.Errorf("%w: %w", ErrInvalidEdit, err)}
}

func editConflict(what string) error {
	return &UserError{
		Msg:  what + " changed while you were editing it",
		Hint: "your edit wasn't saved; run the command again to edit what's there now",
		Err:  ErrEditConflict,
	}
}

// EditDir is where a copy being edited is kept, in Knapsack's cache
// folder, which it makes if needed: kept there, an edit that fails is
// still around to recover.
func (a *App) EditDir() (string, error) {
	if err := os.MkdirAll(a.paths.CacheDir, 0o700); err != nil {
		return "", err
	}
	return a.paths.CacheDir, nil
}

// ProfileForEdit returns profile name's settings as TOML, to edit with
// SaveProfileEdit: its path, flavor and addons.
func (a *App) ProfileForEdit(ctx context.Context, name string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := a.config.Load()
	if err != nil {
		return nil, err
	}
	p, ok := c.Profiles[name]
	if !ok {
		return nil, unknownProfile(name)
	}
	return config.MarshalProfile(p), nil
}

// ProfileEditResult says what SaveProfileEdit changed.
type ProfileEditResult struct {
	// Changed is false when the edit left the profile as it was.
	Changed bool
	// Added and Removed are the addons the edit added to the profile and
	// took out of it, which `update` and `update --prune` then install and
	// uninstall.
	Added, Removed []ref.Ref
}

// SaveProfileEdit saves edited as profile name's settings, in place of
// original, what ProfileForEdit gave. It fails with ErrInvalidEdit when
// edited isn't a valid profile, or uses another profile's folder, and with
// ErrEditConflict when the profile isn't original any more.
func (a *App) SaveProfileEdit(ctx context.Context, name string, original, edited []byte) (ProfileEditResult, error) {
	if err := ctx.Err(); err != nil {
		return ProfileEditResult{}, err
	}
	p, err := config.ParseProfile(name, edited)
	if err != nil {
		return ProfileEditResult{}, invalidEdit(err)
	}
	// The path and flavor follow the rules `profiles set` does: an
	// existing folder, completed to its AddOns folder, and a flavor that
	// doesn't contradict a live client folder's.
	if p.Path, err = addonsDir(p.Path, a.home); err != nil {
		return ProfileEditResult{}, invalidEdit(err)
	}
	if l, ok := flavor.Locate(p.Path); ok && l.Test == "" && p.Flavor != l.Flavor {
		return ProfileEditResult{}, invalidEdit(fmt.Errorf("flavor %s doesn't match the path, which is in %s (%s)", p.Flavor, l.Folder, l.Flavor))
	}
	var res ProfileEditResult
	err = a.withProfileLock(name, func() error {
		changed, err := a.changeConfig(func(c *config.Config) error {
			before, ok := c.Profiles[name]
			switch {
			case !ok:
				return unknownProfile(name)
			case !bytes.Equal(config.MarshalProfile(before), original):
				return editConflict(fmt.Sprintf("profile %q", name))
			}
			if other := profileWithPath(c, p.Path, name); other != "" {
				return invalidEdit(fmt.Errorf("profile %q already uses %s; each AddOns folder can belong to only one profile", other, p.Path))
			}
			for _, r := range p.Addons {
				if !slices.Contains(before.Addons, r) {
					res.Added = append(res.Added, r)
				}
			}
			for _, r := range before.Addons {
				if !slices.Contains(p.Addons, r) {
					res.Removed = append(res.Removed, r)
				}
			}
			c.Profiles[name] = p
			return nil
		})
		res.Changed = changed
		return err
	})
	return res, err
}

// ConfigForEdit returns config.toml as it is, comments and all, to edit
// with SaveConfigEdit, or an empty config when there's none yet. It holds
// any API keys, unredacted.
func (a *App) ConfigForEdit(ctx context.Context) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data, err := a.config.Read()
	if err != nil || data != nil {
		return data, err
	}
	return config.Marshal(config.New()), nil
}

// SaveConfigEdit saves edited as config.toml, as it is, in place of
// original, what ConfigForEdit gave. It fails with ErrInvalidEdit when
// edited isn't a valid config, and with ErrEditConflict when the file
// isn't original any more. It reports whether anything changed.
func (a *App) SaveConfigEdit(ctx context.Context, original, edited []byte) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if _, err := config.Parse(edited); err != nil {
		return false, invalidEdit(err)
	}
	l, err := acquire(a.paths.ConfigLock(), "config is in use by another knapsack process")
	if err != nil {
		return false, err
	}
	defer func() { _ = l.Release() }()
	now, err := a.config.Read()
	if err != nil {
		return false, err
	}
	if now == nil {
		now = config.Marshal(config.New())
	}
	if !bytes.Equal(now, original) {
		return false, editConflict("config.toml")
	}
	if bytes.Equal(edited, original) {
		return false, nil
	}
	return true, a.config.SaveRaw(edited)
}
