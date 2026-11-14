package main

import (
	"runtime"
	"testing"
)

func TestShortPath(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		if got := shortPath(`C:\Users\me\wow`, `C:\Users\me`); got != `C:\Users\me\wow` {
			t.Errorf("shortPath on Windows = %q, want it unchanged", got)
		}
		return
	}
	for path, want := range map[string]string{
		"/home/me/wow":     "~/wow",
		"/home/me":         "~",
		"/home/meadow/wow": "/home/meadow/wow", // not under /home/me
		"/opt/wow":         "/opt/wow",
	} {
		if got := shortPath(path, "/home/me"); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", path, got, want)
		}
	}
	if got := shortPath("/home/me/wow", ""); got != "/home/me/wow" {
		t.Errorf("with no home, shortPath = %q", got)
	}
}
