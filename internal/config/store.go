package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/austinrr/knapsack/internal/atomicfile"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

// Store reads and writes one config file. It doesn't lock: callers hold
// config.lock while they load, change and save (DESIGN.md §5.5).
type Store struct {
	path string
}

// NewStore returns a Store for the config file at path.
func NewStore(path string) *Store {
	return &Store{path: path}
}

// Path is the config file this Store reads and writes.
func (s *Store) Path() string {
	return s.path
}

// fileConfig mirrors the TOML layout. Values are plain strings here and are
// parsed into typed values after decoding, so a bad ref or flavor gets an
// error that names its profile.
type fileConfig struct {
	CurrentProfile string                 `toml:"current_profile"`
	Profiles       map[string]fileProfile `toml:"profiles"`
	Sources        struct {
		CurseForge fileSource `toml:"curseforge"`
		Wago       fileSource `toml:"wago"`
	} `toml:"sources"`
}

type fileProfile struct {
	Path   string   `toml:"path"`
	Flavor string   `toml:"flavor"`
	Addons []string `toml:"addons"`
}

type fileSource struct {
	APIKey string `toml:"api_key"`
}

// Load reads and validates the config file. A missing file is not an error:
// it returns an empty Config, as on first run. Refs written by hand in any
// accepted form are normalized, so the next Save writes them canonically.
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var fc fileConfig
	md, err := toml.Decode(string(data), &fc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	// Unknown keys are almost always typos ("flavour", "api-key"), which
	// would otherwise be silently ignored.
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", s.path, strings.Join(keys, ", "))
	}

	c, err := fromFile(&fc)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return c, nil
}

// fromFile converts the decoded TOML into a Config, parsing refs. Flavors
// are checked by Validate.
func fromFile(fc *fileConfig) (*Config, error) {
	c := New()
	c.CurrentProfile = fc.CurrentProfile
	c.Sources.CurseForge.APIKey = fc.Sources.CurseForge.APIKey
	c.Sources.Wago.APIKey = fc.Sources.Wago.APIKey

	var errs []error
	names := make([]string, 0, len(fc.Profiles))
	for name := range fc.Profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		fp := fc.Profiles[name]
		p := Profile{Path: fp.Path, Flavor: flavor.Flavor(fp.Flavor)}
		for _, s := range fp.Addons {
			r, err := ref.Parse(s)
			if err != nil {
				errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
				continue
			}
			p.Addons = append(p.Addons, r)
		}
		c.Profiles[name] = p
	}
	return c, errors.Join(errs...)
}

// Save validates c and atomically writes it. The file is written readable
// only by the user (0600) when it holds an API key. Save creates the config
// directory if needed.
func (s *Store) Save(c *Config) error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("invalid config: %w", err)
	}
	var buf bytes.Buffer
	encode(&buf, c)

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	perm := fs.FileMode(0o644)
	if c.hasSecrets() {
		perm = 0o600
	}
	if err := atomicfile.Write(s.path, buf.Bytes(), perm); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
