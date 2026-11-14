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
// regard to case. Owned names are matched as Install matches them, so an
// owned "Details" covers a "details" folder on a case-insensitive
// filesystem. Hidden folders,
// Knapsack's own working folders among them, and files are left out. A
// symlink to a folder counts as a folder, since addon authors often link
// their working copy into AddOns. A missing addonsDir has none.
func Unmanaged(addonsDir string, owned []string) ([]string, error) {
	onDisk, err := listDir(addonsDir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	managed := map[string]bool{}
	for _, name := range owned {
		if actual, ok := onDisk.find(name); ok {
			managed[actual] = true
		}
	}
	var out []string
	for name := range onDisk.exact {
		if managed[name] || strings.HasPrefix(name, ".") {
			continue
		}
		// os.Stat follows symlinks, so a linked folder counts.
		if info, err := os.Stat(filepath.Join(addonsDir, name)); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, name)
	}
	slices.SortFunc(out, func(a, b string) int {
		return cmp.Or(strings.Compare(strings.ToLower(a), strings.ToLower(b)), strings.Compare(a, b))
	})
	return out, nil
}
