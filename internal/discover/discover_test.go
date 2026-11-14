package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCandidates(t *testing.T) {
	t.Parallel()
	win := Candidates("windows", `C:\Users\me`, []string{`C:\`, `D:\`})
	for _, want := range []string{
		filepath.Join(`C:\`, "Program Files (x86)", "World of Warcraft"),
		filepath.Join(`D:\`, "Games", "World of Warcraft"),
		filepath.Join(`D:\`, "World of Warcraft"),
	} {
		if !slices.Contains(win, want) {
			t.Errorf("windows candidates lack %s", want)
		}
	}
	mac := Candidates("darwin", "/Users/me", nil)
	if !slices.Contains(mac, filepath.Join("/Applications", "World of Warcraft")) {
		t.Errorf("darwin candidates = %v, want /Applications/World of Warcraft", mac)
	}
	linux := Candidates("linux", "/home/me", nil)
	for _, want := range []string{
		filepath.Join("/home/me", ".wine", "drive_c", "Program Files (x86)", "World of Warcraft"),
		filepath.Join("/home/me", "*", "*", "drive_c", "Program Files (x86)", "World of Warcraft"),
	} {
		if !slices.Contains(linux, want) {
			t.Errorf("linux candidates lack %s", want)
		}
	}
	if got := Candidates("linux", "", nil); len(got) != 0 {
		t.Errorf("linux candidates with no home = %v, want none", got)
	}
}

// Find expands patterns, keeps folders with client folders, and lists each
// once; Clients lists them in flavor's order.
func TestFind(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	faugus := filepath.Join(home, "Faugus", "battlenet", "drive_c", "Program Files (x86)", "World of Warcraft")
	lutris := filepath.Join(home, "Games", "wow", "drive_c", "Program Files (x86)", "World of Warcraft")
	empty := filepath.Join(home, "Games", "other", "drive_c", "Program Files (x86)", "World of Warcraft")
	mkdirs(t,
		filepath.Join(faugus, "_forever_"), filepath.Join(faugus, "_retail_"), filepath.Join(faugus, "_ptr_"), filepath.Join(faugus, "Cache"),
		filepath.Join(lutris, "_classic_era_"),
		filepath.Join(empty, "Data"), // no client folders
	)
	got := Find(append(Candidates("linux", home, nil), faugus)) // faugus twice
	want := []string{faugus, lutris}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Find (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"_retail_", "_ptr_", "_forever_"}, Clients(faugus)); diff != "" {
		t.Errorf("Clients (-want +got):\n%s", diff)
	}
	if got := Clients(filepath.Join(home, "missing")); got != nil {
		t.Errorf("Clients of a missing folder = %v", got)
	}
}

func TestDrives(t *testing.T) {
	t.Parallel()
	got := Drives()
	if runtime.GOOS != "windows" && len(got) != 0 {
		t.Errorf("Drives on %s = %v, want none", runtime.GOOS, got)
	}
	if runtime.GOOS == "windows" && !slices.Contains(got, `C:\`) {
		t.Errorf("Drives = %v, want C:\\ at least", got)
	}
}
