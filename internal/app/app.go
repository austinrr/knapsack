package app

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/lock"
	"github.com/austinrr/knapsack/internal/paths"
)

// Options are the App's dependencies. Frontends build them once at startup.
type Options struct {
	Paths  paths.Paths
	Config *config.Store
}

// App runs use cases. It's safe to reuse across commands: it loads fresh
// config at the start of each one and caches nothing.
type App struct {
	paths  paths.Paths
	config *config.Store
}

// New returns an App using opts.
func New(opts Options) *App {
	return &App{paths: opts.Paths, config: opts.Config}
}

// changeConfig runs change on freshly loaded config while holding the
// config lock, then saves it. Loading under the lock means two processes
// can't each save a stale copy and lose the other's change (DESIGN.md §5.5).
//
// If change leaves the config as it was, nothing is written, so a no-op
// command doesn't strip the file's comments. It reports whether it saved.
func (a *App) changeConfig(change func(*config.Config) error) (bool, error) {
	l, err := acquire(a.paths.ConfigLock(), "config is in use by another knapsack process")
	if err != nil {
		return false, err
	}
	defer func() { _ = l.Release() }()

	c, err := a.config.Load()
	if err != nil {
		return false, err
	}
	before := config.Marshal(c)
	if err := change(c); err != nil {
		return false, err
	}
	if bytes.Equal(before, config.Marshal(c)) {
		return false, nil
	}
	if err := a.config.Save(c); err != nil {
		return false, fmt.Errorf("save config: %w", err)
	}
	return true, nil
}

// withProfileLock runs fn holding the profile's lock, for anything that
// changes or deletes a profile. Lock order is always profile, then config
// (DESIGN.md §5.5), so a running update on the profile can't have it changed
// or deleted underneath it.
func (a *App) withProfileLock(profile string, fn func() error) error {
	l, err := acquire(a.paths.ProfileLock(profile),
		fmt.Sprintf("profile %q is in use by another knapsack process", profile))
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()
	return fn()
}

// acquire takes a lock, turning contention into a UserError with msg.
func acquire(path, msg string) (*lock.Lock, error) {
	l, err := lock.Acquire(path)
	if errors.Is(err, lock.ErrLocked) {
		return nil, &UserError{Msg: msg, Hint: "wait for it to finish, then try again", Err: err}
	}
	return l, err
}
