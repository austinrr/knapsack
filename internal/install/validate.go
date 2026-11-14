package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/toc"
)

// Validate returns these errors, wrapped with the folder name and details,
// for an addon folder the client would never load. Match them with
// errors.Is.
var (
	// ErrNotAddon means the folder has no TOC file the client would read.
	ErrNotAddon = errors.New("not an addon folder")
	// ErrExcluded means the TOC's AllowLoadGameType or ExcludeLoadGameType
	// rules out the client's game type.
	ErrExcluded = errors.New("excluded by the addon")
	// ErrWrongGame means no ## Interface value is in the client's range, so
	// the addon was built for a different game.
	ErrWrongGame = errors.New("built for a different game")
)

// Validate checks the addon folder dir the way a client of flavor f would:
// it picks the TOC file the client would load, then checks its load rules
// and Interface numbers.
//
// clientInterface is the client's exact Interface number, from
// flavor.ClientInterface, or 0 if it isn't known. An addon that is in range
// but not built for that exact number is loaded by the client only as out
// of date; Validate returns that as a warning rather than an error, and
// skips the check when clientInterface is 0.
func Validate(dir string, f flavor.Flavor, clientInterface int) (warning string, err error) {
	c, ok := f.Client()
	if !ok {
		return "", fmt.Errorf("unknown flavor %q", f)
	}
	folder := filepath.Base(dir)

	name, file, err := TOC(dir, f)
	if err != nil {
		return "", fmt.Errorf("%s: %w", folder, err)
	}

	if err := checkGameType(file, c); err != nil {
		return "", fmt.Errorf("%s: %w", folder, err)
	}

	switch {
	case len(file.Interfaces) == 0:
		return "", fmt.Errorf("%s: %w: %s has no ## Interface", folder, ErrWrongGame, name)
	case !slices.ContainsFunc(file.Interfaces, c.Interface.Contains):
		return "", fmt.Errorf("%s: %w: Interface %s, but %s needs %s",
			folder, ErrWrongGame, joinInts(file.Interfaces), f, c.Interface)
	case clientInterface != 0 && !slices.Contains(file.Interfaces, clientInterface):
		return fmt.Sprintf("%s is out of date: Interface %s, but the client is %d",
			folder, joinInts(file.Interfaces), clientInterface), nil
	}
	return "", nil
}

// selectTOC returns the name of the TOC file in dir that the client loads:
// the first of <Folder>_<GameType>.toc (then one per alias),
// <Folder>_<Family>.toc and <Folder>.toc that exists. Names are matched without regard to case, as
// on Windows and macOS, where the client runs.
func selectTOC(dir string, c flavor.Client) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	files := map[string]string{} // lower-cased name → actual name
	for _, e := range entries {
		// Not IsRegular: a symlinked TOC is a TOC, and the client follows it.
		if !e.IsDir() {
			files[strings.ToLower(e.Name())] = e.Name()
		}
	}
	folder := filepath.Base(dir)
	var candidates []string
	for _, t := range c.GameTypes() {
		candidates = append(candidates, folder+"_"+t+".toc")
	}
	candidates = append(candidates, folder+"_"+string(c.Family)+".toc", folder+".toc")
	for _, want := range candidates {
		if name, ok := files[strings.ToLower(want)]; ok {
			return name, nil
		}
	}
	return "", fmt.Errorf("%w: no %s.toc, or %s_<suffix>.toc for %s or %s",
		ErrNotAddon, folder, folder, strings.Join(c.GameTypes(), ", "), c.Family)
}

// TOC returns the name and contents of the TOC file a client of flavor f
// loads from the addon folder dir, for showing an addon's title and notes.
func TOC(dir string, f flavor.Flavor) (string, toc.File, error) {
	c, ok := f.Client()
	if !ok {
		return "", toc.File{}, fmt.Errorf("unknown flavor %q", f)
	}
	name, err := selectTOC(dir, c)
	if err != nil {
		return "", toc.File{}, err
	}
	file, err := readTOC(filepath.Join(dir, name))
	return name, file, err
}

// readTOC parses one TOC file.
func readTOC(path string) (toc.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return toc.File{}, err
	}
	defer func() { _ = f.Close() }() // read-only; nothing to flush
	return toc.Parse(f)
}

// checkGameType applies ExcludeLoadGameType and AllowLoadGameType. A name
// matches the client if it's the client's game type, an alias, or its
// family. Allow
// follows the client's quirk: names the client doesn't recognize are
// ignored, and if none are recognized the rule passes.
func checkGameType(file toc.File, c flavor.Client) error {
	matches := func(name string) bool {
		return slices.Contains(c.GameTypes(), name) || name == string(c.Family)
	}
	if slices.ContainsFunc(file.ExcludeLoadGameTypes, matches) {
		return fmt.Errorf("%w: ExcludeLoadGameType %s", ErrExcluded, strings.Join(file.ExcludeLoadGameTypes, ", "))
	}
	known := slices.DeleteFunc(slices.Clone(file.AllowLoadGameTypes), func(name string) bool {
		return !flavor.KnownGameType(name)
	})
	if len(known) > 0 && !slices.ContainsFunc(known, matches) {
		return fmt.Errorf("%w: AllowLoadGameType %s", ErrExcluded, strings.Join(file.AllowLoadGameTypes, ", "))
	}
	return nil
}

// joinInts formats Interface numbers for messages: "120105, 16001".
func joinInts(ns []int) string {
	s := make([]string, len(ns))
	for i, n := range ns {
		s[i] = fmt.Sprint(n)
	}
	return strings.Join(s, ", ")
}
