package install

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/flavor"
)

// tree maps every file under dir, as a slash-separated relative path, to
// its contents. Comparing trees shows exactly what an operation changed.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(data)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// writeTree creates files (slash-separated path → contents) under dir.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// addonZip builds a release zip with one valid Forever addon folder per
// name, each holding a TOC and a Core.lua whose contents are version.
func addonZip(t *testing.T, version string, folders ...string) string {
	t.Helper()
	var entries []entry
	for _, f := range folders {
		entries = append(entries,
			entry{name: f + "/" + f + ".toc", body: "## Interface: 16001\n"},
			entry{name: f + "/Core.lua", body: version},
		)
	}
	return writeZip(t, entries...)
}

// installed is the tree an addonZip leaves in AddOns.
func installed(version string, folders ...string) map[string]string {
	out := map[string]string{}
	for _, f := range folders {
		out[f+"/"+f+".toc"] = "## Interface: 16001\n"
		out[f+"/Core.lua"] = version
	}
	return out
}

func doInstall(t *testing.T, archive, addons string, opts Options) ([]string, []string, error) {
	t.Helper()
	if opts.Flavor == "" {
		opts.Flavor = flavor.Forever
	}
	return Install(context.Background(), archive, addons, opts)
}

// assertNoWorkFolders checks that nothing Knapsack works in is left.
func assertNoWorkFolders(t *testing.T, addons string) {
	t.Helper()
	entries, err := os.ReadDir(addons)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if isWorkFolder(e.Name()) {
			t.Errorf("left %s in AddOns", e.Name())
		}
	}
}

func TestInstallFresh(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	writeTree(t, addons, map[string]string{"Other/Other.toc": "x"})

	folders, warnings, err := doInstall(t, addonZip(t, "v1", "Details", "Details_DataStorage"), addons, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Details", "Details_DataStorage"}, folders); diff != "" {
		t.Errorf("folders (-want +got):\n%s", diff)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none", warnings)
	}
	want := installed("v1", "Details", "Details_DataStorage")
	want["Other/Other.toc"] = "x"
	if diff := cmp.Diff(want, tree(t, addons)); diff != "" {
		t.Errorf("AddOns (-want +got):\n%s", diff)
	}
	assertNoWorkFolders(t, addons)
}

// An update replaces the addon's folders, adds new ones and removes ones
// the new release dropped.
func TestInstallUpdate(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	if _, _, err := doInstall(t, addonZip(t, "v1", "Details", "Details_Old"), addons, Options{}); err != nil {
		t.Fatal(err)
	}
	folders, _, err := doInstall(t, addonZip(t, "v2", "Details", "Details_New"), addons,
		Options{Previous: []string{"Details", "Details_Old"}})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff([]string{"Details", "Details_New"}, folders); diff != "" {
		t.Errorf("folders (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(installed("v2", "Details", "Details_New"), tree(t, addons)); diff != "" {
		t.Errorf("AddOns (-want +got):\n%s", diff)
	}
	assertNoWorkFolders(t, addons)
}

// A folder that exists but the addon didn't own is refused, and AddOns is
// unchanged, unless Force is set. Names match without regard to case.
func TestInstallConflict(t *testing.T) {
	t.Parallel()
	for _, existing := range []string{"Baganator", "baganator"} {
		t.Run(existing, func(t *testing.T) {
			t.Parallel()
			addons := t.TempDir()
			before := map[string]string{existing + "/" + existing + ".toc": "hand-installed"}
			writeTree(t, addons, before)
			archive := addonZip(t, "v1", "Baganator")

			_, _, err := doInstall(t, archive, addons, Options{})
			if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), existing) {
				t.Fatalf("Install error = %v, want ErrConflict naming %s", err, existing)
			}
			if diff := cmp.Diff(before, tree(t, addons)); diff != "" {
				t.Errorf("AddOns changed after a conflict (-want +got):\n%s", diff)
			}
			assertNoWorkFolders(t, addons)

			if _, _, err := doInstall(t, archive, addons, Options{Force: true}); err != nil {
				t.Fatalf("Install with Force: %v", err)
			}
			if diff := cmp.Diff(installed("v1", "Baganator"), tree(t, addons)); diff != "" {
				t.Errorf("AddOns after Force (-want +got):\n%s", diff)
			}
		})
	}
}

// A folder that fails validation stops the install before anything moves.
func TestInstallInvalid(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	archive := writeZip(t,
		entry{name: "Good/Good.toc", body: "## Interface: 16001\n"},
		entry{name: "Retail/Retail.toc", body: "## Interface: 120105\n"},
	)
	_, _, err := doInstall(t, archive, addons, Options{})
	if !errors.Is(err, ErrWrongGame) {
		t.Fatalf("Install error = %v, want ErrWrongGame", err)
	}
	if got := tree(t, addons); len(got) != 0 {
		t.Errorf("AddOns isn't empty: %v", got)
	}
	assertNoWorkFolders(t, addons)
}

func TestInstallWarnings(t *testing.T) {
	t.Parallel()
	_, warnings, err := doInstall(t, addonZip(t, "v1", "Addon"), t.TempDir(), Options{ClientInterface: 16002})
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Addon is out of date") {
		t.Errorf("warnings = %q, want one out-of-date warning", warnings)
	}
}

