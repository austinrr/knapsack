package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/austinrr/knapsack/internal/app"
)

// reporter is an app.Reporter the CLI can stop before printing results.
type reporter interface {
	app.Reporter
	// stop ends any progress display, so stdout can be written cleanly.
	stop()
}

// newReporter picks how progress is shown on w: a progress bar on a
// terminal, else plain lines. --verbose always gets lines, since it shows
// every step.
func newReporter(w io.Writer, verbose bool, getenv func(string) string) reporter {
	if !verbose && isTerminal(w) && getenv("TERM") != "dumb" {
		return &barReporter{w: w, now: time.Now, addons: map[string]*addonProgress{}}
	}
	return &lineReporter{w: w, verbose: verbose}
}

// isTerminal reports whether w is a terminal (a character device). That's
// also true of /dev/null and NUL, where the bar is harmlessly discarded.
func isTerminal(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

// lineReporter prints progress on stderr as lines: warnings always, and
// each step with --verbose. Failures and skips aren't printed here; the
// command reports them at the end, failures with their hints. A mutex keeps
// lines from interleaving, since events arrive from parallel downloads.
type lineReporter struct {
	mu      sync.Mutex
	w       io.Writer
	verbose bool
}

func (r *lineReporter) Event(e app.Event) {
	var line string
	switch {
	case e.Kind == app.EventWarned:
		line = "warning: " + e.Ref.String() + ": " + e.Message
	case !r.verbose:
		return
	case e.Kind == app.EventResolving:
		line = "resolving " + e.Ref.String()
	case e.Kind == app.EventDownloading:
		line = "downloading " + e.Ref.String() + " " + e.Version
	default:
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Progress that can't be written isn't worth failing the command for.
	_, _ = fmt.Fprintln(r.w, line)
}

func (r *lineReporter) stop() {}

// barReporter shows one status line on a terminal, redrawn in place with a
// carriage return:
//
//	2 done · 1 resolving · 3 downloading, 1.2 of 4.5 MiB
//
// One line needs no cursor movement, so it works in any terminal,
// including the old Windows console. Warnings are printed above it. It
// redraws at most every redrawEvery, so a fast download doesn't flood the
// terminal.
type barReporter struct {
	mu     sync.Mutex
	w      io.Writer
	now    func() time.Time
	last   time.Time // when the line was last drawn
	shown  int       // columns of text on screen, to erase it
	addons map[string]*addonProgress
	order  []string // keys in addons, in the order first seen
}

// addonProgress is where one addon is.
type addonProgress struct {
	stage       stage
	done, total int64
}

type stage int

const (
	stageResolving stage = iota
	stageDownloading
	stageDone
)

const redrawEvery = 100 * time.Millisecond

func (r *barReporter) Event(e app.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := e.Profile + " " + e.Ref.String()
	if e.Ref.ID == "" {
		key = e.Profile + " " + e.Name
	}
	a := r.addons[key]
	if a == nil {
		a = &addonProgress{}
		r.addons[key] = a
		r.order = append(r.order, key)
	}
	switch e.Kind {
	case app.EventResolving:
		a.stage = stageResolving
	case app.EventDownloading:
		a.stage, a.done, a.total = stageDownloading, 0, -1
	case app.EventProgress:
		a.done, a.total = e.Done, e.Total
		if r.now().Sub(r.last) < redrawEvery {
			return
		}
	case app.EventWarned:
		r.erase()
		_, _ = fmt.Fprintln(r.w, "warning: "+e.Ref.String()+": "+e.Message)
	case app.EventInstalled, app.EventRemoved, app.EventSkipped, app.EventChecked, app.EventFailed:
		a.stage = stageDone
	}
	r.draw()
}

// line describes every addon seen so far.
func (r *barReporter) line() string {
	var done, resolving, downloading int
	var got, want int64
	known := true
	for _, key := range r.order {
		a := r.addons[key]
		switch a.stage {
		case stageResolving:
			resolving++
		case stageDownloading:
			downloading++
			got += a.done
			if a.total < 0 {
				known = false
			} else {
				want += a.total
			}
		case stageDone:
			done++
		}
	}
	parts := []string{fmt.Sprintf("%d done", done)}
	if resolving > 0 {
		parts = append(parts, fmt.Sprintf("%d resolving", resolving))
	}
	if downloading > 0 {
		bytes := formatBytes(got)
		if known {
			bytes += " of " + formatBytes(want)
		}
		parts = append(parts, fmt.Sprintf("%d downloading, %s", downloading, bytes))
	}
	return strings.Join(parts, " · ")
}

// draw writes the line over the previous one, padding with spaces to
// cover a longer one.
func (r *barReporter) draw() {
	line := r.line()
	// Measured in runes, not bytes: "·" is two bytes but one column.
	width := utf8.RuneCountInString(line)
	pad := ""
	if n := r.shown - width; n > 0 {
		pad = strings.Repeat(" ", n)
	}
	_, _ = fmt.Fprint(r.w, "\r"+line+pad)
	r.shown, r.last = width, r.now()
}

// erase clears the line and leaves the cursor at its start.
func (r *barReporter) erase() {
	if r.shown > 0 {
		_, _ = fmt.Fprint(r.w, "\r"+strings.Repeat(" ", r.shown)+"\r")
		r.shown = 0
	}
}

func (r *barReporter) stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.erase()
}

// formatBytes shows a size in KiB or MiB with one decimal, as progress
// needs no more precision.
func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
