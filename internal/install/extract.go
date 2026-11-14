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
// symlink, corrupt data, an archive with no addon folders, and so on.
var ErrBadArchive = errors.New("bad archive")

// stagingPrefix starts the name of each staging folder in AddOns. The
// client ignores these folders: none has a TOC matching its name.
const stagingPrefix = ".knapsack-staging-"

// Limits on one archive, so a zip bomb fails instead of filling the disk or
// running out of inodes. The largest addons are tens of megabytes and a few
// thousand files.
const (
	maxExtractedSize = 1 << 30
	maxEntries       = 100_000
)

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
// symlinks and other special files, and paths that collide on a
// case-insensitive filesystem. On error, nothing is left behind.
func extract(ctx context.Context, archivePath, addonsDir string) (staged, error) {
	// Open the file separately, so failing to read it is an I/O error and
	// only failing to parse it is a bad archive.
	file, err := os.Open(archivePath)
	if err != nil {
		return staged{}, err
	}
	defer func() { _ = file.Close() }() // read-only; nothing to flush
	info, err := file.Stat()
	if err != nil {
		return staged{}, err
	}
	zr, err := zip.NewReader(file, info.Size())
	if err != nil {
		return staged{}, fmt.Errorf("%w: %w", ErrBadArchive, err)
	}

	dir, err := os.MkdirTemp(addonsDir, stagingPrefix)
	if err != nil {
		return staged{}, fmt.Errorf("create staging folder: %w", err)
	}
	s, err := extractInto(ctx, zr, dir, maxExtractedSize)
	if err != nil {
		_ = os.RemoveAll(dir) // best effort; Cleanup catches anything left
		return staged{}, err
	}
	return s, nil
}

// extractInto extracts every entry of zr into dir, writing at most limit
// bytes, then finds the addon folders.
func extractInto(ctx context.Context, zr *zip.Reader, dir string, limit int64) (staged, error) {
	if len(zr.File) > maxEntries {
		return staged{}, fmt.Errorf("%w: more than %d entries", ErrBadArchive, maxEntries)
	}
	x := &extractor{ctx: ctx, dir: dir, limit: limit, seen: map[string]seenPath{}}
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return staged{}, err
		}
		if err := x.extractEntry(f); err != nil {
			return staged{}, err
		}
	}
	return findFolders(dir)
}

// extractor extracts one archive into dir.
type extractor struct {
	ctx     context.Context
	dir     string
	limit   int64 // bytes the whole archive may write
	written int64
	// seen records every path created so far, folders implied by a file's
	// path included, keyed by its lower-cased form. That's how paths that
	// are one file or folder on Windows and macOS are caught.
	seen map[string]seenPath
}

// seenPath is a path the archive created, as it spelled it.
type seenPath struct {
	name string
	dir  bool
}

// extractEntry extracts one entry.
func (x *extractor) extractEntry(f *zip.File) error {
	name, err := entryName(f.Name)
	if err != nil || name == "" {
		return err // "" is the archive's root, written as "./" or "/"
	}
	mode := f.Mode()
	switch {
	case mode.IsDir():
		return x.mkdirs(name)
	case mode.IsRegular():
		if parent := path.Dir(name); parent != "." {
			if err := x.mkdirs(parent); err != nil {
				return err
			}
		}
		if err := x.claim(name, false); err != nil {
			return err
		}
		return x.writeFile(f, name)
	default:
		// Symlinks could point outside AddOns; devices and pipes have no
		// place in an addon.
		return fmt.Errorf("%w: %q is a %s, not a file or folder", ErrBadArchive, f.Name, describeMode(mode))
	}
}

// claim records that the archive creates name, a file or a folder. It's an
// error if the archive already created the same path in a different case,
// a file where a folder is (or the reverse), or the same file twice.
// Claiming the same folder again is fine.
func (x *extractor) claim(name string, dir bool) error {
	key := strings.ToLower(name)
	prev, ok := x.seen[key]
	switch {
	case !ok:
		x.seen[key] = seenPath{name: name, dir: dir}
		return nil
	case prev.name != name:
		return fmt.Errorf("%w: %q and %q are the same path on Windows and macOS, where case doesn't matter",
			ErrBadArchive, prev.name, name)
	case prev.dir != dir:
		return fmt.Errorf("%w: %q is both a file and a folder", ErrBadArchive, name)
	case !dir:
		return fmt.Errorf("%w: %q appears twice", ErrBadArchive, name)
	}
	return nil
}

