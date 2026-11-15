package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replace is a fake edit: from replaced by to.
func replace(from, to string) func(string) string {
	return func(text string) string { return strings.Replace(text, from, to, 1) }
}

// addDetailsLine adds details-mirror to the profile's addons.
var addDetailsLine = replace("addons = [\n", "addons = [\n  \"github:austinrr/details-mirror\",\n")

func TestEditCommands(t *testing.T) {
	t.Parallel()
	runCases(t, []cliCase{
		{name: "profiles/edit_help", args: []string{"profiles", "edit", "--help"}},
		// Adding and removing addons in the editor, for update to do.
		{name: "profiles/edit_add", setup: [][]string{addForever, addBaganator}, edits: []func(string) string{addDetailsLine},
			args: []string{"profiles", "edit", "forever"}},
		{name: "profiles/edit_remove", setup: [][]string{addForever, addBaganator},
			edits: []func(string) string{replace("  \"github:austinrr/baganator-mirror\",\n", "")},
			args:  []string{"profiles", "edit", "forever"}},
		// A folder that isn't a live client's can be any flavor.
		{name: "profiles/edit_flavor", setup: [][]string{{"profiles", "create", "custom", "--path", plain, "--flavor", "forever"}},
			edits: []func(string) string{replace(`flavor = "forever"`, `flavor = "classic_era"`)},
			args:  []string{"profiles", "edit", "custom"}},
		// A live client's folder decides, as for profiles set: the editor
		// opens again, then is quit without saving.
		{name: "profiles/edit_flavor_mismatch", setup: [][]string{addForever},
			edits: []func(string) string{replace(`flavor = "forever"`, `flavor = "retail"`)},
			args:  []string{"profiles", "edit", "forever"}},
		{name: "profiles/edit_missing_path", setup: [][]string{addForever},
			edits: []func(string) string{replace("_forever_", "_gone_")},
			args:  []string{"profiles", "edit", "forever"}},
		// Quitting without saving changes nothing.
		{name: "profiles/edit_cancel", setup: [][]string{addForever}, args: []string{"profiles", "edit", "forever"}},
		// Saved as it was is unchanged too.
		{name: "profiles/edit_same", setup: [][]string{addForever}, edits: []func(string) string{replace("#|", "#|")},
			args: []string{"profiles", "edit", "forever"}},
		{name: "profiles/edit_unknown", args: []string{"profiles", "edit", "nope"}},
		{name: "profiles/edit_no_name", args: []string{"profiles", "edit"}},
		// An invalid edit opens the editor again, with the error; fixing it
		// saves.
		{name: "profiles/edit_fixed", setup: [][]string{addForever, addBaganator},
			edits: []func(string) string{replace(`flavor = "forever"`, `flavor = "forevr"`),
				func(text string) string {
					return addDetailsLine(replace(`flavor = "forevr"`, `flavor = "forever"`)(text))
				}},
			args: []string{"profiles", "edit", "forever"}},

		{name: "config/edit_help", args: []string{"config", "edit", "--help"}},
		{name: "config/edit", setup: [][]string{addForever, addRetail},
			edits: []func(string) string{replace(`current_profile = "forever"`, "# switched by hand\ncurrent_profile = \"retail\"")},
			args:  []string{"config", "edit"}},
		{name: "config/edit_cancel", setup: [][]string{addForever}, args: []string{"config", "edit"}},
	})
}

// The editor is shown the profile with notes above it, and opened again
// on an invalid edit with the error, which is dropped once fixed; the
// saved profile is what was written.
func TestProfilesEditReopens(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.edits = []func(string) string{
		replace(`flavor = "forever"`, `flavor = "forevr"`),
		replace(`flavor = "forevr"`, `flavor = "forever"`),
	}
	s.mustRun("profiles", "edit", "forever")
	if len(s.shown) != 2 {
		t.Fatalf("editor opened %d times, want twice", len(s.shown))
	}
	if !strings.HasPrefix(s.shown[0], "#| Editing profile \"forever\".") || !strings.Contains(s.shown[0], "path = ") {
		t.Errorf("first opening:\n%s", s.shown[0])
	}
	if !strings.Contains(s.shown[1], "#| error: ") || !strings.Contains(s.shown[1], "forevr") {
		t.Errorf("second opening has no error:\n%s", s.shown[1])
	}
	data, err := os.ReadFile(filepath.Join(s.home, "config", "config.toml"))
	if err != nil || strings.Contains(string(data), "#|") || !strings.Contains(string(data), `flavor = "forever"`) {
		t.Errorf("config = %q, %v; want the fixed profile without notes", data, err)
	}
}

// Cancelling after an invalid edit keeps the edit in a file the error
// names, so nothing typed is lost.
func TestProfilesEditCancelKeepsEdit(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.edits = []func(string) string{replace(`flavor = "forever"`, `flavor = "forevr"`)}
	r := s.run("profiles", "edit", "forever")
	if r.Code != exitError || !strings.Contains(r.Stderr, "edit cancelled") || !strings.Contains(r.Stderr, "hint: your edit is kept in <home>/config/edit-") {
		t.Fatalf("result:\n%s", r)
	}
	kept, _ := filepath.Glob(filepath.Join(s.home, "config", "edit-*.toml"))
	if len(kept) != 1 {
		t.Fatalf("kept copies: %q, want one", kept)
	}
	if data, err := os.ReadFile(kept[0]); err != nil || !strings.Contains(string(data), "forevr") {
		t.Errorf("kept edit = %q, %v", data, err)
	}
}

