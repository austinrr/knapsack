package state

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/austinrr/knapsack/internal/atomicfile"
	"github.com/austinrr/knapsack/internal/ref"
)

// Store reads and writes state files, one per profile. It doesn't lock:
// callers hold the profile's lock (DESIGN.md §5.5).
type Store struct {
	pathFor func(profile string) string
}

// NewStore returns a Store that keeps each profile's state at
// pathFor(profile). Pass paths.Paths.StateFile, so the layout of the
// config directory stays in package paths.
func NewStore(pathFor func(profile string) string) *Store {
	return &Store{pathFor: pathFor}
}

// fileState mirrors the TOML layout, for decoding; encode.go writes it. Refs are strings here and parsed after
// decoding, so a bad one gets an error that names it.
type fileState struct {
	Version int                  `toml:"version"`
	Addons  map[string]fileAddon `toml:"addons"`
}

type fileAddon struct {
	Version     string    `toml:"version"`
	SHA256      string    `toml:"sha256"`
	InstalledAt time.Time `toml:"installed_at"`
	Folders     []string  `toml:"folders"`
}

// Load reads and validates profile's state. A missing file isn't an error:
// it returns an empty State, as for a profile with nothing installed yet.
func (s *Store) Load(profile string) (*State, error) {
	path := s.pathFor(profile)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}

	// Check the schema version first, so a file from a newer Knapsack gets
	// a clear error instead of a list of keys this build doesn't know.
	var v struct {
		Version int `toml:"version"`
	}
	if _, err := toml.Decode(string(data), &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	switch {
	case v.Version > SchemaVersion:
		return nil, fmt.Errorf("%s: schema version %d is newer than this knapsack supports (%d); upgrade knapsack",
			path, v.Version, SchemaVersion)
	case v.Version == 0:
		return nil, fmt.Errorf("%s: no schema version", path)
	case v.Version != SchemaVersion:
		return nil, fmt.Errorf("%s: schema version %d isn't valid (want %d)", path, v.Version, SchemaVersion)
	}

	var file fileState
	md, err := toml.Decode(string(data), &file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		names := make([]string, len(undecoded))
		for i, k := range undecoded {
			names[i] = k.String()
		}
		return nil, fmt.Errorf("%s: unknown keys: %s", path, strings.Join(names, ", "))
	}

	st, refErrs := fromFile(&file)
	if err := errors.Join(refErrs, st.Validate()); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return st, nil
}

// fromFile converts decoded TOML into a State. Refs must already be in
// normal form, since Knapsack wrote them: a ref that would normalize to
// something else means the file was edited or corrupted.
func fromFile(file *fileState) (*State, error) {
	st := New()
	var errs []error
	for _, key := range slices.Sorted(maps.Keys(file.Addons)) {
		r, err := ref.Parse(key)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if r.String() != key {
			errs = append(errs, fmt.Errorf("addon %q: ref isn't in normal form (want %q)", key, r))
			continue
		}
		// The two structs have the same fields, so one converts to the
		// other; struct tags don't count.
		st.Addons[r] = Addon(file.Addons[key])
	}
	return st, errors.Join(errs...)
}

// Save validates st and atomically writes it as profile's state, creating
// the state directory if needed.
func (s *Store) Save(profile string, st *State) error {
	if err := st.Validate(); err != nil {
		return fmt.Errorf("invalid state: %w", err)
	}
	data := Marshal(st)
	path := s.pathFor(profile)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	if err := atomicfile.Write(path, data, 0o644); err != nil {
		return fmt.Errorf("save state: %w", err)
	}
	return nil
}

// Rename moves from's state to to, for renaming a profile. It's a no-op if
// from has no state, and an error if to already has some, so one profile's
// state never replaces another's.
func (s *Store) Rename(from, to string) error {
	src, dst := s.pathFor(from), s.pathFor(to)
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("rename state: %s already exists", dst)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("rename state: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("rename state: %w", err)
	}
	return nil
}

// Delete removes profile's state. A profile with no state file is fine.
func (s *Store) Delete(profile string) error {
	err := os.Remove(s.pathFor(profile))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("delete state: %w", err)
	}
	return nil
}
