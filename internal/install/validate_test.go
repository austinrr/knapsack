package install_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/install"
)

// addon makes an addon folder named Addon with the given files (name →
// contents) and returns its path.
func addon(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "Addon")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// One case per row of the validation table in DESIGN.md §6.4, plus the
// edges of each rule. All cases validate for Forever (game type forever,
// family mainline, range 16000–16999) unless they say otherwise.
func TestValidate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		files           map[string]string
		flavor          flavor.Flavor // Forever if empty
		clientInterface int
		wantErr         error  // nil for success
		wantWarning     string // substring; "" for no warning
	}{
		// Row 1: no TOC at all.
		{
			name:    "no toc",
			files:   map[string]string{"Addon.lua": ""},
			wantErr: install.ErrNotAddon,
		},
		{
			name:    "toc for another folder",
			files:   map[string]string{"Other.toc": "## Interface: 16001\n"},
			wantErr: install.ErrNotAddon,
		},
		{
			name:    "toc for another game type only",
			files:   map[string]string{"Addon_Mists.toc": "## Interface: 50504\n"},
			wantErr: install.ErrNotAddon,
		},

		// Row 2: load rules exclude this game type.
		{
			name:    "excluded by game type",
			files:   map[string]string{"Addon.toc": "## Interface: 16001\n## ExcludeLoadGameType: forever\n"},
			wantErr: install.ErrExcluded,
		},
		{
			name:    "excluded by family",
			files:   map[string]string{"Addon.toc": "## Interface: 16001\n## ExcludeLoadGameType: mainline\n"},
			wantErr: install.ErrExcluded,
		},
		{
			name:  "exclude names another game type",
			files: map[string]string{"Addon.toc": "## Interface: 16001\n## ExcludeLoadGameType: standard, classic\n"},
		},
		{
			name:    "allow names only other types",
			files:   map[string]string{"Addon.toc": "## Interface: 16001\n## AllowLoadGameType: standard, classic\n"},
			wantErr: install.ErrExcluded,
		},
		{
			name:  "allow names the family",
			files: map[string]string{"Addon.toc": "## Interface: 16001\n## AllowLoadGameType: Mainline\n"},
		},
		{
			name:  "allow names the game type among others",
			files: map[string]string{"Addon.toc": "## Interface: 16001\n## AllowLoadGameType: mists\n## AllowLoadGameType: forever\n"},
		},
		{
			// The client's quirk: if no listed type is recognized, the rule passes.
			name:  "allow names only unrecognized types",
			files: map[string]string{"Addon.toc": "## Interface: 16001\n## AllowLoadGameType: futuregame\n"},
		},
		{
			// Unrecognized names are ignored, so standard decides.
			name:    "allow names unrecognized and other types",
			files:   map[string]string{"Addon.toc": "## Interface: 16001\n## AllowLoadGameType: futuregame, standard\n"},
			wantErr: install.ErrExcluded,
		},

		// Row 3: no Interface value in range. The OpenMap case.
		{
			name:    "built for retail only",
			files:   map[string]string{"Addon.toc": "## Interface: 120105\n"},
			wantErr: install.ErrWrongGame,
		},
		{
			name:    "no interface line",
			files:   map[string]string{"Addon.toc": "## Title: Addon\n"},
			wantErr: install.ErrWrongGame,
		},
		{
			name:    "retail flavor, forever toc",
			files:   map[string]string{"Addon.toc": "## Interface: 16001\n"},
			flavor:  flavor.Retail,
			wantErr: install.ErrWrongGame,
		},

		// Row 4: in range, but not the client's exact number.
		{
			name:            "out of date",
			files:           map[string]string{"Addon.toc": "## Interface: 120105, 16000\n"},
			clientInterface: 16001,
			wantWarning:     "Addon is out of date: Interface 120105, 16000, but the client is 16001",
		},
		{
			// Without the client's number, the out-of-date check is skipped.
			name:  "client interface unknown",
			files: map[string]string{"Addon.toc": "## Interface: 16000\n"},
		},

		// Row 5: built for this exact client.
		{
			name:            "multi-interface toc",
			files:           map[string]string{"Addon.toc": "## Interface: 120105, 16001, 50504, 11509\n"},
			clientInterface: 16001,
		},
		{
			name:            "retail",
			files:           map[string]string{"Addon.toc": "## Interface: 120105, 16001\n"},
			flavor:          flavor.Retail,
			clientInterface: 120105,
		},
		{
			name:            "classic family toc",
			files:           map[string]string{"Addon_Classic.toc": "## Interface: 50504\n"},
			flavor:          flavor.Classic,
			clientInterface: 50504,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := tt.flavor
			if f == "" {
				f = flavor.Forever
			}
			warning, err := install.Validate(addon(t, tt.files), f, tt.clientInterface)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Validate error = %v, want %v", err, tt.wantErr)
			}
			if err != nil && !strings.HasPrefix(err.Error(), "Addon: ") {
				t.Errorf("error %q should start with the folder name", err)
			}
			if (tt.wantWarning == "" && warning != "") || !strings.Contains(warning, tt.wantWarning) {
				t.Errorf("warning = %q, want %q", warning, tt.wantWarning)
			}
		})
	}
}

