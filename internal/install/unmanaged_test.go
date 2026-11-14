package install

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestUnmanaged(t *testing.T) {
	t.Parallel()
	addons := t.TempDir()
	for _, d := range []string{"Owned", "details", "Hand", "another", ".git", trashPrefix + "1", stagingPrefix + "2"} {
		if err := os.Mkdir(filepath.Join(addons, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(addons, "notes.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want := []string{"another", "Hand"}
	// An author's working copy linked into AddOns. Making a symlink can
	// need privileges on Windows, so it's only checked where it works.
	if err := os.Symlink(t.TempDir(), filepath.Join(addons, "Linked")); err == nil {
		want = append(want, "Linked")
	}
	_ = os.Symlink(filepath.Join(addons, "gone"), filepath.Join(addons, "Broken"))

	// An owned "Details" is the "details" folder only where the
	// filesystem ignores case; elsewhere it's another folder.
	if _, err := os.Stat(filepath.Join(addons, "DETAILS")); err != nil {
		want = append(want, "details")
		slices.SortFunc(want, func(a, b string) int { return strings.Compare(strings.ToLower(a), strings.ToLower(b)) })
	}
	got, err := Unmanaged(addons, []string{"Owned", "Details", "NotOnDisk"})
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Unmanaged (-want +got):\n%s", diff)
	}
}

func TestUnmanagedMissingDir(t *testing.T) {
	t.Parallel()
	got, err := Unmanaged(filepath.Join(t.TempDir(), "nope"), nil)
	if err != nil || got != nil {
		t.Errorf("Unmanaged of a missing folder = %v, %v; want nil, nil", got, err)
	}
}
