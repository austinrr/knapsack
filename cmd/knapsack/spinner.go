package main

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/austinrr/knapsack/internal/app"
)

// spinner is the animation shown while Knapsack waits on the network
// (DESIGN.md §8.6): in list's and check's rows being checked, at the start
// of the progress bar, and in a status line for a wait with nothing else to
// show. Each redraws on a ticker that calls next.
type spinner struct {
	frames []string
	i      int
}

// Spinner frames: braille dots, or ASCII for the Windows console, whose
// default fonts lack them.
var (
	spinnerDots  = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerASCII = []string{"|", "/", "-", `\`}
)

// spinEvery is how often a spinner turns.
const spinEvery = 100 * time.Millisecond

func newSpinner() spinner {
	if runtime.GOOS == "windows" {
		return spinner{frames: spinnerASCII}
	}
	return spinner{frames: spinnerDots}
}

func (s *spinner) String() string { return s.frames[s.i] }

func (s *spinner) next() { s.i = (s.i + 1) % len(s.frames) }

// ticker calls fn every interval on its own goroutine until stop, which
// waits for a call in progress to finish. stop is safe to call more than
// once.
type ticker struct {
	quit, done chan struct{}
	once       sync.Once
}

func startTicker(interval time.Duration, fn func()) *ticker {
	t := &ticker{quit: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(t.done)
		tk := time.NewTicker(interval)
		defer tk.Stop()
		for {
			select {
			case <-t.quit:
				return
			case <-tk.C:
				fn()
			}
		}
	}()
	return t
}

func (t *ticker) stop() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		close(t.quit)
		<-t.done
	})
}

// oneLine is a single line on a terminal, redrawn in place with a carriage
// return. Needing no cursor movement, it works in any terminal, the old
// Windows console included. It isn't safe for concurrent use; its owner
// locks.
type oneLine struct {
	w     io.Writer
	shown int // columns of text on screen, to erase it
}

// draw writes line over the one on screen, padding with spaces to cover a
// longer one.
func (l *oneLine) draw(line string) {
	// Measured in runes, not bytes: "·" is two bytes but one column.
	width := utf8.RuneCountInString(line)
	pad := ""
	if n := l.shown - width; n > 0 {
		pad = strings.Repeat(" ", n)
	}
	_, _ = fmt.Fprint(l.w, "\r"+line+pad) // progress; nothing to fail for
	l.shown = width
}

// erase clears the line and leaves the cursor at its start.
func (l *oneLine) erase() {
	if l.shown > 0 {
		_, _ = fmt.Fprint(l.w, "\r"+strings.Repeat(" ", l.shown)+"\r")
		l.shown = 0
	}
}

// statusShowAfter is how long a status waits before it shows, so a quick
// answer doesn't flash one.
const statusShowAfter = 200 * time.Millisecond

// status is a spinner and a message on one line of stderr, for a wait with
// nothing else to show, such as checking for a newer release. It's also a
// reporter, for self-update's download. A nil *status shows nothing, so
// callers needn't check whether there's a terminal.
type status struct {
	mu      sync.Mutex
	line    oneLine
	spin    spinner
	msg     string
	visible bool // false until statusShowAfter has passed
	stopped bool
	tick    *ticker
	now     func() time.Time
	since   time.Time
}

// startStatus shows msg with a spinner on w, if w can animate (canAnimate),
// once statusShowAfter has passed. Otherwise it returns nil, which shows
// nothing.
func startStatus(w io.Writer, verbose bool, getenv func(string) string, msg string) *status {
	if !canAnimate(w, verbose, getenv) {
		return nil
	}
	s := newStatus(w, msg, time.Now)
	s.tick = startTicker(spinEvery, s.turn)
	return s
}

func newStatus(w io.Writer, msg string, now func() time.Time) *status {
	return &status{line: oneLine{w: w}, spin: newSpinner(), msg: msg, now: now, since: now()}
}

// turn advances the spinner and redraws, once the status may show.
func (s *status) turn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	if !s.visible && s.now().Sub(s.since) < statusShowAfter {
		return
	}
	s.visible = true
	s.spin.next()
	s.draw()
}

// set changes the message.
func (s *status) set(msg string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.msg = msg
	if s.visible && !s.stopped {
		s.draw()
	}
}

func (s *status) draw() { s.line.draw(s.spin.String() + " " + s.msg) }

// stop erases the status. It's safe to call more than once.
func (s *status) stop() {
	if s == nil {
		return
	}
	s.tick.stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	s.line.erase()
}

// Event implements app.Reporter, for self-update: finding the release,
// then downloading it.
func (s *status) Event(e app.Event) {
	if s == nil {
		return
	}
	switch e.Kind {
	case app.EventResolving:
		s.set("Looking for the newest knapsack release")
	case app.EventDownloading:
		s.set("Downloading knapsack " + e.Version)
	case app.EventProgress:
		got := formatBytes(e.Done)
		if e.Total >= 0 {
			got += " of " + formatBytes(e.Total)
		}
		s.set("Downloading knapsack " + e.Version + ", " + got)
	case app.EventWarned:
		s.mu.Lock()
		defer s.mu.Unlock()
		s.line.erase()
		_, _ = fmt.Fprintln(s.line.w, warning(e))
	case app.EventInstalled, app.EventRemoved, app.EventSkipped, app.EventChecked, app.EventFailed:
	}
}
