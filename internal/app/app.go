package app

import (
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
func (a *App) changeConfig(change func(*config.Config) error) error {
	l, err := lock.Acquire(a.paths.ConfigLock())
	if errors.Is(err, lock.ErrLocked) {
		return &UserError{
			Msg:  "config is in use by another knapsack process",
			Hint: "wait for it to finish, then try again",
			Err:  err,
		}
	}
	if err != nil {
		return err
	}
	defer func() { _ = l.Release() }()

	c, err := a.config.Load()
	if err != nil {
		return err
	}
	if err := change(c); err != nil {
		return err
	}
	if err := a.config.Save(c); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
