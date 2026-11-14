package install

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
)

// Errors from Install and Remove that callers turn into hints. Match them
// with errors.Is.
var (
	// ErrConflict means a folder the archive installs already exists and
	// isn't one the addon owned before. Options.Force replaces it.
	ErrConflict = errors.New("folders already exist and this addon doesn't own them")
	// ErrInUse means a folder couldn't be moved because a file in it is
	// open, on Windows usually by the game.
	ErrInUse = errors.New("folder is in use")
)

// The folders Knapsack creates in AddOns while it works, besides staging
// folders. The client ignores them: none has a TOC matching its name.
const (
	// backupPrefix holds folders moved aside during a swap. If one is left
	// behind, the swap was interrupted, and Cleanup moves them back.
	backupPrefix = ".knapsack-backup-"
	// trashPrefix is a backup folder after a swap succeeded: its contents
	// are only waiting to be deleted.
	trashPrefix = ".knapsack-trash-"
)

// Options configure Install.
type Options struct {
	// Flavor is the profile's client, and ClientInterface its exact
	// Interface number, or 0 if unknown (see Validate).
	Flavor          flavor.Flavor
	ClientInterface int
	// Previous are the folders the addon owned before, from state. They
	// may be replaced, and any the new release no longer has are removed.
	Previous []string
	// Force also replaces folders that exist but no addon owns.
	Force bool
}

// Install installs the zip at archivePath into addonsDir and returns the
// folders it now owns, plus warnings such as out-of-date TOCs (DESIGN.md
// §6.3). Either every folder is replaced or AddOns is left as it was.
//
// Callers run Cleanup first, so leftovers from an interrupted run are
// resolved before anything new is moved.
func Install(ctx context.Context, archivePath, addonsDir string, opts Options) (folders []string, warnings []string, err error) {
	s, err := extract(ctx, archivePath, addonsDir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(s.dir) }() // best effort; Cleanup catches anything left

	var errs []error
	for _, f := range s.folders {
		w, err := Validate(filepath.Join(s.root, f), opts.Flavor, opts.ClientInterface)
		if err != nil {
			errs = append(errs, err)
		} else if w != "" {
			warnings = append(warnings, w)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, nil, err
	}

	onDisk, err := listDir(addonsDir)
	if err != nil {
		return nil, nil, err
	}
	out, err := foldersToReplace(s.folders, opts.Previous, onDisk, opts.Force)
	if err != nil {
		return nil, nil, err
	}
	// Past this point the swap runs to completion or rolls back, so it
	// isn't interrupted by cancellation.
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := swap(addonsDir, s.root, s.folders, out, rename); err != nil {
		return nil, nil, err
	}
	return s.folders, warnings, nil
}

// foldersToReplace returns the existing folders to move out of AddOns, by
// their names on disk: each folder the new release has, and each previous
// folder it no longer has. A new folder that exists but isn't in previous is
// a conflict unless force is set. Names are matched as listing.find does.
func foldersToReplace(incoming, previous []string, onDisk listing, force bool) ([]string, error) {
	owned, kept := foldSet(previous), foldSet(incoming)
	var out, conflicts []string
	seen := map[string]bool{}
	add := func(actual string) {
		if !seen[actual] {
			seen[actual] = true
			out = append(out, actual)
		}
	}
	for _, f := range incoming {
		actual, exists := onDisk.find(f)
		if !exists {
			continue
		}
		if !owned[strings.ToLower(f)] && !force {
			conflicts = append(conflicts, actual)
		}
		add(actual)
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrConflict, strings.Join(conflicts, ", "))
	}
	for _, p := range previous {
		if kept[strings.ToLower(p)] {
			continue
		}
		if actual, exists := onDisk.find(p); exists {
			add(actual)
		}
	}
	return out, nil
}

// foldSet returns the lower-cased names as a set.
func foldSet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[strings.ToLower(n)] = true
	}
	return set
}

// listing is what's in AddOns, apart from Knapsack's own working folders.
type listing struct {
	exact  map[string]bool
	folded map[string]string // lower-cased name → first name on disk with it
}

