package main

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// newTestLive returns a liveList drawing into a buffer, as on a terminal
// of width by height, without the spinner's ticker.
func newTestLive(width, height int) (*liveList, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	size := func() (int, int, error) { return width, height, nil }
	return newLiveList(&out, &errOut, size, "Profile retail:", false, false, []string{"*", "+"}), &out, &errOut
}

func addon(t *testing.T, id string, status app.Status) app.Installed {
	t.Helper()
	r, err := ref.Parse("github:o/" + id)
	if err != nil {
		t.Fatal(err)
	}
	return app.Installed{Profile: "retail", Ref: r, Title: id, Status: status, Version: "v1"}
}

// The table is drawn when listed, with a spinner for each addon being
// checked, then redrawn in place as each check finishes, and erased by
// stop.
func TestLiveListRedraws(t *testing.T) {
	t.Parallel()
	l, out, _ := newTestLive(80, 24)
	l.Listed([]app.Installed{addon(t, "a", app.StatusChecking), addon(t, "b", app.StatusChecking)})
	first := out.String()
	want := "Profile retail:\x1b[K\n" +
		"ADDON  STATUS      VERSION  SOURCE\x1b[K\n" +
		"a      * checking  v1       github\x1b[K\n" +
		"b      * checking  v1       github\x1b[K\n" +
		"\x1b[J"
	if diff := cmp.Diff(want, first); diff != "" {
		t.Errorf("first draw (-want +got):\n%s", diff)
	}

	out.Reset()
	l.Checked("retail", 1, addon(t, "b", app.StatusUpToDate))
	if got := out.String(); !strings.HasPrefix(got, "\r\x1b[4A") || !strings.Contains(got, "b      up to date  v1") || !strings.Contains(got, "a      * checking") {
		t.Errorf("redraw = %q, want the cursor moved up over 4 lines and b up to date", got)
	}

	out.Reset()
	l.stop()
	l.stop() // again does nothing
	if got := out.String(); got != "\r\x1b[4A\x1b[J" {
		t.Errorf("stop wrote %q, want the table erased", got)
	}
}

// Under -A, each profile's rows follow the last's, and a check is matched
// to its row by the profile and the index in what it listed.
func TestLiveListProfiles(t *testing.T) {
	t.Parallel()
	l, _, _ := newTestLive(80, 24)
	l.all = true
	l.header = ""
	classic := addon(t, "c", app.StatusChecking)
	classic.Profile = "classic"
	l.Listed([]app.Installed{addon(t, "a", app.StatusChecking)})
	l.Listed(nil) // a profile with nothing in it draws nothing
	l.Listed([]app.Installed{classic})
	done := classic
	done.Status = app.StatusUpdateAvailable
	l.Checked("classic", 0, done)
	if l.rows[0].Status != app.StatusChecking || l.rows[1].Status != app.StatusUpdateAvailable {
		t.Errorf("rows = %+v, want classic's check on its own row", l.rows)
	}
}

// A warning is printed on stderr with the table erased, then the table is
// drawn again below it.
func TestLiveListWarning(t *testing.T) {
	t.Parallel()
	l, out, errOut := newTestLive(80, 24)
	l.Listed([]app.Installed{addon(t, "a", app.StatusChecking)})
	out.Reset()
	l.Event(app.Event{Kind: app.EventResolving}) // not shown
	l.Event(app.Event{Kind: app.EventWarned, Ref: l.rows[0].Ref, Message: "careful"})
	if got := errOut.String(); got != "warning: github:o/a: careful\n" {
		t.Errorf("stderr = %q", got)
	}
	if got := out.String(); !strings.HasPrefix(got, "\r\x1b[3A\x1b[JProfile retail:\x1b[K\n") {
		t.Errorf("stdout = %q, want the table erased, then drawn again", got)
	}
}

// Nothing is drawn before there's a row, so stop has nothing to erase.
func TestLiveListEmpty(t *testing.T) {
	t.Parallel()
	l, out, _ := newTestLive(80, 24)
	l.Listed(nil)
	l.stop()
	if out.Len() != 0 {
		t.Errorf("wrote %q for no addons", out.String())
	}
}

func TestFit(t *testing.T) {
	t.Parallel()
	lines := []string{"header", "row 1", "row 2", "row 3", "row 4"}
	tests := []struct {
		name          string
		width, height int
		want          []string
	}{
		{"fits", 80, 24, lines},
		{"unknown size", 0, 0, lines},
		// One line is left for the cursor, and the last shown counts the rest.
		{"too tall", 80, 4, []string{"header", "row 1", "… 3 more"}},
		{"too narrow", 4, 24, []string{"hea", "row", "row", "row", "row"}},
		{"cut by runes", 3, 24, []string{"he", "ro", "ro", "ro", "ro"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tt.want, fit(slices.Clone(lines), tt.width, tt.height)); diff != "" {
				t.Errorf("fit (-want +got):\n%s", diff)
			}
		})
	}
	if got := fit([]string{"Détails"}, 4, 0); got[0] != "Dét" {
		t.Errorf("fit cut %q, want whole runes", got[0])
	}
}

// An unknown terminal size draws the whole table.
func TestLiveListSizeError(t *testing.T) {
	t.Parallel()
	l, out, _ := newTestLive(0, 0)
	l.size = func() (int, int, error) { return 0, 0, errors.New("no size") }
	l.Listed([]app.Installed{addon(t, "a", app.StatusChecking)})
	if strings.Count(out.String(), "\n") != 3 {
		t.Errorf("drew %q, want the header, column names and row", out.String())
	}
}
