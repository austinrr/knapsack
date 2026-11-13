package config

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

// Config is the contents of config.toml.
type Config struct {
	// CurrentProfile is used when no profile is given. Empty means none.
	CurrentProfile string
	// Profiles maps each profile name to its settings.
	Profiles map[string]Profile
	// Sources holds per-source settings.
	Sources Sources
}

// Profile is one AddOns folder and the addons the user wants in it.
type Profile struct {
	// Path is the AddOns directory. It must be absolute.
	Path string
	// Flavor is the client the AddOns directory belongs to.
	Flavor flavor.Flavor
	// Addons are normalized refs, in the order they were added.
	Addons []ref.Ref
}

// Sources holds per-source settings. GitHub has none: its token comes from
// the environment and is never stored.
type Sources struct {
	CurseForge Source
	Wago       Source
}

// Source holds one source's settings.
type Source struct {
	// APIKey as written in config. Use Config.APIKey, which applies
	// environment overrides.
	APIKey string
}

// Environment variables that override the API keys in config, so a config
// kept in a dotfiles repo can leave the keys empty.
const (
	CurseForgeKeyEnv = "KNAPSACK_CURSEFORGE_API_KEY"
	WagoKeyEnv       = "KNAPSACK_WAGO_API_KEY"
)

// New returns an empty Config, as used on first run.
func New() *Config {
	return &Config{Profiles: map[string]Profile{}}
}

// APIKey returns the API key for a source. The environment variable wins
// over config when it's set. Sources without keys return "".
func (c *Config) APIKey(source string, getenv func(string) string) string {
	var env, key string
	switch source {
	case ref.CurseForge:
		env, key = CurseForgeKeyEnv, c.Sources.CurseForge.APIKey
	case ref.Wago:
		env, key = WagoKeyEnv, c.Sources.Wago.APIKey
	default:
		return ""
	}
	if v := getenv(env); v != "" {
		return v
	}
	return key
}

// hasSecrets reports whether the config holds an API key, in which case it's
// written readable only by the user.
func (c *Config) hasSecrets() bool {
	return c.Sources.CurseForge.APIKey != "" || c.Sources.Wago.APIKey != ""
}

// profileName is the rule for profile names. Each name becomes a state and a
// lock file name, so it's limited to characters that are safe in file names
// everywhere, and lowercase so "Forever" and "forever" can't share a file on
// a case-insensitive filesystem. It also keeps every name a bare TOML key.
var profileName = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// ValidateProfileName reports whether name can be used as a profile name.
func ValidateProfileName(name string) error {
	if !profileName.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: use 1-64 lowercase letters, digits, '-' or '_'", name)
	}
	return nil
}

// Validate checks the whole config and returns every problem it finds,
// joined one per line, in a stable order.
func (c *Config) Validate() error {
	var errs []error
	for _, name := range c.ProfileNames() {
		errs = append(errs, c.Profiles[name].validate(name)...)
	}
	if c.CurrentProfile != "" {
		if _, ok := c.Profiles[c.CurrentProfile]; !ok {
			errs = append(errs, fmt.Errorf("current_profile %q is not a profile", c.CurrentProfile))
		}
	}
	return errors.Join(errs...)
}

// validate returns the profile's problems, each prefixed with its name so
// every line of a joined error says where it came from.
func (p Profile) validate(name string) []error {
	var errs []error
	add := func(err error) {
		errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
	}
	if err := ValidateProfileName(name); err != nil {
		errs = append(errs, err)
	}
	switch {
	case p.Path == "":
		add(errors.New("path is empty"))
	case !filepath.IsAbs(p.Path):
		add(fmt.Errorf("path %q is not absolute", p.Path))
	}
	if p.Flavor == "" {
		add(errors.New("flavor is empty"))
	} else if _, err := flavor.Parse(string(p.Flavor)); err != nil {
		add(err)
	}
	seen := make(map[ref.Ref]bool, len(p.Addons))
	for _, r := range p.Addons {
		if seen[r] {
			add(fmt.Errorf("addon %q is listed twice", r))
		}
		seen[r] = true
	}
	return errs
}

// ProfileNames returns the profile names in sorted order.
func (c *Config) ProfileNames() []string {
	names := make([]string, 0, len(c.Profiles))
	for name := range c.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