func TestInstallCancelled(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	addons := t.TempDir()
	_, _, err := Install(ctx, addonZip(t, "v1", "Addon"), addons, Options{Flavor: flavor.Forever})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Install error = %v, want context.Canceled", err)
	}
	if got := tree(t, addons); len(got) != 0 {
		t.Errorf("AddOns isn't empty: %v", got)
	}
}

// failingRename fails the nth call and, if rollbackFails, every call after
// it.
func failingRename(n int, rollbackFails bool) func(string, string) error {
	calls := 0
	return func(oldpath, newpath string) error {
		calls++
		if calls == n || (rollbackFails && calls > n) {
			return fmt.Errorf("injected failure on rename %d", calls)
		}
		return os.Rename(oldpath, newpath)
	}
}

// stageUpdate sets up AddOns with v1 of an addon in A and B (plus an
// unrelated folder) and a staged v2 in A and C, and returns AddOns, the
// staged root, and the tree before. The swap moves A and B out, then A and
// C in.
func stageUpdate(t *testing.T) (addons, staged string, before map[string]string) {
	t.Helper()
	addons = t.TempDir()
	writeTree(t, addons, installed("v1", "A", "B", "Unrelated"))
	staged = filepath.Join(addons, stagingPrefix+"test")
	writeTree(t, staged, installed("v2", "A", "C"))
	return addons, staged, tree(t, addons)
}

// A failure at any point in the swap rolls back every move, leaving AddOns
// as it was.
func TestSwapRollback(t *testing.T) {
	t.Parallel()
	const moves = 4 // two out, two in
	for n := 1; n <= moves; n++ {
		t.Run(fmt.Sprintf("fail move %d", n), func(t *testing.T) {
			t.Parallel()
			addons, staged, before := stageUpdate(t)
			err := swap(addons, staged, []string{"A", "C"}, []string{"A", "B"}, failingRename(n, false))
			if err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("swap error = %v, want the injected failure", err)
			}
			if diff := cmp.Diff(before, tree(t, addons)); diff != "" {
				t.Errorf("AddOns after rollback (-want +got):\n%s", diff)
			}
			for _, e := range mustReadDir(t, addons) {
				if strings.HasPrefix(e.Name(), backupPrefix) {
					t.Errorf("backup %s left after a full rollback", e.Name())
				}
			}
		})
	}
}

// If rollback fails too, the backup is kept and named in the error, and
// Cleanup later puts back every old folder that wasn't replaced.
func TestSwapRollbackFailsThenCleanup(t *testing.T) {
	t.Parallel()
	addons, staged, _ := stageUpdate(t)
	// Moves 1 and 2 take A and B out; move 3 (A in) fails, and so does
	// every rollback move, leaving A and B only in the backup.
	err := swap(addons, staged, []string{"A", "C"}, []string{"A", "B"}, failingRename(3, true))
	if err == nil || !strings.Contains(err.Error(), "rolling back also failed") || !strings.Contains(err.Error(), backupPrefix) {
		t.Fatalf("swap error = %v, want it to name the kept backup", err)
	}
	if err := Cleanup(addons); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(installed("v1", "A", "B", "Unrelated"), tree(t, addons)); diff != "" {
		t.Errorf("AddOns after Cleanup (-want +got):\n%s", diff)
	}
	assertNoWorkFolders(t, addons)
}

// Cleanup deletes staging and trash, and restores every backed-up folder,
// replacing a new one already in its place.
func TestCleanup(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	writeTree(t, addons, map[string]string{
		"A/Core.lua":                    "new", // moved in before the swap stopped, so it's undone
		stagingPrefix + "1/X/Core.lua":  "staged",
		trashPrefix + "2/Old/Core.lua":  "trash",
		backupPrefix + "3/A/Core.lua":   "old",
		backupPrefix + "3/B/Core.lua":   "old", // missing, so it's restored
		backupPrefix + "4/C/Core.lua":   "old",
		"NotOurs/.knapsack-staging-not": "x", // only top-level names count
	})
	if err := Cleanup(addons); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"A/Core.lua":                    "old",
		"B/Core.lua":                    "old",
		"C/Core.lua":                    "old",
		"NotOurs/.knapsack-staging-not": "x",
	}
	if diff := cmp.Diff(want, tree(t, addons)); diff != "" {
		t.Errorf("AddOns after Cleanup (-want +got):\n%s", diff)
	}
	assertNoWorkFolders(t, addons)
}

func TestRemove(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	writeTree(t, addons, installed("v1", "Details", "Details_DataStorage", "Other"))
	// Case doesn't matter, and folders already gone are skipped.
	if err := Remove(addons, []string{"details", "Details_DataStorage", "Gone"}); err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(installed("v1", "Other"), tree(t, addons)); diff != "" {
		t.Errorf("AddOns after Remove (-want +got):\n%s", diff)
	}
	assertNoWorkFolders(t, addons)
	if err := Remove(addons, nil); err != nil {
		t.Errorf("Remove of nothing: %v", err)
	}
}

// Remove never touches anything outside AddOns, whatever state says.
func TestRemoveRejectsPaths(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	for _, f := range []string{"..", "../Other", `..\Other`, "A/B", "", "/abs"} {
		if err := Remove(addons, []string{f}); err == nil {
			t.Errorf("Remove(%q) succeeded", f)
		}
	}
}

func mustReadDir(t *testing.T, dir string) []fs.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
