package app_test

import (
	"runtime"
	"testing"

	"github.com/austinrr/knapsack/internal/app"
)

func TestShortenHome(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		if got := app.ShortenHome(`C:\Users\me\wow`, `C:\Users\me`); got != `C:\Users\me\wow` {
			t.Errorf("ShortenHome on Windows = %q, want it unchanged", got)
		}
		return
	}
	for path, want := range map[string]string{
		"/home/me/wow":     "~/wow",
		"/home/me":         "~",
		"/home/meadow/wow": "/home/meadow/wow", // not under /home/me
		"/opt/wow":         "/opt/wow",
	} {
		got := app.ShortenHome(path, "/home/me")
		if got != want {
			t.Errorf("ShortenHome(%q) = %q, want %q", path, got, want)
		}
		if back := app.ExpandHome(got, "/home/me"); back != path {
			t.Errorf("ExpandHome(%q) = %q, want %q back", got, back, path)
		}
	}
	if got := app.ShortenHome("/home/me/wow", ""); got != "/home/me/wow" {
		t.Errorf("with no home, ShortenHome = %q", got)
	}
}
