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

// Resolve works out the default paths. getenv supplies KNAPSACK_HOME: it's
// os.Getenv in production and a fake in tests. Without KNAPSACK_HOME, the
// defaults come from os.UserConfigDir and os.UserCacheDir, which read the
// real environment (XDG_CONFIG_HOME, HOME, AppData), so tests always set
// KNAPSACK_HOME.
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

// ConfigLock is held by every command that writes config.toml. It lives in
// Knapsack's own config directory, beside StateDir rather than in it, so it
// can't collide with a profile's lock. It stays there with --config, so
// Knapsack never creates files next to a config the user put elsewhere.
func (p Paths) ConfigLock() string {
	return filepath.Join(filepath.Dir(p.StateDir), "config.lock")
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
