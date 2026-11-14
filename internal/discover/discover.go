// Package discover finds World of Warcraft installs, for set-profile
// --auto (DESIGN.md §8.4): the folders the game is usually installed in on
// each OS, kept when they hold client folders such as _retail_. It only
// reads the filesystem.
package discover

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
)

// gameFolder is the folder the Battle.net launcher installs the game in.
const gameFolder = "World of Warcraft"

// Candidates returns the places the game folder usually is on goos, for
// Find. They may be glob patterns. home is the user's home directory, and
// drives are Windows' drive roots (C:\, D:\…), from Drives.
func Candidates(goos, home string, drives []string) []string {
	var out []string
	switch goos {
	case "windows":
		for _, d := range drives {
			for _, dir := range []string{"Program Files (x86)", "Program Files", "Games", "Blizzard", ""} {
				out = append(out, filepath.Join(d, dir, gameFolder))
			}
		}
	case "darwin":
		out = append(out, filepath.Join("/Applications", gameFolder))
		if home != "" {
			out = append(out, filepath.Join(escapeGlob(home), "Applications", gameFolder))
		}
	default:
		// Linux runs the game under Wine, in a prefix whose drive_c is a
		// Windows C:. Launchers put prefixes in different places: Wine's
		// own, Lutris (~/Games/<name>), Faugus (~/Faugus/<name>), Bottles,
		// and Steam's Proton.
		if home == "" {
			break
		}
		// home is a literal path in a pattern, so a [ or * in it is
		// escaped, not matched.
		home = escapeGlob(home)
		prefixes := []string{
			".wine",
			"*", "*/*",
			".local/share/bottles/bottles/*",
			".var/app/com.usebottles.bottles/data/bottles/bottles/*",
			".steam/steam/steamapps/compatdata/*/pfx",
			".local/share/Steam/steamapps/compatdata/*/pfx",
		}
		for _, p := range prefixes {
			for _, dir := range []string{"Program Files (x86)", "Program Files", "Games"} {
				out = append(out, filepath.Join(home, filepath.FromSlash(p), "drive_c", dir, gameFolder))
			}
		}
	}
	return out
}

// escapeGlob escapes the characters filepath.Glob treats specially, so s
// matches only itself. It's for Unix paths: on Windows, \ is the separator,
// and Windows candidates have no patterns.
func escapeGlob(s string) string {
	var b strings.Builder
	for _, r := range s {
		if strings.ContainsRune(`*?[\`, r) {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Drives returns the drive roots that exist, C:\ to Z:\. It's empty on
// other OSes, where they don't exist.
func Drives() []string {
	var out []string
	for d := 'C'; d <= 'Z'; d++ {
		root := string(d) + `:\`
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

// Find returns the game folders among candidates, which may be glob
// patterns: folders with at least one client folder in them. Each is
// listed once, in the order found.
func Find(candidates []string) []string {
	var out []string
	for _, c := range candidates {
		matches, err := filepath.Glob(c)
		if err != nil {
			continue // a bad pattern; the others still count
		}
		for _, m := range matches {
			if len(Clients(m)) > 0 && !slices.ContainsFunc(out, func(f string) bool { return samePath(f, m) }) {
				out = append(out, m)
			}
		}
	}
	return out
}

// Clients returns the client folders in game, by their names on disk, in
// flavor's order: each live client, then its tests.
func Clients(game string) []string {
	entries, err := os.ReadDir(game)
	if err != nil {
		return nil
	}
	var out []string
	for _, want := range flavor.ClientFolders() {
		for _, e := range entries {
			if e.IsDir() && strings.EqualFold(e.Name(), want) {
				out = append(out, e.Name())
				break
			}
		}
	}
	return out
}

// samePath reports whether two found folders are one: the same path, or
// differing only in case on Windows and macOS, where case doesn't matter,
// or the same folder through a symlink.
func samePath(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b || (runtime.GOOS == "windows" || runtime.GOOS == "darwin") && strings.EqualFold(a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
