package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

func TestBarReporter(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	clock := time.Unix(0, 0)
	r := &barReporter{w: &buf, now: func() time.Time { return clock }, addons: map[string]*addonProgress{}}
	syndicator := ref.Ref{Source: "github", ID: "austinrr/syndicator-mirror"}
	step := func(e app.Event) string {
		buf.Reset()
		e.Profile = "forever"
		r.Event(e)
		return buf.String()
	}
	tests := []struct {
		name  string
		event app.Event
		tick  time.Duration // advance the clock first
		want  string
	}{
		{"resolving", app.Event{Kind: app.EventResolving, Ref: baganator}, 0,
			"\r0 done · 1 resolving"},
		{"second resolving", app.Event{Kind: app.EventResolving, Ref: syndicator}, 0,
			"\r0 done · 2 resolving"},
		{"downloading, size unknown yet", app.Event{Kind: app.EventDownloading, Ref: baganator}, 0,
			"\r0 done · 1 resolving · 1 downloading, 0 B"},
		{"progress", app.Event{Kind: app.EventProgress, Ref: baganator, Done: 512 << 10, Total: 1 << 20}, redrawEvery,
			"\r0 done · 1 resolving · 1 downloading, 512.0 KiB of 1.0 MiB"},
		// Too soon after the last draw: counted, not drawn.
		{"throttled", app.Event{Kind: app.EventProgress, Ref: baganator, Done: 600 << 10, Total: 1 << 20}, time.Millisecond,
			""},
		{"warning above the line", app.Event{Kind: app.EventWarned, Ref: baganator, Message: "Core is out of date"}, 0,
			"\r" + spaces("0 done · 1 resolving · 1 downloading, 512.0 KiB of 1.0 MiB") + "\r" +
				"warning: github:austinrr/baganator-mirror: Core is out of date\n" +
				"\r0 done · 1 resolving · 1 downloading, 600.0 KiB of 1.0 MiB"},
		// A shorter line is padded to cover the old one.
		{"installed", app.Event{Kind: app.EventInstalled, Ref: baganator}, 0,
			"\r" + cover("1 done · 1 resolving", "0 done · 1 resolving · 1 downloading, 600.0 KiB of 1.0 MiB")},
	}
	for _, tt := range tests {
		clock = clock.Add(tt.tick)
		if got := step(tt.event); got != tt.want {
			t.Errorf("%s: wrote %q, want %q", tt.name, got, tt.want)
		}
	}

	buf.Reset()
	r.stop()
	if want := "\r" + spaces("1 done · 1 resolving") + "\r"; buf.String() != want {
		t.Errorf("stop wrote %q, want %q", buf.String(), want)
	}
	buf.Reset()
	r.stop()
	if buf.Len() != 0 {
		t.Errorf("second stop wrote %q, want nothing", buf.String())
	}
}

// spaces covers the columns s took on screen.
func spaces(s string) string { return strings.Repeat(" ", utf8.RuneCountInString(s)) }

// cover pads line with spaces over the rest of old.
func cover(line, old string) string {
	return line + strings.Repeat(" ", utf8.RuneCountInString(old)-utf8.RuneCountInString(line))
}

// The bar is only for a terminal, and never with --verbose or TERM=dumb.
func TestNewReporter(t *testing.T) {
	t.Parallel()
	noEnv := func(string) string { return "" }
	if _, ok := newReporter(&bytes.Buffer{}, false, noEnv).(*lineReporter); !ok {
		t.Error("a buffer got the progress bar")
	}
	f, err := os.Create(filepath.Join(t.TempDir(), "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if isTerminal(f) {
		t.Error("a regular file counts as a terminal")
	}
	// os.DevNull is a character device, so it stands in for a terminal.
	null, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = null.Close() }()
	if _, ok := newReporter(null, false, noEnv).(*barReporter); !ok {
		t.Error("a character device didn't get the progress bar")
	}
	if _, ok := newReporter(null, true, noEnv).(*lineReporter); !ok {
		t.Error("--verbose got the progress bar")
	}
	dumb := func(k string) string {
		if k == "TERM" {
			return "dumb"
		}
		return ""
	}
	if _, ok := newReporter(null, false, dumb).(*lineReporter); !ok {
		t.Error("TERM=dumb got the progress bar")
	}
}
