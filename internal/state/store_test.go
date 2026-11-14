package state_test

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/state"
)

var update = flag.Bool("update", false, "rewrite testdata/state.golden")

// newStore returns a Store keeping state in a temp directory, and that
// directory.
func newStore(t *testing.T) (*state.Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "state")
	return state.NewStore(func(p string) string { return filepath.Join(dir, p+".toml") }), dir
}

func mustRef(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// sample is a State with two addons, one owning several folders.
func sample(t *testing.T) *state.State {
	t.Helper()
	st := state.New()
	st.Addons[mustRef(t, "github:austinrr/baganator-mirror")] = state.Addon{
		Version:     "851",
		SHA256:      strings.Repeat("d3", 32),
		InstalledAt: time.Date(2026, 11, 13, 18, 4, 5, 0, time.UTC),
		Folders:     []string{"Baganator"},
	}
	st.Addons[mustRef(t, "wago:VBNBxKx5")] = state.Addon{
		Version:     "11.2.0",
		SHA256:      strings.Repeat("1a", 32),
		InstalledAt: time.Date(2026, 11, 13, 18, 4, 9, 0, time.UTC),
		Folders:     []string{"Details", "Details_DataStorage", "Details_EncounterDetails"},
	}
	return st
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	want := sample(t)
	if err := s.Save("forever", want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("forever")
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("state after round trip (-want +got):\n%s", diff)
	}
}

// Save writes in UTC to the second, whatever zone the time was in.
func TestSaveNormalizesTime(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	st := sample(t)
	r := mustRef(t, "wago:VBNBxKx5")
	a := st.Addons[r]
	a.InstalledAt = time.Date(2026, 11, 13, 13, 4, 9, 999, time.FixedZone("EST", -5*60*60))
	st.Addons[r] = a
	if err := s.Save("forever", st); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load("forever")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 11, 13, 18, 4, 9, 0, time.UTC); !got.Addons[r].InstalledAt.Equal(want) || got.Addons[r].InstalledAt.Location() != time.UTC {
		t.Errorf("InstalledAt = %v, want %v", got.Addons[r].InstalledAt, want)
	}
}

func TestGolden(t *testing.T) {
	t.Parallel()
	got := state.Marshal(sample(t))
	path := filepath.Join("testdata", "state.golden")
	if *update {
		if err := os.WriteFile(path, got, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(want), string(got)); diff != "" {
		t.Errorf("Marshal differs from %s (-want +got):\n%s", path, diff)
	}
}

func TestLoadMissing(t *testing.T) {
	t.Parallel()
	s, _ := newStore(t)
	got, err := s.Load("forever")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Addons) != 0 {
		t.Errorf("Load of a missing file = %v, want empty", got.Addons)
	}
}

