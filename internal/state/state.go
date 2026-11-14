package state

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/austinrr/knapsack/internal/ref"
)

// SchemaVersion is the state file format this build reads and writes.
const SchemaVersion = 1

// State is what is installed in one profile.
type State struct {
	// Addons maps each installed addon's ref to what is installed for it.
	Addons map[ref.Ref]Addon
}

// Addon is one installed release.
type Addon struct {
	// Version is the release's label, for display only. Labels follow no
	// common scheme, so they're never compared.
	Version string
	// SHA256 is the hex digest of the release archive. It's the release's
	// identity: an update is available when the latest release's digest
	// differs.
	SHA256 string
	// InstalledAt is when the release was installed. It's saved in UTC to
	// the second.
	InstalledAt time.Time
	// Folders are the AddOns folders the addon owns, as plain names
	// relative to the profile's AddOns path.
	Folders []string
}

// New returns an empty State.
func New() *State {
	return &State{Addons: map[ref.Ref]Addon{}}
}

// Refs returns the installed addons' refs, sorted by their string form.
func (s *State) Refs() []ref.Ref {
	return slices.SortedFunc(maps.Keys(s.Addons), func(a, b ref.Ref) int {
		return strings.Compare(a.String(), b.String())
	})
}

// Owner returns the addon that owns folder, matched without regard to
// case as on Windows and macOS, or false if none does.
func (s *State) Owner(folder string) (ref.Ref, bool) {
	for _, r := range s.Refs() {
		if slices.ContainsFunc(s.Addons[r].Folders, func(f string) bool { return strings.EqualFold(f, folder) }) {
			return r, true
		}
	}
	return ref.Ref{}, false
}

var sha256Hex = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Validate reports every problem in s, joined. Folder names are checked
// strictly because Remove deletes them: a tampered or corrupt state file
// must not be able to point outside the AddOns folder.
func (s *State) Validate() error {
	var errs []error
	owners := map[string]ref.Ref{} // lower-cased folder → owner
	for _, r := range s.Refs() {
		a := s.Addons[r]
		if !sha256Hex.MatchString(a.SHA256) {
			errs = append(errs, fmt.Errorf("addon %s: sha256 %q isn't 64 lowercase hex digits", r, a.SHA256))
		}
		if a.InstalledAt.IsZero() {
			errs = append(errs, fmt.Errorf("addon %s: installed_at is missing", r))
		}
		if len(a.Folders) == 0 {
			errs = append(errs, fmt.Errorf("addon %s: owns no folders", r))
		}
		for _, f := range a.Folders {
			if err := validateFolder(f); err != nil {
				errs = append(errs, fmt.Errorf("addon %s: %w", r, err))
				continue
			}
			key := strings.ToLower(f)
			if other, ok := owners[key]; ok {
				errs = append(errs, fmt.Errorf("addon %s: folder %q is also owned by %s", r, f, other))
				continue
			}
			owners[key] = r
		}
	}
	return errors.Join(errs...)
}

// validateFolder checks that name is a single path element: no separators
// on any OS, and not "." or "..".
func validateFolder(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("folder name %q isn't allowed", name)
	case strings.ContainsAny(name, `/\:`) || strings.ContainsRune(name, 0):
		return fmt.Errorf("folder name %q must be a plain name, not a path", name)
	}
	return nil
}