// The client loads the first of <Folder>_<GameType>.toc, <Folder>_<Family>.toc
// and <Folder>.toc that exists. Each candidate here is valid only if it's
// the one chosen, so the result shows which was read.
func TestValidateTOCPrecedence(t *testing.T) {
	t.Parallel()
	const (
		chosen = "## Interface: 16001\n"
		other  = "## Interface: 120105\n" // wrong game for Forever
	)
	tests := []struct {
		name  string
		files map[string]string
	}{
		{"game type over family and plain", map[string]string{
			"Addon_Forever.toc": chosen, "Addon_Mainline.toc": other, "Addon.toc": other,
		}},
		{"family over plain", map[string]string{
			"Addon_Mainline.toc": chosen, "Addon.toc": other, "Addon_Standard.toc": other,
		}},
		{"plain when no suffix matches", map[string]string{
			"Addon.toc": chosen, "Addon_Classic.toc": other, "Addon_Mists.toc": other,
		}},
		{"suffix case doesn't matter", map[string]string{
			"addon_FOREVER.TOC": chosen, "Addon.toc": other,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if _, err := install.Validate(addon(t, tt.files), flavor.Forever, 16001); err != nil {
				t.Errorf("Validate read the wrong TOC: %v", err)
			}
		})
	}
}

// A directory named like a TOC isn't one.
func TestValidateTOCDirectory(t *testing.T) {
	t.Parallel()
	dir := addon(t, nil)
	if err := os.Mkdir(filepath.Join(dir, "Addon.toc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := install.Validate(dir, flavor.Forever, 0); !errors.Is(err, install.ErrNotAddon) {
		t.Errorf("Validate error = %v, want ErrNotAddon", err)
	}
}

func TestValidateUnknownFlavor(t *testing.T) {
	t.Parallel()
	dir := addon(t, map[string]string{"Addon.toc": "## Interface: 16001\n"})
	if _, err := install.Validate(dir, flavor.Flavor("ptr"), 0); err == nil {
		t.Error("Validate with an unknown flavor succeeded")
	}
}

// A symlinked TOC is read like any other, as the client follows it.
func TestValidateTOCSymlink(t *testing.T) {
	t.Parallel()
	dir := addon(t, map[string]string{"Addon.toc": "## Interface: 120105\n", "real.toc": "## Interface: 16001\n"})
	if err := os.Symlink("real.toc", filepath.Join(dir, "Addon_Mainline.toc")); err != nil {
		t.Skipf("can't create symlinks here: %v", err)
	}
	if _, err := install.Validate(dir, flavor.Forever, 16001); err != nil {
		t.Errorf("Validate didn't read the symlinked TOC: %v", err)
	}
}

// The error names the files the client would have looked for, not just
// the plain TOC.
func TestValidateNotAddonMessage(t *testing.T) {
	t.Parallel()
	dir := addon(t, map[string]string{"Addon_Mists.toc": "## Interface: 50504\n"})
	_, err := install.Validate(dir, flavor.Forever, 0)
	want := "Addon: not an addon folder: no Addon.toc, or Addon_<suffix>.toc for forever or mainline"
	if err == nil || err.Error() != want {
		t.Errorf("Validate error = %v, want %q", err, want)
	}
}
