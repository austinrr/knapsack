package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

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
	if canAnimate(w, verbose, getenv) {
		r := newBarReporter(w, time.Now)
		r.animate = true
		return r
	}
	return &lineReporter{w: w, verbose: verbose}
}

// canAnimate reports whether w can show a spinner, the progress bar or
// list's live table: a terminal, but not with --verbose, which prints
// every step as a line, or TERM=dumb.
func canAnimate(w io.Writer, verbose bool, getenv func(string) string) bool {
	return !verbose && isTerminal(w) && getenv("TERM") != "dumb"
}

// warning is how a Warned event is printed.
func warning(e app.Event) string {
	return "warning: " + e.Ref.String() + ": " + e.Message
}

// relay is the App's Reporter. It passes each event on to another
// reporter, which a command can swap while it runs, as list does for its
// live table.
type relay struct {
	mu sync.Mutex
	to reporter
}

func (r *relay) Event(e app.Event) { r.current().Event(e) }

func (r *relay) stop() { r.current().stop() }

func (r *relay) current() reporter {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.to
}

// swap makes to the reporter events go to, and returns the one before.
func (r *relay) swap(to reporter) reporter {
	r.mu.Lock()
	defer r.mu.Unlock()
	prev := r.to
	r.to = to
	return prev
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
		line = warning(e)
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

// barReporter shows one status line on a terminal, redrawn in place, with
// a spinner that turns while anything is in progress:
//
//	⠹ 2 done · 1 resolving · 3 downloading, 1.2 of 4.5 MiB
//
// Warnings are printed above it. Events redraw it at most every
// redrawEvery, so a fast download doesn't flood the terminal.
type barReporter struct {
	mu      sync.Mutex
	line    oneLine
	spin    spinner
	now     func() time.Time
	last    time.Time // when the line was last drawn
	addons  map[string]*addonProgress
	order   []string // keys in addons, in the order first seen
	animate bool     // turn the spinner on a ticker, from the first event
	tick    *ticker
	stopped bool
}

func newBarReporter(w io.Writer, now func() time.Time) *barReporter {
	return &barReporter{line: oneLine{w: w}, spin: newSpinner(), now: now, addons: map[string]*addonProgress{}}
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
	if r.stopped {
		// Results are printing; a warning still mustn't be lost.
		if e.Kind == app.EventWarned {
			_, _ = fmt.Fprintln(r.line.w, warning(e))
		}
		return
	}
	if r.animate && r.tick == nil {
		r.tick = startTicker(spinEvery, r.turn)
	}
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
		r.line.erase()
		_, _ = fmt.Fprintln(r.line.w, warning(e))
	case app.EventInstalled, app.EventRemoved, app.EventSkipped, app.EventChecked, app.EventFailed:
		a.stage = stageDone
	}
	r.draw()
}

// turn advances the spinner while anything is in progress.
func (r *barReporter) turn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped || !r.busy() {
		return
	}
	r.spin.next()
	r.draw()
}

func (r *barReporter) busy() bool {
	for _, a := range r.addons {
		if a.stage != stageDone {
			return true
		}
	}
	return false
}

// text describes every addon seen so far, after the spinner.
func (r *barReporter) text() string {
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
	return r.spin.String() + " " + strings.Join(parts, " · ")
}

func (r *barReporter) draw() {
	r.line.draw(r.text())
	r.last = r.now()
}

// stop erases the bar and stops the spinner. It's safe to call more than
// once.
func (r *barReporter) stop() {
	r.mu.Lock()
	tick := r.tick
	r.stopped = true
	r.mu.Unlock()
	tick.stop() // outside mu, since a turn in progress takes it
	r.mu.Lock()
	defer r.mu.Unlock()
	r.line.erase()
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
