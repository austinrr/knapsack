package install

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// entry is one file in a test zip. A name ending in "/" is a folder.
type entry struct {
	name string
	body string
	mode fs.FileMode // 0 for a plain file or folder
}

// writeZip builds a zip from entries in a temp dir and returns its path.
func writeZip(t *testing.T, entries ...entry) string {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(e.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "addon.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// files lists every file under dir as slash-separated relative paths.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// Each normalization case: where the addon folders are found.
func TestExtractFolders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		entries     []entry
		wantRoot    string // relative to the staging folder, slash-separated
		wantFolders []string
	}{
		{
			name: "release zip",
			entries: []entry{
				{name: "Baganator/Baganator.toc"},
				{name: "Baganator/Core.lua"},
			},
			wantRoot:    ".",
			wantFolders: []string{"Baganator"},
		},
		{
			name: "several folders with readme",
			entries: []entry{
				{name: "README.md"},
				{name: "Details/Details.toc"},
				{name: "Details_DataStorage/Details_DataStorage.toc"},
			},
			wantRoot:    ".",
			wantFolders: []string{"Details", "Details_DataStorage"},
		},
		{
			name: "github source zip wrapper",
			entries: []entry{
				{name: "repo-1.2/"},
				{name: "repo-1.2/README.md"},
				{name: "repo-1.2/Addon/Addon.toc"},
				{name: "repo-1.2/Addon_Options/Addon_Options.toc"},
			},
			wantRoot:    "repo-1.2",
			wantFolders: []string{"Addon", "Addon_Options"},
		},
		{
			name: "nested wrappers",
			entries: []entry{
				{name: "outer/inner/Addon/Addon.toc"},
			},
			wantRoot:    "outer/inner",
			wantFolders: []string{"Addon"},
		},
		{
			// A single folder with a TOC is the addon, not a wrapper.
			name: "single addon folder",
			entries: []entry{
				{name: "Addon/Addon.toc"},
				{name: "Addon/Libs/Lib.lua"},
			},
			wantRoot:    ".",
			wantFolders: []string{"Addon"},
		},
		{
			name: "folder entries only, no file entries for parents",
			entries: []entry{
				{name: "Addon/sub/x.lua"},
				{name: "Addon/Addon.toc"},
			},
			wantRoot:    ".",
			wantFolders: []string{"Addon"},
		},
		{
			name: "backslash separators",
			entries: []entry{
				{name: `Addon\Addon.toc`},
				{name: `Addon\Libs\Lib.lua`},
			},
			wantRoot:    ".",
			wantFolders: []string{"Addon"},
		},
		{
			name: "dot-slash prefix",
			entries: []entry{
				{name: "./"},
				{name: "./Addon/Addon.toc"},
			},
			wantRoot:    ".",
			wantFolders: []string{"Addon"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addons := t.TempDir()
			s, err := extract(context.Background(), writeZip(t, tt.entries...), addons)
			if err != nil {
				t.Fatal(err)
			}
			if filepath.Dir(s.dir) != addons || !strings.HasPrefix(filepath.Base(s.dir), stagingPrefix) {
				t.Errorf("staging folder %s isn't a %s* folder in AddOns", s.dir, stagingPrefix)
			}
			root, err := filepath.Rel(s.dir, s.root)
			if err != nil {
				t.Fatal(err)
			}
			if got := filepath.ToSlash(root); got != tt.wantRoot {
				t.Errorf("root = %s, want %s", got, tt.wantRoot)
			}
			if diff := cmp.Diff(tt.wantFolders, s.folders); diff != "" {
				t.Errorf("folders (-want +got):\n%s", diff)
			}
		})
	}
}

