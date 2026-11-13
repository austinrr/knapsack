package config

import (
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

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
	// Path is the AddOns directory. It must be absolute, in Unix or Windows
	// form, and valid UTF-8.
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

// keyedSources is every source that takes an API key, in file order: its
// [sources.<name>] table, its environment variable and its field. Loading,
// saving, APIKey and the file mode all use this one list, so adding a source
// can't miss one of them.
var keyedSources = []keyedSource{
	{ref.CurseForge, CurseForgeKeyEnv, func(s *Sources) *Source { return &s.CurseForge }},
	{ref.Wago, WagoKeyEnv, func(s *Sources) *Source { return &s.Wago }},
}

type keyedSource struct {
	name  string
	env   string
	field func(*Sources) *Source
}

// APIKey returns the API key for a source. The environment variable wins
// over config when it's set. Sources without keys return "".
func (c *Config) APIKey(source string, getenv func(string) string) string {
	for _, ks := range keyedSources {
		if ks.name == source {
			if v := getenv(ks.env); v != "" {
				return v
			}
			return ks.field(&c.Sources).APIKey
		}
	}
	return ""
}

// hasSecrets reports whether the config holds an API key, in which case it's
// written readable only by the user.
func (c *Config) hasSecrets() bool {
	for _, ks := range keyedSources {
		if ks.field(&c.Sources).APIKey != "" {
			return true
		}
	}
	return false
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
	paths := map[string]string{} // normalized path -> first profile using it
	for _, name := range c.ProfileNames() {
		p := c.Profiles[name]
		errs = append(errs, p.validate(name)...)
		if p.Path == "" {
			continue
		}
		key := samePathKey(p.Path)
		if other, ok := paths[key]; ok {
			errs = append(errs, fmt.Errorf("profiles %q and %q have the same path %q; each AddOns folder can belong to only one profile", other, name, p.Path))
			continue
		}
		paths[key] = name
	}
	if c.CurrentProfile != "" {
		if _, ok := c.Profiles[c.CurrentProfile]; !ok {
			errs = append(errs, fmt.Errorf("current_profile %q is not a profile", c.CurrentProfile))
		}
	}
	for _, ks := range keyedSources {
		if !utf8.ValidString(ks.field(&c.Sources).APIKey) {
			errs = append(errs, fmt.Errorf("sources.%s.api_key is not valid UTF-8", ks.name))
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
	case !utf8.ValidString(p.Path):
		// TOML strings are UTF-8, so this path can't be saved faithfully.
		add(fmt.Errorf("path %q is not valid UTF-8", p.Path))
	case !isAbs(p.Path):
		add(fmt.Errorf("path %q is not absolute", p.Path))
	}
	if p.Flavor == "" {
		add(errors.New("flavor is empty"))
	} else if _, err := flavor.Parse(string(p.Flavor)); err != nil {
		add(err)
	}
	seen := make(map[ref.Ref]bool, len(p.Addons))
	for _, r := range p.Addons {
		// Refs must be exactly what Parse produces, or Save would write a
		// file that Load rejects or rewrites, and the duplicate check below
		// could miss two spellings of one addon.
		if parsed, err := ref.Parse(r.String()); err != nil || parsed != r {
			add(fmt.Errorf("addon %q is not a valid, normalized ref", r))
			continue
		}
		if seen[r] {
			add(fmt.Errorf("addon %q is listed twice", r))
		}
		seen[r] = true
	}
	return errs
}

// isAbs reports whether p is absolute in Unix ("/games/...") or Windows
// ("C:/...", "C:\...", "\\server\share") form, whatever OS is running. A
// config in a dotfiles repo may hold profiles for both, and it must still
// load on either; a profile from the other OS simply won't find its folder.
func isAbs(p string) bool {
	if filepath.IsAbs(p) || strings.HasPrefix(p, "/") || strings.HasPrefix(p, `\\`) {
		return true
	}
	return len(p) >= 3 && p[1] == ':' && (p[2] == '/' || p[2] == '\\') &&
		('a' <= p[0] && p[0] <= 'z' || 'A' <= p[0] && p[0] <= 'Z')
}

// samePathKey normalizes a path for spotting two profiles that point at one
// folder: separators unified, cleaned, and case folded, since Windows and
// macOS ignore case.
func samePathKey(p string) string {
	return strings.ToLower(path.Clean(strings.ReplaceAll(p, `\`, "/")))
}

// ProfileNames returns the profile names in sorted order.
func (c *Config) ProfileNames() []string {
	return slices.Sorted(maps.Keys(c.Profiles))
}
