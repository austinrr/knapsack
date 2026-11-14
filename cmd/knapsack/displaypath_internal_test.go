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
		// Any client folder, PTRs too, and the case Windows might use.
		{join(append(wine, "_classic_beta_", "interface", "addons")...), "~/Games/battlenet/…/_classic_beta_"},
		// Not in a game folder called World of Warcraft: nothing elided.
		{join("WoW", "_retail_", "Interface", "AddOns"), "~/WoW/_retail_"},
		// Not in a client folder: the whole path.
		{join("addons"), "~/addons"},
		{join("Games", "x", "Interface", "AddOns"), "~/Games/x/Interface/AddOns"},
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