// A saved edit leaves no copy behind.
func TestEditCleansUp(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.edits = []func(string) string{replace(`flavor = "forever"`, `flavor = "forever"`+"\n")}
	s.mustRun("profiles", "edit", "forever")
	if kept, _ := filepath.Glob(filepath.Join(s.home, "config", "edit-*.toml")); len(kept) != 0 {
		t.Errorf("left behind: %q", kept)
	}
}

// A profile changed by another command while it's edited isn't
// overwritten: the edit fails, and is kept.
func TestProfilesEditConflict(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.mustRun(addBaganator...)
	s.edits = []func(string) string{func(text string) string {
		s.mustRun(addDetails...) // meanwhile, in another terminal
		return strings.Replace(text, "  \"github:austinrr/baganator-mirror\",\n", "", 1)
	}}
	r := s.run("profiles", "edit", "forever")
	if r.Code != exitError || !strings.Contains(r.Stderr, `profile "forever" changed while you were editing it`) || !strings.Contains(r.Stderr, "your edit is kept in") {
		t.Fatalf("result:\n%s", r)
	}
	if data, _ := os.ReadFile(filepath.Join(s.home, "config", "config.toml")); !strings.Contains(string(data), "details-mirror") || !strings.Contains(string(data), "baganator-mirror") {
		t.Errorf("the edit was saved over the change:\n%s", data)
	}
}

// config edit saves the file as written, comments included, and refuses
// an invalid one until it's fixed.
func TestConfigEditKeepsComments(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.edits = []func(string) string{
		func(text string) string {
			return "# my settings\n" + strings.Replace(text, "current_profile", "current_profle", 1)
		},
		replace("\ncurrent_profle", "\ncurrent_profile"),
	}
	s.mustRun("config", "edit")
	if len(s.shown) != 2 || !strings.Contains(s.shown[1], "#| error: unknown keys: current_profle") {
		t.Fatalf("openings: %q", s.shown)
	}
	data, err := os.ReadFile(filepath.Join(s.home, "config", "config.toml"))
	if err != nil || !strings.HasPrefix(string(data), "# my settings\n") || strings.Contains(string(data), "#|") {
		t.Errorf("config = %q, %v; want it as written, without notes", data, err)
	}
}

// A profile's folder is checked only when the edit changes it, so one
// whose folder is missing for now can still have its addons edited; a
// changed one may be written with ~ and as the client folder.
func TestProfilesEditPaths(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	if err := os.RemoveAll(filepath.Join(s.home, "wow", "_forever_")); err != nil {
		t.Fatal(err)
	}
	s.edits = []func(string) string{replace("addons = []", `addons = ["github:austinrr/details-mirror"]`)}
	s.mustRun("profiles", "edit", "forever")
	s.shown = nil
	s.edits = []func(string) string{func(text string) string {
		_, rest, _ := strings.Cut(text, `path = "`)
		path, _, _ := strings.Cut(rest, `"`)
		return strings.Replace(text, path, "~/wow/_retail_", 1)
	}, replace(`flavor = "forever"`, `flavor = "retail"`)}
	s.env = map[string]string{"HOME": s.home}
	r := s.run("profiles", "edit", "forever")
	if r.Code != exitOK || len(s.shown) != 2 || !strings.Contains(s.shown[1], "doesn't match the path") {
		t.Fatalf("result:\n%s\nopenings: %q", r, s.shown)
	}
	data, _ := os.ReadFile(filepath.Join(s.home, "config", "config.toml"))
	if want := filepath.Join(s.home, "wow", "_retail_", "Interface", "AddOns"); !strings.Contains(string(data), want) {
		t.Errorf("config:\n%s\nwant the path completed to %s", data, want)
	}
}

// A failing editor after an invalid try keeps what was typed.
func TestEditEditorFailsKeepsEdit(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.edits = []func(string) string{replace(`flavor = "forever"`, `flavor = "forevr"`)}
	s.editorFails = 2 // the second opening, as with vim's :cq
	r := s.run("profiles", "edit", "forever")
	if r.Code != exitError || !strings.Contains(r.Stderr, "your edit is kept in") {
		t.Fatalf("result:\n%s", r)
	}
	if kept, _ := filepath.Glob(filepath.Join(s.home, "config", "edit-*.toml")); len(kept) != 1 {
		t.Errorf("kept copies: %q, want one", kept)
	}
}

// config edit deletes a profile taken out as profiles delete would, state
// and all, and moves a renamed one's state with it.
func TestConfigEditProfiles(t *testing.T) {
	t.Parallel()
	s := newSandbox(t)
	s.mustRun(addForever...)
	s.mustRun(addBaganator...)
	s.mustRun(addRetail...)
	s.mustRun("add", "-p", "retail", "github:austinrr/retail-only")
	s.edits = []func(string) string{func(text string) string {
		text = strings.Replace(text, "[profiles.forever]", "[profiles.main]", 1)
		text = strings.Replace(text, `current_profile = "forever"`, `current_profile = "main"`, 1)
		i := strings.Index(text, "[profiles.retail]")
		return text[:i]
	}}
	r := s.run("config", "edit")
	if r.Code != exitOK || !strings.Contains(r.Stdout, `Renamed profile "forever" to "main"`) || !strings.Contains(r.Stdout, `Deleted profile "retail"`) {
		t.Fatalf("result:\n%s", r)
	}
	states := filepath.Join(s.home, "config", "state")
	for name, want := range map[string]bool{"forever": false, "main": true, "retail": false} {
		if _, err := os.Stat(filepath.Join(states, name+".toml")); (err == nil) != want {
			t.Errorf("state for %s: %v, want it there: %v", name, err, want)
		}
	}
	if l := s.run("list", "--no-check"); !strings.Contains(l.Stdout, "Baganator") {
		t.Errorf("main's addons after the rename:\n%s", l)
	}
}
