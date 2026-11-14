package install

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// ErrBadArchive is wrapped by every error about the archive's contents, as
// opposed to I/O errors: an entry that would escape the staging folder, a
// symlink, an archive with no addon folders, and so on.
var ErrBadArchive = errors.New("bad archive")

// stagingPrefix starts the name of each staging folder in AddOns. The
// client ignores these folders: none has a TOC matching its name.
const stagingPrefix = ".knapsack-staging-"

// maxExtractedSize bounds the bytes extracted from one archive, so a zip
// bomb fills an error instead of the disk. The largest addons are tens of
// megabytes.
const maxExtractedSize = 1 << 30

// staged is an archive extracted into AddOns, ready to swap into place.
type staged struct {
	// dir is the staging folder. The caller removes it when done.
	dir string
	// root is the folder inside dir that holds the addon folders: dir
	// itself, or a wrapper folder the archive put them in.
	root string
	// folders are the addon folders in root, sorted.
	folders []string
}

// extract extracts the zip at archivePath into a new staging folder in
// addonsDir, so the later swap is a rename on the same filesystem
// (DESIGN.md §6.3, steps 2 and 3).
//
// It refuses entries that are absolute or would escape the staging folder,
// symlinks and other special files, and names that collide on a
// case-insensitive filesystem. On error, nothing is left behind.
func extract(ctx context.Context, archivePath, addonsDir string) (staged, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return staged{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}
	defer func() { _ = zr.Close() }() // read-only; nothing to flush

	dir, err := os.MkdirTemp(addonsDir, stagingPrefix)
	if err != nil {
		return staged{}, fmt.Errorf("create staging folder: %w", err)
	}
	s, err := extractInto(ctx, &zr.Reader, dir, maxExtractedSize)
	if err != nil {
		_ = os.RemoveAll(dir) // best effort; Cleanup catches anything left
		return staged{}, err
	}
	return s, nil
}

// extractInto extracts every entry of zr into dir, writing at most limit
// bytes, then finds the addon folders.
func extractInto(ctx context.Context, zr *zip.Reader, dir string, limit int64) (staged, error) {
	seen := map[string]string{} // lower-cased name → name as in the archive
	budget := limit
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return staged{}, err
		}
		name, err := entryName(f.Name)
		if err != nil {
			return staged{}, err
		}
		if name == "" {
			continue // the archive's root, written as "./" or "/"
		}
		if other, ok := seen[strings.ToLower(name)]; ok && !f.FileInfo().IsDir() {
			if other == f.Name {
				return staged{}, fmt.Errorf("%w: %q appears twice", ErrBadArchive, f.Name)
			}
			return staged{}, fmt.Errorf("%w: %q and %q are the same file on Windows and macOS, where case doesn't matter", ErrBadArchive, other, f.Name)
		}
		seen[strings.ToLower(name)] = f.Name

		target := filepath.Join(dir, name)
		mode := f.Mode()
		switch {
		case mode.IsDir():
			if err := os.MkdirAll(target, 0o700); err != nil {
				return staged{}, err
			}
		case mode.IsRegular():
			n, err := extractFile(f, target, budget)
			if err != nil {
				return staged{}, err
			}
			budget -= n
		default:
			// Symlinks could point outside AddOns; devices and pipes have
			// no place in an addon.
			return staged{}, fmt.Errorf("%w: %q is a %s, not a file or folder", ErrBadArchive, f.Name, describeMode(mode))
		}
	}
	return findFolders(dir)
}