// mkdirs creates folder name and any parents not yet created, claiming
// each one.
func (x *extractor) mkdirs(name string) error {
	var prefix string
	for elem := range strings.SplitSeq(name, "/") {
		prefix = path.Join(prefix, elem)
		if x.seen[strings.ToLower(prefix)] == (seenPath{name: prefix, dir: true}) {
			continue // created by an earlier entry
		}
		if err := x.claim(prefix, true); err != nil {
			return err
		}
		if err := os.Mkdir(x.local(prefix), 0o700); err != nil {
			return err
		}
	}
	return nil
}

// local turns a slash-separated path from entryName into a path under dir.
func (x *extractor) local(name string) string {
	return filepath.Join(x.dir, filepath.FromSlash(name))
}

// writeFile writes one file entry, counting its bytes against the limit.
// O_EXCL is a last guard against overwriting anything.
//
// Permissions in the archive are ignored. Files are written 0600 and
// folders 0700, like Knapsack's own files: the game runs as the user who
// installs addons, so no one else needs access.
func (x *extractor) writeFile(f *zip.File, name string) error {
	src, err := f.Open()
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrBadArchive, f.Name, err)
	}
	defer func() { _ = src.Close() }() // read-only; nothing to flush

	dst, err := os.OpenFile(x.local(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	// Read one byte past what's left, to tell "exactly at the limit" from
	// "over it" without trusting the size the archive claims.
	remaining := x.limit - x.written
	r := &entryReader{ctx: x.ctx, r: io.LimitReader(src, remaining+1), name: f.Name}
	n, err := io.Copy(dst, r)
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	x.written += n
	if err != nil {
		return err
	}
	if n > remaining {
		return fmt.Errorf("%w: more than %s uncompressed", ErrBadArchive, formatSize(x.limit))
	}
	return nil
}

// formatSize formats a size limit for messages: "1024 MiB", or bytes when
// it isn't a whole number of MiB.
func formatSize(n int64) string {
	if n >= 1<<20 && n%(1<<20) == 0 {
		return fmt.Sprintf("%d MiB", n>>20)
	}
	return fmt.Sprintf("%d bytes", n)
}

// entryReader reads one entry's data. A read error means corrupt data (a
// checksum or decompression failure), so it wraps ErrBadArchive, while
// write errors from io.Copy stay I/O errors. It also stops a long entry
// promptly when ctx is cancelled.
type entryReader struct {
	ctx  context.Context
	r    io.Reader
	name string
}

func (e *entryReader) Read(p []byte) (int, error) {
	if err := e.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := e.r.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = fmt.Errorf("%w: %s: %w", ErrBadArchive, e.name, err)
	}
	return n, err
}

// entryName turns an archive entry's name into a clean, slash-separated
// path relative to the staging folder, or "" for the root. Backslashes are
// treated as separators, since some Windows tools write them. A name that
// is absolute or climbs out of the staging folder is an error.
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
	if _, err := filepath.Localize(clean); err != nil {
		return "", fmt.Errorf("%w: %q escapes the archive or isn't a valid file name", ErrBadArchive, raw)
	}
	return clean, nil
}

// hasVolume reports whether name starts with a Windows drive letter, such
// as C:, which filepath.IsAbs on Unix doesn't recognize.
func hasVolume(name string) bool {
	return len(name) >= 2 && name[1] == ':' &&
		(name[0] >= 'a' && name[0] <= 'z' || name[0] >= 'A' && name[0] <= 'Z')
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
// inside it; wrappers can nest.
//
// Other files and junk folders are ignored: top-level files such as a
// README, hidden folders such as .github, and __MACOSX, which Finder adds
// to zips. A TOC at the top level is an error, since the addon's files
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
			switch {
			case e.IsDir() && !isJunk(e.Name()):
				folders = append(folders, e.Name())
			case isTOC(e):
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

// isJunk reports whether a top-level folder is never an addon: hidden
// folders, which the client doesn't load, and Finder's __MACOSX.
func isJunk(name string) bool {
	return strings.HasPrefix(name, ".") || name == "__MACOSX"
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
