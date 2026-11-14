package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDisplayPath(t *testing.T) {
	t.Parallel()
	home := filepath.FromSlash("/home/me")
	if runtime.GOOS == "windows" {
		home = `C:\Users\me`
	}
	join := func(parts ...string) string { return filepath.Join(append([]string{home}, parts...)...) }
	wine := []string{"Games", "battlenet", "drive_c", "Program Files (x86)", "World of Warcraft"}
	tests := []struct {
		path, want string
	}{
		// A Wine install: the client folder, the prefix's standard part elided.
		{join(append(wine, "_retail_", "Interface", "AddOns")...), "~/Games/battlenet/…/_retail_"},
		// Any client folder, PTRs too.
		{join(append(wine, "_classic_beta_", "Interface", "AddOns")...), "~/Games/battlenet/…/_classic_beta_"},
		// Only the launcher's default layout is cut, so a second install
		// in the same prefix still looks different.
		{join("Games", "battlenet", "drive_c", "Games", "World of Warcraft", "_retail_", "Interface", "AddOns"),
			"~/Games/battlenet/drive_c/Games/World of Warcraft/_retail_"},
		// The drive_c nearest the game, not an earlier folder of that name.
		{join("drive_c", "bn", "drive_c", "Program Files (x86)", "World of Warcraft", "_retail_", "Interface", "AddOns"),
			"~/drive_c/bn/…/_retail_"},
		// Not in a game folder called World of Warcraft: nothing elided.
		{join("WoW", "_retail_", "Interface", "AddOns"), "~/WoW/_retail_"},
		// Not in a client folder: the whole path.
		{join("addons"), "~/addons"},
		{join("Games", "x", "Interface", "AddOns"), "~/Games/x/Interface/AddOns"},
	}
	// Another case is the same folder only where the filesystem ignores
	// case; elsewhere it isn't the client's AddOns, so it's left whole.
	lower := join("WoW", "_retail_", "interface", "addons")
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		tests = append(tests, struct{ path, want string }{lower, "~/WoW/_retail_"})
	} else {
		tests = append(tests, struct{ path, want string }{lower, "~/WoW/_retail_/interface/addons"})
	}
	for _, tt := range tests {
		want := filepath.FromSlash(tt.want)
		if runtime.GOOS == "windows" {
			// Windows shows home as it is.
			want = home + want[1:]
		}
		if got := displayPath(tt.path, home); got != want {
			t.Errorf("displayPath(%q) = %q, want %q", tt.path, got, want)
		}
	}
}
