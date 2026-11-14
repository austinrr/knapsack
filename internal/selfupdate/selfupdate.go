// Package selfupdate replaces the running knapsack binary with a release
// archive's (DESIGN.md §8.4): which archive fits this platform, whether
// one version is newer than another, whether a package manager owns the
// binary, and the swap itself. Like install, it never touches the
// network; app downloads the archive.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// AssetName is the release archive for a platform, as .goreleaser.yaml
// names it: knapsack_0.2.0_linux_amd64.tar.gz, or .zip for Windows.
func AssetName(version, goos, goarch string) string {
	ext := ".tar.gz"
	if goos == "windows" {
		ext = ".zip"
	}
	return fmt.Sprintf("knapsack_%s_%s_%s%s", strings.TrimPrefix(version, "v"), goos, goarch, ext)
}

// Newer reports whether version a is newer than b. Both are vX.Y.Z,
// optionally with a pre-release suffix (v1.2.3-rc1), which sorts before
// the release itself; suffixes aren't compared with each other. A version
// that doesn't parse, like "dev", is never newer or older.
func Newer(a, b string) bool {
	va, okA := parse(a)
	vb, okB := parse(b)
	if !okA || !okB {
		return false
	}
	for i := range 3 {
		if va.core[i] != vb.core[i] {
			return va.core[i] > vb.core[i]
		}
	}
	return !va.pre && vb.pre
}

type version struct {
	core [3]int
	pre  bool
}

func parse(s string) (version, bool) {
	s, ok := strings.CutPrefix(s, "v")
	if !ok {
		return version{}, false
	}
	var v version
	if core, _, found := strings.Cut(s, "-"); found {
		s, v.pre = core, true
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return version{}, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}, false
		}
		v.core[i] = n
	}
	return v, true
}

// PackageManager names the package manager that installed the binary at
// exe, judged by where it lives, or returns "" for none. Replacing a
// binary a package manager owns would leave the manager's records wrong.
func PackageManager(exe string) string {
	// Lower-cased, since Windows and macOS paths ignore case, and with /
	// for \ whatever the OS, so the checks are the same everywhere.
	p := strings.ToLower(strings.ReplaceAll(exe, `\`, "/"))
	switch {
	case strings.Contains(p, "/cellar/"), strings.HasPrefix(p, "/opt/homebrew/"), strings.HasPrefix(p, "/home/linuxbrew/"):
		return "Homebrew"
	case strings.Contains(p, "/scoop/apps/"):
		return "Scoop"
	case strings.Contains(p, "/winget/packages/"):
		return "winget"
	case strings.HasPrefix(p, "/nix/store/"):
		return "Nix"
	case strings.HasPrefix(p, "/usr/bin/"), strings.HasPrefix(p, "/usr/sbin/"),
		strings.HasPrefix(p, "/bin/"), strings.HasPrefix(p, "/sbin/"):
		return "your system's package manager"
	}
	return ""
}

// maxBinary bounds the binary extracted from an archive; knapsack is a few
// megabytes.
const maxBinary = 256 << 20

// ErrNoBinary means the archive has no knapsack binary at its root.
var ErrNoBinary = errors.New("archive has no knapsack binary")

// Replace replaces the binary at exe with the one in the release archive
// at archivePath, keeping exe's permissions. The archive is this
// platform's, as AssetName names it: a .zip on Windows, else a .tar.gz;
// the file's own name doesn't matter.
// The new binary is written next to exe first, so a failed extraction
// leaves exe as it was.
func Replace(exe, archivePath string) error {
	info, err := os.Stat(exe)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".knapsack-new-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }() // gone after a successful swap
	err = extract(archivePath, binaryName(), runtime.GOOS == "windows", tmp)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	if err := os.Chmod(tmpPath, info.Mode().Perm()|0o111); err != nil {
		return err
	}
	return swap(exe, tmpPath, runtime.GOOS == "windows")
}

func binaryName() string {
	if runtime.GOOS == "windows" {
		return "knapsack.exe"
	}
	return "knapsack"
}

// OldPath is where Windows leaves the replaced binary: a running .exe can
// be renamed but not deleted. RemoveOld deletes it on a later run.
func OldPath(exe string) string { return exe + ".old" }

// RemoveOld deletes the binary a previous update left behind, if any.
func RemoveOld(exe string) {
	_ = os.Remove(OldPath(exe)) // usually there's nothing to remove
}

// swap moves newPath to exe. Elsewhere a rename replaces exe in one step,
// even while it runs. Windows won't replace a running .exe, but will
// rename it, so exe moves aside first and is put back if the second
// rename fails.
func swap(exe, newPath string, windows bool) error {
	if !windows {
		return os.Rename(newPath, exe)
	}
	old := OldPath(exe)
	_ = os.Remove(old) // left by an earlier update
	if err := os.Rename(exe, old); err != nil {
		return err
	}
	if err := os.Rename(newPath, exe); err != nil {
		if restoreErr := os.Rename(old, exe); restoreErr != nil {
			return errors.Join(err, fmt.Errorf("restore %s: %w", exe, restoreErr))
		}
		return err
	}
	_ = os.Remove(old) // fails while the old binary runs; RemoveOld retries
	return nil
}

// extract copies the file called name at the root of the archive, a zip
// or else a gzipped tar, into w.
func extract(archivePath, name string, isZip bool, w io.Writer) error {
	if isZip {
		return extractZip(archivePath, name, w)
	}
	return extractTarGz(archivePath, name, w)
}

func extractTarGz(archivePath, name string, w io.Writer) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // read-only; nothing to flush
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(archivePath), err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return ErrNoBinary
		}
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(archivePath), err)
		}
		if h.Typeflag == tar.TypeReg && strings.TrimPrefix(h.Name, "./") == name {
			return copyBinary(w, tr)
		}
	}
}

func extractZip(archivePath, name string, w io.Writer) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("read %s: %w", filepath.Base(archivePath), err)
	}
	defer func() { _ = zr.Close() }() // read-only; nothing to flush
	for _, f := range zr.File {
		if f.Name != name || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		defer func() { _ = rc.Close() }() // read-only; nothing to flush
		return copyBinary(w, rc)
	}
	return ErrNoBinary
}

func copyBinary(w io.Writer, r io.Reader) error {
	n, err := io.Copy(w, io.LimitReader(r, maxBinary+1))
	if err != nil {
		return err
	}
	if n > maxBinary {
		return fmt.Errorf("binary in archive is over %d MiB", maxBinary>>20)
	}
	return nil
}
