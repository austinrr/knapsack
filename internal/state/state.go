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
// case as on Windows and macOS, or false if none does. Validate ensures a
// folder has at most one owner, so map order doesn't matter.
func (s *State) Owner(folder string) (ref.Ref, bool) {
	key := folderKey(folder)
	for r, a := range s.Addons {
		if slices.ContainsFunc(a.Folders, func(f string) bool { return folderKey(f) == key }) {
			return r, true
		}
	}
	return ref.Ref{}, false
}

// folderKey is the form in which two folder names are the same folder on
// Windows and macOS. Validate and Owner both compare with it, so they agree.
func folderKey(name string) string {
	return strings.ToLower(name)
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
		} else if y := a.InstalledAt.UTC().Year(); y < 1 || y > 9999 {
			// TOML can't represent other years, so the file couldn't be read back.
			errs = append(errs, fmt.Errorf("addon %s: installed_at year %d is out of range", r, y))
		}
		if len(a.Folders) == 0 {
			errs = append(errs, fmt.Errorf("addon %s: owns no folders", r))
		}
		for _, f := range a.Folders {
			if err := validateFolder(f); err != nil {
				errs = append(errs, fmt.Errorf("addon %s: %w", r, err))
				continue
			}
			key := folderKey(f)
			if other, ok := owners[key]; ok {
				errs = append(errs, fmt.Errorf("addon %s: folder %q is also owned by %s", r, f, other))
				continue
			}
			owners[key] = r
		}
	}
	return errors.Join(errs...)
}

// validateFolder checks that name is a single path element that means the
// same folder on every OS: no separators, not "." or "..", no trailing dot
// or space (Windows strips them, so "Baganator." is "Baganator"), and not a
// Windows device name such as NUL.
func validateFolder(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("folder name %q isn't allowed", name)
	case strings.ContainsAny(name, `/\:`) || strings.ContainsRune(name, 0):
		return fmt.Errorf("folder name %q must be a plain name, not a path", name)
	case strings.HasSuffix(name, ".") || strings.HasSuffix(name, " "):
		return fmt.Errorf("folder name %q can't end in a dot or space", name)
	case isDeviceName(name):
		return fmt.Errorf("folder name %q is a Windows device name", name)
	}
	return nil
}

// isDeviceName reports whether name is reserved on Windows, with or
// without an extension: CON, PRN, AUX, NUL, COM1–COM9 and LPT1–LPT9.
func isDeviceName(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) {
		return base[3] >= '1' && base[3] <= '9'
	}
	return false
}
