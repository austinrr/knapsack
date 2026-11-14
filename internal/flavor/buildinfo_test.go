package flavor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/austinrr/knapsack/internal/flavor"
)

// newWoW makes a WoW folder holding testdata/build.info, a copy of a real
// install's file with a second, made-up row for retail, and returns it.
func newWoW(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "build.info"))
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "World of Warcraft")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".build.info"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestClientInterface(t *testing.T) {
	t.Parallel()
	root := newWoW(t)
	addons := func(folder string) string {
		return filepath.Join(root, folder, "Interface", "AddOns")
	}
	tests := []struct {
		name   string
		dir    string
		flavor flavor.Flavor
		want   int
		wantOK bool
	}{
		{"forever", addons("_forever_"), flavor.Forever, 16001, true},
		{"retail", addons("_retail_"), flavor.Retail, 120105, true},
		// Folder names are matched without regard to case.
		{"folder case", addons("_Forever_"), flavor.Forever, 16001, true},
		{"trailing separator", addons("_forever_") + string(filepath.Separator), flavor.Forever, 16001, true},
		// The file has no row for wow_classic.
		{"product not installed", addons("_classic_"), flavor.Classic, 0, false},
		// Anniversary's product code isn't known yet (DESIGN.md §13).
		{"unknown product", addons("_anniversary_"), flavor.Anniversary, 0, false},
		// Without the client folder in the path, the WoW folder can't be found.
		{"no client folder", filepath.Join(root, "AddOns"), flavor.Forever, 0, false},
		// The path names a different client than the flavor.
		{"other client folder", addons("_retail_"), flavor.Forever, 0, false},
		{"unknown flavor", addons("_forever_"), flavor.Flavor("ptr"), 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, ok := flavor.ClientInterface(tt.dir, tt.flavor)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("ClientInterface(%q, %q) = %d, %v; want %d, %v", tt.dir, tt.flavor, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestClientInterfaceMissingFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "_forever_", "Interface", "AddOns")
	if got, ok := flavor.ClientInterface(dir, flavor.Forever); ok {
		t.Errorf("ClientInterface without .build.info = %d, true; want false", got)
	}
}

func TestKnownGameType(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"standard", "forever", "camelot", "mists", "cata", "wrath", "tbc", "vanilla", "mainline", "classic"} {
		if !flavor.KnownGameType(name) {
			t.Errorf("KnownGameType(%q) = false, want true", name)
		}
	}
	// Every client in the table, with its aliases, must be recognized, or validation would
	// ignore AllowLoadGameType lines naming it.
	for _, f := range flavor.All() {
		c, _ := f.Client()
		for _, name := range append(c.GameTypes(), string(c.Family)) {
			if !flavor.KnownGameType(name) {
				t.Errorf("%s: game type or family %q isn't known", f, name)
			}
		}
	}
	for _, name := range []string{"Mainline", "retail", ""} {
		if flavor.KnownGameType(name) {
			t.Errorf("KnownGameType(%q) = true, want false", name)
		}
	}
}

// A version outside the flavor's range means the table or product code is
// wrong, so it's not trusted for the out-of-date check.
func TestClientInterfaceOutOfRange(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "World of Warcraft")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	info := "Active!DEC:1|Version!STRING:0|Product!STRING:0\n1|6.0.1.1|wow_classic\n"
	if err := os.WriteFile(filepath.Join(root, ".build.info"), []byte(info), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "_classic_", "Interface", "AddOns")
	if got, ok := flavor.ClientInterface(dir, flavor.Classic); ok {
		t.Errorf("ClientInterface = %d, true; want false for 60001 on classic", got)
	}
}
