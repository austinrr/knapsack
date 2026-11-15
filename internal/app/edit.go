package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
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

// EditDir is where a copy being edited is kept: next to config.toml,
// which it may be a copy of, API keys and all, so it's no less private,
// and kept when an edit fails, so nothing typed is lost.
func (a *App) EditDir() (string, error) {
	dir := filepath.Dir(a.config.Path())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
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
			if err := a.checkEditedProfile(&p, before); err != nil {
				return invalidEdit(err)
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
			if err := c.Validate(); err != nil {
				return invalidEdit(err)
			}
			return nil
		})
		res.Changed = changed
		return err
	})
	return res, err
}

// checkEditedProfile checks p's path and flavor as `profiles set` would
// have, but only what the edit changed, so a profile whose folder is
// missing for now, as one for another OS in a shared config is, can still
// have its addons edited. A changed path must be an existing folder, and
// is made absolute and completed to its AddOns folder, ~ expanded; a
// changed path or flavor mustn't contradict a live client folder.
func (a *App) checkEditedProfile(p *config.Profile, before config.Profile) error {
	if p.Path != before.Path {
		path, err := addonsDir(p.Path, a.home)
		if err != nil {
			return err
		}
		p.Path = path
	}
	if p.Path == before.Path && p.Flavor == before.Flavor {
		return nil
	}
	if l, ok := flavor.Locate(p.Path); ok && l.Test == "" && p.Flavor != l.Flavor {
		return fmt.Errorf("flavor %s doesn't match the path, which is in %s (%s)", p.Flavor, l.Folder, l.Flavor)
	}
	return nil
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

// ConfigEditResult says what SaveConfigEdit changed.
type ConfigEditResult struct {
	// Changed is false when the edit left config.toml as it was.
	Changed bool
	// Deleted are the profiles the edit took out, whose records of what
	// they installed are deleted too, as `profiles delete` deletes them.
	// Renamed maps each profile renamed, the same folder under a new
	// name, to its new name; its record moves with it.
	Deleted []string
	Renamed map[string]string
}

// SaveConfigEdit saves edited as config.toml, as it is, in place of
// original, what ConfigForEdit gave. It fails with ErrInvalidEdit when
// edited isn't a valid config, and with ErrEditConflict when the file
// isn't original any more. It holds every profile's lock, old and new, as
// any change to a profile does, so none is edited under a running update.
func (a *App) SaveConfigEdit(ctx context.Context, original, edited []byte) (ConfigEditResult, error) {
	if err := ctx.Err(); err != nil {
		return ConfigEditResult{}, err
	}
	after, err := config.Parse(edited)
	if err != nil {
		return ConfigEditResult{}, invalidEdit(err)
	}
	before, err := config.Parse(original)
	if err != nil {
		before = config.New() // a broken file is being fixed
	}
	names := slices.Sorted(maps.Keys(before.Profiles))
	for name := range after.Profiles {
		if !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	var res ConfigEditResult
	err = a.withProfileLocks(names, func() error {
		l, err := acquire(a.paths.ConfigLock(), "config is in use by another knapsack process")
		if err != nil {
			return err
		}
		defer func() { _ = l.Release() }()
		now, err := a.config.Read()
		if err != nil {
			return err
		}
		if now == nil {
			now = config.Marshal(config.New())
		}
		if !bytes.Equal(now, original) {
			return editConflict("config.toml")
		}
		if bytes.Equal(edited, original) {
			return nil
		}
		if err := a.config.SaveRaw(edited, after); err != nil {
			return err
		}
		res.Changed = true
		return a.followProfiles(before, after, &res)
	})
	return res, err
}

// followProfiles moves or deletes the records of what profiles installed,
// for those an edit renamed or took out of config, as `profiles delete`
// would: a profile whose folder is now another's name is renamed.
func (a *App) followProfiles(before, after *config.Config, res *ConfigEditResult) error {
	for _, name := range before.ProfileNames() {
		if _, kept := after.Profiles[name]; kept {
			continue
		}
		renamed := profileWithPath(after, before.Profiles[name].Path, "")
		if _, existed := before.Profiles[renamed]; renamed != "" && !existed {
			st, err := a.state.Load(name)
			if err != nil {
				return err
			}
			if err := a.state.Save(renamed, st); err != nil {
				return err
			}
			if res.Renamed == nil {
				res.Renamed = map[string]string{}
			}
			res.Renamed[name] = renamed
		} else {
			res.Deleted = append(res.Deleted, name)
		}
		if err := a.state.Delete(name); err != nil {
			return err
		}
	}
	return nil
}

// withProfileLocks runs fn holding each of profiles' locks, taken in the
// order given, which callers sort, so two never wait on each other.
func (a *App) withProfileLocks(profiles []string, fn func() error) error {
	if len(profiles) == 0 {
		return fn()
	}
	return a.withProfileLock(profiles[0], func() error {
		return a.withProfileLocks(profiles[1:], fn)
	})
}
