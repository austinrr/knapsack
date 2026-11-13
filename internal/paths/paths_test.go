package paths_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/austinrr/knapsack/internal/paths"
)

// env returns a getenv func that sees only vars.
func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestResolveHome(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	got, err := paths.Resolve(env(map[string]string{paths.HomeEnv: home}))
	if err != nil {
		t.Fatal(err)
	}
	want := paths.Paths{
		ConfigFile: filepath.Join(home, "config", "config.toml"),
		StateDir:   filepath.Join(home, "config", "state"),
		CacheDir:   filepath.Join(home, "cache"),
	}
	if got != want {
		t.Errorf("Resolve() =\n  %+v\nwant\n  %+v", got, want)
	}
}

// A relative KNAPSACK_HOME is made absolute, so paths don't change meaning if
// the working directory does.
func TestResolveRelativeHome(t *testing.T) {
	t.Parallel()
	got, err := paths.Resolve(env(map[string]string{paths.HomeEnv: "relhome"}))
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wd, "relhome", "cache"); got.CacheDir != want {
		t.Errorf("CacheDir = %q, want %q", got.CacheDir, want)
	}
	if !filepath.IsAbs(got.ConfigFile) {
		t.Errorf("ConfigFile %q is not absolute", got.ConfigFile)
	}
}

// Without KNAPSACK_HOME, the OS's per-user directories are used.
func TestResolveDefault(t *testing.T) {
	t.Parallel()
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skipf("no user config dir on this machine: %v", err)
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Skipf("no user cache dir on this machine: %v", err)
	}
	got, err := paths.Resolve(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	want := paths.Paths{
		ConfigFile: filepath.Join(configDir, "knapsack", "config.toml"),
		StateDir:   filepath.Join(configDir, "knapsack", "state"),
		CacheDir:   filepath.Join(cacheDir, "knapsack"),
	}
	if got != want {
		t.Errorf("Resolve() =\n  %+v\nwant\n  %+v", got, want)
	}
}

func TestWithConfigFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p, err := paths.Resolve(env(map[string]string{paths.HomeEnv: home}))
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "other.toml")
	got, err := p.WithConfigFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if got.ConfigFile != file {
		t.Errorf("ConfigFile = %q, want %q", got.ConfigFile, file)
	}
	// Only the config file moves.
	if got.StateDir != p.StateDir || got.CacheDir != p.CacheDir {
		t.Errorf("WithConfigFile moved state or cache: %+v", got)
	}
	// It returns a copy; the original is unchanged.
	if p.ConfigFile == file {
		t.Error("WithConfigFile modified the receiver")
	}
	if got.ConfigLock() != file+".lock" {
		t.Errorf("ConfigLock = %q, want it next to the config file", got.ConfigLock())
	}

	if _, err := p.WithConfigFile(""); err == nil {
		t.Error("WithConfigFile(\"\") succeeded, want error")
	}
}

func TestProfileFiles(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	p, err := paths.Resolve(env(map[string]string{paths.HomeEnv: home}))
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(home, "config", "state")
	if got, want := p.StateFile("forever"), filepath.Join(state, "forever.toml"); got != want {
		t.Errorf("StateFile = %q, want %q", got, want)
	}
	if got, want := p.ProfileLock("forever"), filepath.Join(state, "forever.lock"); got != want {
		t.Errorf("ProfileLock = %q, want %q", got, want)
	}
	// A profile named "config" doesn't share the config lock.
	if p.ProfileLock("config") == p.ConfigLock() {
		t.Errorf("profile %q and config share lock %q", "config", p.ConfigLock())
	}
}
