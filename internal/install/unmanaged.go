package install

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Unmanaged returns the folders in addonsDir that aren't in owned: addons
// installed some other way, by hand or by another manager, sorted without
// regard to case. Hidden folders, Knapsack's own working folders among
// them, and files are left out. A symlink to a folder counts as a folder,
// since addon authors often link their working copy into AddOns. A missing
// addonsDir has none.
func Unmanaged(addonsDir string, owned []string) ([]string, error) {
	entries, err := os.ReadDir(addonsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	onDisk := newListing(entries)
	managed := map[string]bool{}
	for _, name := range owned {
		if actual, ok := ownedFolder(addonsDir, onDisk, name); ok {
			managed[actual] = true
		}
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if managed[name] || strings.HasPrefix(name, ".") || !isDir(addonsDir, e) {
			continue
		}
		out = append(out, name)
	}
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
	})
	return out, nil
}

// ownedFolder returns the folder on disk that the owned name is. Usually
// that's an exact match. A folder differing only in case is the same one
// only on a case-insensitive filesystem, as on Windows and macOS, which is
// when looking the owned name up finds it; on Linux it's another folder.
func ownedFolder(addonsDir string, onDisk listing, name string) (string, bool) {
	actual, ok := onDisk.find(name)
	if !ok || actual == name {
		return actual, ok
	}
	_, err := os.Lstat(filepath.Join(addonsDir, name))
	return actual, err == nil
}

// isDir reports whether e is a folder, or a symlink to one. Only symlinks
// need a stat: ReadDir already knows the type of everything else.
func isDir(dir string, e os.DirEntry) bool {
	if e.Type()&fs.ModeSymlink == 0 {
		return e.IsDir()
	}
	info, err := os.Stat(filepath.Join(dir, e.Name()))
	return err == nil && info.IsDir()
}