// Every way a state file can be wrong gets an error naming the problem.
func TestLoadErrors(t *testing.T) {
	t.Parallel()
	const addon = `[addons."github:a/b"]
version = "1"
sha256 = "` + "d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3d3" + `"
installed_at = 2026-11-13T18:04:05Z
`
	tests := []struct {
		name string
		file string
		want string // substring of the error
	}{
		{"newer schema", "version = 2\n", "schema version 2 is newer than this knapsack supports (1); upgrade knapsack"},
		{"no schema version", addon + `folders = ["B"]` + "\n", "no schema version"},
		{"negative schema", "version = -1\n", "schema version -1 isn't valid"},
		{"not toml", "version = \n", "expected value"},
		{"unknown key", "version = 1\n" + addon + "folders = [\"B\"]\ntitle = \"B\"\n", `unknown keys: addons."github:a/b".title`},
		{"bad ref", "version = 1\n[addons.\"svn:x\"]\n", `unknown source "svn"`},
		{"ref not normal", "version = 1\n" + strings.Replace(addon, "github:a/b", "github:A/B", 1) + "folders = [\"B\"]\n", `"github:A/B": ref isn't in normal form (want "github:a/b")`},
		{"bad sha", "version = 1\n" + strings.Replace(addon, "d3d3", "D3D3", 1) + "folders = [\"B\"]\n", "isn't 64 lowercase hex digits"},
		{"no folders", "version = 1\n" + addon, "owns no folders"},
		{"parent folder", "version = 1\n" + addon + "folders = [\"..\"]\n", `folder name ".." isn't allowed`},
		{"path folder", "version = 1\n" + addon + "folders = [\"../Other\"]\n", "must be a plain name"},
		{"trailing dot", "version = 1\n" + addon + "folders = [\"Baganator.\"]\n", "can't end in a dot or space"},
		{"trailing space", "version = 1\n" + addon + "folders = [\"Baganator \"]\n", "can't end in a dot or space"},
		{"device name", "version = 1\n" + addon + "folders = [\"nul\"]\n", "is a Windows device name"},
		{"device name with extension", "version = 1\n" + addon + "folders = [\"COM1.txt\"]\n", "is a Windows device name"},
		{"year out of range", "version = 1\n" + strings.Replace(addon, "2026-11-13", "0000-01-01", 1) + "folders = [\"B\"]\n", "installed_at year 0 is out of range"},
		{"windows path folder", "version = 1\n" + addon + "folders = [\"C:\\\\x\"]\n", "must be a plain name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, dir := newStore(t)
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "forever.toml"), []byte(tt.file), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := s.Load("forever")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Load error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

// Two addons can't own the same folder, even in different case.
func TestValidateSharedFolder(t *testing.T) {
	t.Parallel()
	st := sample(t)
	st.Addons[mustRef(t, "github:a/b")] = state.Addon{
		SHA256:      strings.Repeat("ab", 32),
		InstalledAt: time.Now(),
		Folders:     []string{"baganator"},
	}
	err := st.Validate()
	if err == nil || !strings.Contains(err.Error(), `folder "Baganator" is also owned by github:a/b`) {
		t.Errorf("Validate = %v, want a shared-folder error", err)
	}
	s, _ := newStore(t)
	if err := s.Save("forever", st); err == nil {
		t.Error("Save accepted an invalid state")
	}
}

func TestOwner(t *testing.T) {
	t.Parallel()
	st := sample(t)
	if r, ok := st.Owner("details_datastorage"); !ok || r != mustRef(t, "wago:VBNBxKx5") {
		t.Errorf("Owner(details_datastorage) = %v, %v; want wago:VBNBxKx5", r, ok)
	}
	if r, ok := st.Owner("WeakAuras"); ok {
		t.Errorf("Owner(WeakAuras) = %v, want none", r)
	}
}

func TestDelete(t *testing.T) {
	t.Parallel()
	s, dir := newStore(t)
	if err := s.Save("forever", sample(t)); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("forever"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "forever.toml")); !os.IsNotExist(err) {
		t.Errorf("state still exists after Delete: %v", err)
	}
	if err := s.Delete("forever"); err != nil {
		t.Errorf("Delete of missing state: %v", err)
	}
}

// Names that only look like device names are fine.
func TestValidateFolderNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"Console", "COM10", "LPT0", "NULL", "Auxiliary", "Details_DataStorage", ".hidden"} {
		st := state.New()
		st.Addons[mustRef(t, "github:a/b")] = state.Addon{
			SHA256:      strings.Repeat("ab", 32),
			InstalledAt: time.Now(),
			Folders:     []string{name},
		}
		if err := st.Validate(); err != nil {
			t.Errorf("folder %q: %v", name, err)
		}
	}
}

// A year TOML can't represent is rejected before it's written, so Save
// never produces a file Load can't read.
func TestValidateYear(t *testing.T) {
	t.Parallel()
	st := sample(t)
	r := mustRef(t, "wago:VBNBxKx5")
	a := st.Addons[r]
	a.InstalledAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	st.Addons[r] = a
	s, _ := newStore(t)
	if err := s.Save("forever", st); err == nil || !strings.Contains(err.Error(), "year 10000 is out of range") {
		t.Errorf("Save = %v, want a year error", err)
	}
}