// find returns the name on disk of the folder called name. An exact match
// wins; otherwise names are matched without regard to case, as on Windows
// and macOS. On a case-sensitive filesystem that holds both Details and
// details, find("Details") is Details, never the other folder.
func (l listing) find(name string) (string, bool) {
	if l.exact[name] {
		return name, true
	}
	actual, ok := l.folded[strings.ToLower(name)]
	return actual, ok
}

// listDir lists dir for find.
func listDir(dir string) (listing, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return listing{}, err
	}
	l := listing{exact: map[string]bool{}, folded: map[string]string{}}
	for _, e := range entries {
		name := e.Name()
		if isWorkFolder(name) {
			continue
		}
		l.exact[name] = true
		if _, ok := l.folded[strings.ToLower(name)]; !ok {
			l.folded[strings.ToLower(name)] = name
		}
	}
	return l, nil
}

// isWorkFolder reports whether name is one of Knapsack's own folders in
// AddOns.
func isWorkFolder(name string) bool {
	return strings.HasPrefix(name, stagingPrefix) ||
		strings.HasPrefix(name, backupPrefix) ||
		strings.HasPrefix(name, trashPrefix)
}

// move is one rename swap did, kept so it can be undone.
type move struct{ from, to string }

// swap moves the folders out (names in addonsDir) into a backup folder,
// then the folders in (names in stagedRoot) into addonsDir. If any move
// fails, every move so far is undone in reverse order. Once all succeed the
// backup becomes trash and is deleted.
//
// rename is a parameter so tests can make a move fail partway.
func swap(addonsDir, stagedRoot string, in, out []string, rename func(oldpath, newpath string) error) error {
	backup, err := os.MkdirTemp(addonsDir, backupPrefix)
	if err != nil {
		return fmt.Errorf("create backup folder: %w", err)
	}
	var done []move
	do := func(from, to string) error {
		if err := rename(from, to); err != nil {
			return err
		}
		done = append(done, move{from, to})
		return nil
	}
	var failed error
	for _, name := range out {
		if failed = do(filepath.Join(addonsDir, name), filepath.Join(backup, name)); failed != nil {
			break
		}
	}
	for _, name := range in {
		if failed != nil {
			break
		}
		failed = do(filepath.Join(stagedRoot, name), filepath.Join(addonsDir, name))
	}
	if failed != nil {
		return rollback(done, backup, failed, rename)
	}
	discard(backup, rename)
	return nil
}

// rollback undoes done in reverse order. If that fails too, the backup is
// kept, and the error says where it is; Cleanup moves its folders back.
func rollback(done []move, backup string, cause error, rename func(oldpath, newpath string) error) error {
	var errs []error
	for i := len(done) - 1; i >= 0; i-- {
		if err := rename(done[i].to, done[i].from); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("%w (rolling back also failed, so the old folders are in %s; knapsack restores them on its next run: %w)",
			cause, backup, errors.Join(errs...))
	}
	_ = os.Remove(backup) // empty after a full rollback
	return cause
}

// discard deletes a backup folder after a successful swap. It first marks
// the swap finished, so that if deleting is interrupted or fails, Cleanup
// deletes what's left rather than restoring old folders from it. Renaming
// the backup to trash is the mark; it's atomic. If the rename fails (on
// Windows, a scanner may hold a file in it), an empty trash file of the same
// name is the mark instead, and it's removed once the backup is gone.
func discard(backup string, rename func(oldpath, newpath string) error) {
	trash := trashFor(backup)
	if rename(backup, trash) == nil {
		_ = os.RemoveAll(trash) // best effort; Cleanup catches anything left
		return
	}
	if err := os.WriteFile(trash, nil, 0o600); err != nil {
		return // unmarked; Cleanup will treat it as interrupted
	}
	if os.RemoveAll(backup) == nil {
		_ = os.Remove(trash)
	}
}

// trashFor is the trash name that marks backup's swap as finished: the same
// random suffix after trashPrefix.
func trashFor(backup string) string {
	return filepath.Join(filepath.Dir(backup), trashPrefix+strings.TrimPrefix(filepath.Base(backup), backupPrefix))
}

