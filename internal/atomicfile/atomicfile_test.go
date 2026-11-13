package atomicfile_test

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/austinrr/knapsack/internal/atomicfile"
)

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// assertOnlyFile fails if dir holds anything besides name, such as a
// leftover temp file.
func assertOnlyFile(t *testing.T, dir, name string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != name {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("directory holds %v, want only %q", names, name)
	}
}

func TestWriteCreates(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := atomicfile.Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
	assertOnlyFile(t, dir, "config.toml")
}

func TestWriteReplaces(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("old contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(path, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, path); got != "new" {
		t.Errorf("contents = %q, want %q", got, "new")
	}
	assertOnlyFile(t, dir, "config.toml")
}

// Windows has no Unix permission bits, so this runs on Unix only.
func TestWritePerm(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions on Windows")
	}
	for _, perm := range []os.FileMode{0o600, 0o644} {
		path := filepath.Join(t.TempDir(), "f")
		if err := atomicfile.Write(path, []byte("x"), perm); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != perm {
			t.Errorf("mode = %v, want %v", got, perm)
		}
	}
}

// A write that fails partway leaves the old file exactly as it was and
// removes the temp file.
func TestWriteFuncFailureKeepsOldFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "state.toml")
	if err := os.WriteFile(path, []byte("old contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("encoder failed")
	err := atomicfile.WriteFunc(path, 0o600, func(w io.Writer) error {
		if _, err := io.WriteString(w, "half a fi"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("WriteFunc error = %v, want it to wrap %v", err, boom)
	}
	if got := readFile(t, path); got != "old contents" {
		t.Errorf("contents = %q, want the old contents intact", got)
	}
	assertOnlyFile(t, dir, "state.toml")
}

// The parent directory isn't created: the stores own their directories.
func TestWriteMissingDir(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "missing", "f")
	if err := atomicfile.Write(path, []byte("x"), 0o600); err == nil {
		t.Error("Write into a missing directory succeeded, want error")
	}
}

// Renaming over a directory fails, and the temp file is still cleaned up.
func TestWriteOverDirectory(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(target, []byte("x"), 0o600); err == nil {
		t.Fatal("Write over a directory succeeded, want error")
	}
	assertOnlyFile(t, dir, "target")
}

// Replacing a file never loosens the permissions it already had.
func TestWriteKeepsTighterPerm(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions on Windows")
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("api_key = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600 kept", got)
	}
}

// Writing through a symlink replaces the file it points to and keeps the
// link, as with a config linked from a dotfiles repo.
func TestWriteThroughSymlink(t *testing.T) {
	t.Parallel()
	dotfiles := t.TempDir()
	target := filepath.Join(dotfiles, "config.toml")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "config.toml")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("can't create symlinks here: %v", err) // Windows without developer mode
	}

	if err := atomicfile.Write(link, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(link)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Error("the symlink was replaced by a regular file")
	}
	if got := readFile(t, target); got != "new" {
		t.Errorf("link target contents = %q, want %q", got, "new")
	}
	assertOnlyFile(t, dotfiles, "config.toml")
}