// entryName turns an archive entry's name into a safe path relative to the
// staging folder, in the OS's form. Backslashes are treated as separators,
// since some Windows tools write them. A name that is absolute or climbs
// out of the staging folder is an error.
func entryName(raw string) (string, error) {
	slashed := strings.ReplaceAll(raw, `\`, "/")
	if strings.HasPrefix(slashed, "/") || hasVolume(slashed) {
		return "", fmt.Errorf("%w: %q is an absolute path", ErrBadArchive, raw)
	}
	clean := path.Clean(slashed)
	if clean == "." {
		return "", nil
	}
	// Localize rejects ".." and names the OS can't hold, such as NUL or
	// a name containing ':' on Windows.
	local, err := filepath.Localize(clean)
	if err != nil {
		return "", fmt.Errorf("%w: %q escapes the archive or isn't a valid file name", ErrBadArchive, raw)
	}
	return local, nil
}

// hasVolume reports whether name starts with a Windows drive letter, such
// as C:, which filepath.IsAbs on Unix doesn't recognize.
func hasVolume(name string) bool {
	return len(name) >= 2 && name[1] == ':' &&
		(name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z')
}

// Permissions in the archive are ignored. Files are written 0600 and
// folders 0700, like Knapsack's own files: the game runs as the user who
// installs addons, so no one else needs access.

// extractFile writes one entry to target, failing if it would take more
// than budget bytes. It returns the bytes written. O_EXCL makes a
// duplicate entry an error rather than a silent overwrite.
func extractFile(f *zip.File, target string, budget int64) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return 0, err
	}
	src, err := f.Open()
	if err != nil {
		return 0, fmt.Errorf("%w: %s: %w", ErrBadArchive, f.Name, err)
	}
	defer func() { _ = src.Close() }() // read-only; nothing to flush

	dst, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return 0, fmt.Errorf("%w: %q appears twice", ErrBadArchive, f.Name)
	}
	if err != nil {
		return 0, err
	}
	// Read one byte past the budget, to tell "exactly at the limit" from
	// "over it" without trusting the size the archive claims.
	n, err := io.Copy(dst, io.LimitReader(src, budget+1))
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("extract %s: %w", f.Name, err)
	}
	if n > budget {
		return n, fmt.Errorf("%w: more than %d MiB uncompressed", ErrBadArchive, maxExtractedSize>>20)
	}
	return n, nil
}

// describeMode names a non-regular file type for messages.
func describeMode(m fs.FileMode) string {
	switch {
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m&fs.ModeDevice != 0:
		return "device"
	case m&fs.ModeNamedPipe != 0:
		return "named pipe"
	case m&fs.ModeSocket != 0:
		return "socket"
	}
	return "special file"
}

// findFolders finds the addon folders in an extracted archive. If the
// archive holds a single folder with no TOC in it, it's a wrapper, as in
// GitHub's source zips (repo-1.2/Addon/...), and the addon folders are
// inside it; wrappers can nest. Other top-level files, such as a README,
// are ignored, but a TOC at the top level is an error: the addon's files
// must be in a folder named after it.
func findFolders(dir string) (staged, error) {
	root := dir
	for {
		entries, err := os.ReadDir(root)
		if err != nil {
			return staged{}, err
		}
		var folders []string
		for _, e := range entries {
			if e.IsDir() {
				folders = append(folders, e.Name())
			} else if isTOC(e) {
				return staged{}, fmt.Errorf("%w: %s isn't in an addon folder", ErrBadArchive, e.Name())
			}
		}
		if len(folders) == 1 && !dirHasTOC(filepath.Join(root, folders[0])) {
			root = filepath.Join(root, folders[0])
			continue
		}
		if len(folders) == 0 {
			return staged{}, fmt.Errorf("%w: no addon folders in it", ErrBadArchive)
		}
		slices.Sort(folders)
		return staged{dir: dir, root: root, folders: folders}, nil
	}
}

// dirHasTOC reports whether dir directly contains a .toc file.
func dirHasTOC(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	return slices.ContainsFunc(entries, isTOC)
}

// isTOC reports whether e is a .toc file.
func isTOC(e fs.DirEntry) bool {
	return !e.IsDir() && strings.EqualFold(filepath.Ext(e.Name()), ".toc")
}