// Contents are extracted byte for byte, with backslashes becoming folders.
func TestExtractContents(t *testing.T) {
	t.Parallel()
	s, err := extract(context.Background(), writeZip(t,
		entry{name: "Addon/Addon.toc", body: "## Interface: 16001\n"},
		entry{name: `Addon\Libs\Lib.lua`, body: "-- lib"},
	), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Addon/Addon.toc", "Addon/Libs/Lib.lua"}, files(t, s.dir)); diff != "" {
		t.Errorf("files (-want +got):\n%s", diff)
	}
	got, err := os.ReadFile(filepath.Join(s.root, "Addon", "Addon.toc"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "## Interface: 16001\n" {
		t.Errorf("Addon.toc = %q", got)
	}
}

// Each rejection: the error wraps ErrBadArchive and no staging folder is
// left in AddOns.
func TestExtractRejects(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		entries []entry
		want    string // substring of the error
	}{
		{"zip slip", []entry{{name: "Addon/Addon.toc"}, {name: "../evil.lua"}}, "escapes the archive"},
		{"zip slip inside a folder", []entry{{name: "Addon/../../evil.lua"}}, "escapes the archive"},
		{"zip slip with backslashes", []entry{{name: `Addon\..\..\evil.lua`}}, "escapes the archive"},
		{"absolute path", []entry{{name: "/etc/evil"}}, "is an absolute path"},
		{"absolute backslash path", []entry{{name: `\Windows\evil`}}, "is an absolute path"},
		{"drive letter", []entry{{name: "C:/Windows/evil"}}, "is an absolute path"},
		{"symlink", []entry{{name: "Addon/Addon.toc"}, {name: "Addon/link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}}, "is a symlink"},
		{"named pipe", []entry{{name: "Addon/fifo", mode: fs.ModeNamedPipe | 0o644}}, "is a named pipe"},
		{"duplicate entry", []entry{{name: "Addon/a.lua"}, {name: "Addon/a.lua"}}, "appears twice"},
		{"case collision", []entry{{name: "Addon/A.lua"}, {name: "Addon/a.lua"}}, "are the same file on Windows and macOS"},
		{"empty archive", nil, "no addon folders"},
		{"files only", []entry{{name: "README.md"}}, "no addon folders"},
		{"toc at top level", []entry{{name: "Addon.toc"}, {name: "Libs/Lib.lua"}}, "Addon.toc isn't in an addon folder"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addons := t.TempDir()
			_, err := extract(context.Background(), writeZip(t, tt.entries...), addons)
			if !errors.Is(err, ErrBadArchive) || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("extract error = %v, want ErrBadArchive containing %q", err, tt.want)
			}
			assertEmpty(t, addons)
		})
	}
}

func TestExtractNotAZip(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "addon.zip")
	if err := os.WriteFile(path, []byte("<html>rate limited</html>"), 0o600); err != nil {
		t.Fatal(err)
	}
	addons := t.TempDir()
	if _, err := extract(context.Background(), path, addons); !errors.Is(err, ErrBadArchive) {
		t.Errorf("extract error = %v, want ErrBadArchive", err)
	}
	assertEmpty(t, addons)
}

// The size limit counts bytes actually written, not sizes the archive
// claims, and applies across all files.
func TestExtractSizeLimit(t *testing.T) {
	t.Parallel()
	path := writeZip(t,
		entry{name: "Addon/Addon.toc", body: strings.Repeat("a", 60)},
		entry{name: "Addon/big.lua", body: strings.Repeat("b", 60)},
	)
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()

	if _, err := extractInto(context.Background(), &zr.Reader, t.TempDir(), 120); err != nil {
		t.Errorf("extract at exactly the limit: %v", err)
	}
	if _, err := extractInto(context.Background(), &zr.Reader, t.TempDir(), 119); !errors.Is(err, ErrBadArchive) {
		t.Errorf("extract over the limit = %v, want ErrBadArchive", err)
	}
}

func TestExtractCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	addons := t.TempDir()
	if _, err := extract(ctx, writeZip(t, entry{name: "Addon/Addon.toc"}), addons); !errors.Is(err, context.Canceled) {
		t.Errorf("extract error = %v, want context.Canceled", err)
	}
	assertEmpty(t, addons)
}

// Names Windows can't hold are rejected there, so an archive never
// extracts differently than the client would see it.
func TestExtractWindowsNames(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "windows" {
		t.Skip("filepath.Localize only rejects these names on Windows")
	}
	for _, name := range []string{"Addon/NUL", "Addon/a:b.lua"} {
		_, err := extract(context.Background(), writeZip(t, entry{name: name}), t.TempDir())
		if !errors.Is(err, ErrBadArchive) {
			t.Errorf("extract %q = %v, want ErrBadArchive", name, err)
		}
	}
}

func assertEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("AddOns isn't empty after a failed extract: %v", entries)
	}
}
