package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
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
	Sources        map[string]fileSource  `toml:"sources"`
}

type fileProfile struct {
	Path   string   `toml:"path"`
	Flavor string   `toml:"flavor"`
	Addons []string `toml:"addons"`
}

type fileSource struct {
	APIKey string `toml:"api_key"`
	API    string `toml:"api"`
}

// Load reads and validates the config file. A missing file is not an error:
// it returns an empty Config, as on first run. Refs written by hand in any
// accepted form are normalized, so the next Save writes them canonically.
func (s *Store) Load() (*Config, error) {
	data, err := s.Read()
	if err != nil {
		return nil, err
	}
	if data == nil {
		return New(), nil
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.path, err)
	}
	return c, nil
}

// Read returns the config file as it is, or nil when there's none yet.
func (s *Store) Read() ([]byte, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return data, nil
}

// Parse reads and validates config as TOML, as Load does the file.
func Parse(data []byte) (*Config, error) {
	var fc fileConfig
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&fc)
	if err != nil {
		return nil, err
	}
	// Unknown keys are almost always typos ("flavour", "api-key"), which
	// would otherwise be silently ignored.
	if unknown := unknownKeys(md.Undecoded()); len(unknown) > 0 {
		return nil, fmt.Errorf("unknown keys: %s", strings.Join(unknown, ", "))
	}
	// Report bad refs and every other problem together, so one Load shows
	// the user everything to fix.
	c, refErrs := fromFile(&fc)
	if err := errors.Join(refErrs, c.Validate()); err != nil {
		return nil, err
	}
	return c, nil
}

// ParseProfile reads one profile's settings as TOML, as MarshalProfile
// writes them, for profile name: its keys and refs. The rest, such as
// whether its path is absolute, is left for Validate on the config it's
// put in, once the caller has made the path what it should be.
func ParseProfile(name string, data []byte) (Profile, error) {
	var fp fileProfile
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&fp)
	if err != nil {
		return Profile{}, err
	}
	if unknown := unknownKeys(md.Undecoded()); len(unknown) > 0 {
		return Profile{}, fmt.Errorf("unknown keys: %s", strings.Join(unknown, ", "))
	}
	p, errs := fp.profile(name)
	return p, errors.Join(errs...)
}

// SaveRaw writes data, which Parse made c from, as the config file as it
// is, comments and layout kept, as `knapsack config edit` saves it.
func (s *Store) SaveRaw(data []byte, c *Config) error {
	return s.write(data, c.hasSecrets())
}

// unknownKeys names each undecoded key once. An unknown table such as
// [sources.github] is listed by itself, not along with every key inside it.
func unknownKeys(keys []toml.Key) []string {
	var names []string
	for _, k := range keys {
		names = append(names, k.String())
	}
	var out []string
	for _, name := range names {
		parentListed := slices.ContainsFunc(names, func(other string) bool {
			return strings.HasPrefix(name, other+".")
		})
		if !parentListed {
			out = append(out, name)
		}
	}
	return out
}

// fromFile converts the decoded TOML into a Config, parsing refs and source
// tables. It always returns a Config, holding everything that parsed, so
// Validate can still check the rest; flavors and paths are left to Validate.
func fromFile(fc *fileConfig) (*Config, error) {
	c := New()
	c.CurrentProfile = fc.CurrentProfile

	var errs []error
	for _, name := range slices.Sorted(maps.Keys(fc.Sources)) {
		i := slices.IndexFunc(keyedSources, func(ks keyedSource) bool { return ks.name == name })
		if i < 0 {
			var known []string
			for _, ks := range keyedSources {
				known = append(known, ks.name)
			}
			errs = append(errs, fmt.Errorf("unknown source table [sources.%s] (sources with settings: %s)", name, strings.Join(known, ", ")))
			continue
		}
		f := keyedSources[i].field(&c.Sources)
		f.APIKey, f.API = fc.Sources[name].APIKey, API(fc.Sources[name].API)
	}

	for _, name := range slices.Sorted(maps.Keys(fc.Profiles)) {
		p, refErrs := fc.Profiles[name].profile(name)
		errs = append(errs, refErrs...)
		c.Profiles[name] = p
	}
	return c, errors.Join(errs...)
}

// profile converts the decoded TOML of profile name, parsing its refs.
// Flavors and paths are left to Validate.
func (fp fileProfile) profile(name string) (Profile, []error) {
	p := Profile{Path: fp.Path, Flavor: flavor.Flavor(fp.Flavor)}
	var errs []error
	for _, s := range fp.Addons {
		r, err := ref.Parse(s)
		if err != nil {
			errs = append(errs, fmt.Errorf("profile %q: %w", name, err))
			continue
		}
		p.Addons = append(p.Addons, r)
	}
	return p, errs
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
	return s.write(buf.Bytes(), c.hasSecrets())
}

// write atomically writes data as the config file, readable only by the
// user when it holds secrets, creating the config directory if needed.
func (s *Store) write(data []byte, secrets bool) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	perm := fs.FileMode(0o644)
	if secrets {
		perm = 0o600
	}
	if err := atomicfile.Write(s.path, data, perm); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}
