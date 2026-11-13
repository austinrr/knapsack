// Package paths resolves where Knapsack keeps its config, state and cache.
// It's the only package that calls os.UserConfigDir or os.UserCacheDir.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// HomeEnv overrides both the config and cache locations when set.
const HomeEnv = "KNAPSACK_HOME"

// Paths holds every location Knapsack reads or writes.
type Paths struct {
	// ConfigFile is config.toml. --config replaces only this.
	ConfigFile string
	// StateDir holds one state file and one lock file per profile.
	StateDir string
	// CacheDir holds downloads in progress. It's always safe to delete.
	CacheDir string
}

// Resolve works out the default paths. getenv is os.Getenv in production
// and a fake in tests, so nothing here reads global state directly.
//
// With KNAPSACK_HOME set, everything lives under it:
// $KNAPSACK_HOME/config and $KNAPSACK_HOME/cache. Otherwise Knapsack uses
// the per-user config and cache directories, e.g. ~/.config/knapsack and
// ~/.cache/knapsack on Linux.
func Resolve(getenv func(string) string) (Paths, error) {
	if home := getenv(HomeEnv); home != "" {
		abs, err := filepath.Abs(home)
		if err != nil {
			return Paths{}, fmt.Errorf("resolve %s: %w", HomeEnv, err)
		}
		return fromDirs(filepath.Join(abs, "config"), filepath.Join(abs, "cache")), nil
	}

	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find config directory (set %s to choose one): %w", HomeEnv, err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("find cache directory (set %s to choose one): %w", HomeEnv, err)
	}
	return fromDirs(filepath.Join(configDir, "knapsack"), filepath.Join(cacheDir, "knapsack")), nil
}

func fromDirs(configDir, cacheDir string) Paths {
	return Paths{
		ConfigFile: filepath.Join(configDir, "config.toml"),
		StateDir:   filepath.Join(configDir, "state"),
		CacheDir:   cacheDir,
	}
}

// WithConfigFile returns a copy of p that reads config from file, for
// --config. State and cache stay where they were.
func (p Paths) WithConfigFile(file string) (Paths, error) {
	if file == "" {
		return Paths{}, errors.New("config file path is empty")
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config file: %w", err)
	}
	p.ConfigFile = abs
	return p, nil
}

// ConfigLock is held by commands that change config without touching a
// profile. It sits next to the config file, so --config moves it too, and it
// can't collide with a profile's lock in StateDir.
func (p Paths) ConfigLock() string {
	return p.ConfigFile + ".lock"
}

// StateFile is the state file for a profile. profile must already be a valid
// profile name (config validates them), so it can't escape StateDir.
func (p Paths) StateFile(profile string) string {
	return filepath.Join(p.StateDir, profile+".toml")
}

// ProfileLock is held for the whole of any command that reads or writes a
// profile. profile must be a valid profile name.
func (p Paths) ProfileLock(profile string) string {
	return filepath.Join(p.StateDir, profile+".lock")
}