// Remove deletes folders from addonsDir. Folders that don't exist are
// skipped. Like Install, it moves them all aside first, so either every
// folder is removed or none is.
func Remove(addonsDir string, folders []string) error {
	for _, f := range folders {
		if !filepath.IsLocal(f) || strings.ContainsAny(f, `/\`) {
			return fmt.Errorf("remove: %q isn't a folder name", f)
		}
	}
	onDisk, err := listDir(addonsDir)
	if err != nil {
		return err
	}
	var out []string
	for _, f := range folders {
		if actual, ok := onDisk.find(f); ok && !slices.Contains(out, actual) {
			out = append(out, actual)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return swap(addonsDir, "", nil, out, rename)
}

// Cleanup resolves what an interrupted run left in addonsDir.
//
// A backup means a swap didn't finish, unless discard marked it finished.
// Every folder in an unfinished backup is moved back, replacing any new
// folder already moved into its place: AddOns then holds the release state
// still records, rather than a mix of old and new folders. A backup that
// can't be fully restored is kept, so nothing is lost, and that is the only
// error. Staging and trash are deleted on a best-effort basis: leftovers
// there are harmless, and failing over them would block every operation
// while, say, a scanner holds a file.
func Cleanup(addonsDir string) error {
	entries, err := os.ReadDir(addonsDir)
	if err != nil {
		return err
	}
	finished := map[string]bool{} // backup suffixes marked finished
	for _, e := range entries {
		if suffix, ok := strings.CutPrefix(e.Name(), trashPrefix); ok {
			finished[suffix] = true
		}
	}

	var errs []error
	for _, e := range entries {
		path := filepath.Join(addonsDir, e.Name())
		suffix, isBackup := strings.CutPrefix(e.Name(), backupPrefix)
		switch {
		case strings.HasPrefix(e.Name(), stagingPrefix):
			_ = os.RemoveAll(path)
		case isBackup && finished[suffix]:
			_ = os.RemoveAll(path)
		case isBackup:
			if err := restore(addonsDir, path, rename); err != nil {
				errs = append(errs, err)
				continue
			}
			_ = os.Remove(path) // empty now
		}
	}
	// Delete trash last, and keep a mark while its backup still exists.
	for _, e := range entries {
		suffix, ok := strings.CutPrefix(e.Name(), trashPrefix)
		if !ok {
			continue
		}
		if _, err := os.Lstat(filepath.Join(addonsDir, backupPrefix+suffix)); err == nil {
			continue
		}
		_ = os.RemoveAll(filepath.Join(addonsDir, e.Name()))
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("restore folders after an interrupted run: %w", err)
	}
	return nil
}

// restore moves each folder in backup back into addonsDir. A folder of the
// same name already there is the new one from the unfinished swap, so it's
// moved aside into trash first, and moved back if the old one can't be.
func restore(addonsDir, backup string, rename func(oldpath, newpath string) error) error {
	saved, err := os.ReadDir(backup)
	if err != nil {
		return err
	}
	onDisk, err := listDir(addonsDir)
	if err != nil {
		return err
	}
	var aside string // trash for the newer folders, created when needed
	var errs []error
	for _, e := range saved {
		name := e.Name()
		newer, exists := onDisk.find(name)
		if exists {
			if aside == "" {
				if aside, err = os.MkdirTemp(addonsDir, trashPrefix); err != nil {
					return err
				}
			}
			if err := rename(filepath.Join(addonsDir, newer), filepath.Join(aside, newer)); err != nil {
				errs = append(errs, err)
				continue
			}
		}
		if err := rename(filepath.Join(backup, name), filepath.Join(addonsDir, name)); err != nil {
			errs = append(errs, err)
			if exists {
				// Put the newer folder back rather than leave neither.
				if err := rename(filepath.Join(aside, newer), filepath.Join(addonsDir, newer)); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}
	if aside != "" && len(errs) == 0 {
		_ = os.RemoveAll(aside) // best effort; the next Cleanup deletes it
	}
	return errors.Join(errs...)
}
