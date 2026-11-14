package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestAssetName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "knapsack_0.2.0_linux_amd64.tar.gz"},
		{"darwin", "arm64", "knapsack_0.2.0_darwin_arm64.tar.gz"},
		{"windows", "amd64", "knapsack_0.2.0_windows_amd64.zip"},
	} {
		if got := AssetName("v0.2.0", tc.goos, tc.goarch); got != tc.want {
			t.Errorf("AssetName(%s, %s) = %s, want %s", tc.goos, tc.goarch, got, tc.want)
		}
	}
}

func TestNewer(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"v0.2.0", "v0.1.0", true},
		{"v0.1.0", "v0.2.0", false},
		{"v0.1.0", "v0.1.0", false},
		{"v1.0.0", "v0.99.99", true},
		{"v0.10.0", "v0.9.0", true}, // numbers, not strings
		{"v0.1.1", "v0.1.0", true},
		{"v0.1.0", "v0.1.0-rc1", true}, // a release is newer than its pre-release
		{"v0.1.0-rc1", "v0.1.0", false},
		{"v0.1.0-rc2", "v0.1.0-rc1", false}, // suffixes aren't compared
		{"v0.2.0", "dev", false},
		{"dev", "v0.1.0", false},
		{"", "v0.1.0", false},
		{"0.2.0", "v0.1.0", false},
		{"v0.2", "v0.1.0", false},
		{"v0.2.x", "v0.1.0", false},
	} {
		if got := Newer(tc.a, tc.b); got != tc.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestPackageManager(t *testing.T) {
	t.Parallel()
	for exe, want := range map[string]string{
		"/opt/homebrew/bin/knapsack":                                                  "Homebrew",
		"/usr/local/Cellar/knapsack/0.1.0/bin/knapsack":                               "Homebrew",
		"/home/linuxbrew/.linuxbrew/bin/knapsack":                                     "Homebrew",
		`C:\Users\me\scoop\apps\knapsack\current\knapsack.exe`:                        "Scoop",
		`C:\Users\me\AppData\Local\Microsoft\WinGet\Packages\knapsack_x\knapsack.exe`: "winget",
		"/nix/store/abc-knapsack-0.1.0/bin/knapsack":                                  "Nix",
		"/usr/bin/knapsack":                                                           "your system's package manager",
		"/home/me/.local/bin/knapsack":                                                "",
		"/usr/local/bin/knapsack":                                                     "",
		`C:\Users\me\bin\knapsack.exe`:                                                "",
	} {
		if got := PackageManager(exe); got != want {
			t.Errorf("PackageManager(%s) = %q, want %q", exe, got, want)
		}
	}
}

// archive builds a release archive holding files.
func archive(t *testing.T, isZip bool, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	if isZip {
		zw := zip.NewWriter(&buf)
		for name, body := range files {
			w, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte(body))
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for name, body := range files {
			if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			_, _ = tw.Write([]byte(body))
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(t.TempDir(), "download-1.zip") // as httpx names it
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtract(t *testing.T) {
	t.Parallel()
	for _, isZip := range []bool{false, true} {
		files := map[string]string{"LICENSE": "MIT", "README.md": "hi", "knapsack": "binary", "docs/knapsack": "not this one"}
		var out bytes.Buffer
		if err := extract(archive(t, isZip, files), "knapsack", isZip, &out); err != nil || out.String() != "binary" {
			t.Errorf("extract (zip %v) = %q, %v; want the root binary", isZip, out.String(), err)
		}
		delete(files, "knapsack")
		if err := extract(archive(t, isZip, files), "knapsack", isZip, &out); !errors.Is(err, ErrNoBinary) {
			t.Errorf("extract (zip %v) without a binary = %v, want ErrNoBinary", isZip, err)
		}
	}
	// Not an archive at all.
	bad := filepath.Join(t.TempDir(), "bad")
	_ = os.WriteFile(bad, []byte("nope"), 0o600)
	if err := extract(bad, "knapsack", false, &bytes.Buffer{}); err == nil {
		t.Error("extract of a non-archive succeeded")
	}
}

func TestReplace(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe := filepath.Join(dir, binaryName())
	if err := os.WriteFile(exe, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Replace(exe, archive(t, runtime.GOOS == "windows", map[string]string{binaryName(): "new"})); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Errorf("binary = %q, want new", got)
	}
	if info, _ := os.Stat(exe); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want the old one, 0600", info.Mode().Perm())
	}

	// A broken archive leaves the binary, and nothing else in its folder.
	if err := Replace(exe, archive(t, runtime.GOOS == "windows", map[string]string{"LICENSE": "MIT"})); !errors.Is(err, ErrNoBinary) {
		t.Errorf("Replace with no binary = %v, want ErrNoBinary", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "new" {
		t.Errorf("binary after a failed Replace = %q, want it unchanged", got)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("folder has %d entries after a failed Replace, want just the binary", len(entries))
	}
}

// Both ways of swapping work on any OS; Windows' is checked here too.
func TestSwap(t *testing.T) {
	t.Parallel()
	for _, windows := range []bool{false, true} {
		dir := t.TempDir()
		exe, next := filepath.Join(dir, "knapsack"), filepath.Join(dir, "next")
		_ = os.WriteFile(exe, []byte("old"), 0o600)
		_ = os.WriteFile(next, []byte("new"), 0o600)
		if err := swap(exe, next, windows); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.ReadFile(exe); string(got) != "new" {
			t.Errorf("windows=%v: binary = %q, want new", windows, got)
		}
		if _, err := os.Stat(OldPath(exe)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("windows=%v: old binary left behind (%v)", windows, err)
		}
		// When the new binary can't be moved in, the old one is put back.
		if err := swap(exe, filepath.Join(dir, "missing"), true); err == nil {
			t.Error("swap of a missing file succeeded")
		}
		if got, _ := os.ReadFile(exe); string(got) != "new" {
			t.Errorf("windows=%v: binary after a failed swap = %q, want it restored", windows, got)
		}
	}
}

// On Windows, an old binary that's still running can't be deleted. A
// folder that isn't empty can't either, which stands in for it here.
func TestSwapOldInUse(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	exe, next := filepath.Join(dir, "knapsack.exe"), filepath.Join(dir, "next")
	_ = os.WriteFile(exe, []byte("old"), 0o600)
	_ = os.WriteFile(next, []byte("new"), 0o600)
	if err := os.MkdirAll(filepath.Join(OldPath(exe), "busy"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := swap(exe, next, true); !errors.Is(err, ErrOldInUse) {
		t.Errorf("swap = %v, want ErrOldInUse", err)
	}
	if got, _ := os.ReadFile(exe); string(got) != "old" {
		t.Errorf("binary = %q, want it untouched", got)
	}
}

func TestRemoveOld(t *testing.T) {
	t.Parallel()
	exe := filepath.Join(t.TempDir(), "knapsack.exe")
	_ = os.WriteFile(OldPath(exe), []byte("old"), 0o600)
	RemoveOld(exe)
	if _, err := os.Stat(OldPath(exe)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("old binary still there (%v)", err)
	}
	RemoveOld(exe) // nothing to remove is fine
}

// AssetName matches the archives a real release has (v0.1.0, saved for
// source/github's tests), for every platform .goreleaser.yaml builds, so
// a change to its name_template can't silently break self-update.
func TestAssetNameMatchesRelease(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("..", "source", "github", "testdata", "knapsack.json"))
	if err != nil {
		t.Fatal(err)
	}
	var rel struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(data, &rel); err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, a := range rel.Assets {
		names[a.Name] = true
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		for _, goarch := range []string{"amd64", "arm64"} {
			if name := AssetName(rel.Tag, goos, goarch); !names[name] {
				t.Errorf("release %s has no %s", rel.Tag, name)
			}
		}
	}
}
