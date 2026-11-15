package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/austinrr/knapsack/internal/app"
)

// liveList draws list's table on a terminal before its addons are checked,
// and redraws it as each check finishes, with a spinner for those still
// being checked (DESIGN.md §8.6). It's the app.ListWatcher, and the App's
// reporter while it runs, so a warning prints above the table rather than
// through it. stop erases the table, so the finished one is printed as it
// would be anywhere else.
//
// The table is redrawn by moving the cursor back up over it, so every line
// it draws must fit on one terminal line, and the whole table on the
// screen: lines are cut to the terminal's width, and rows that don't fit
// are counted on a last line instead.
type liveList struct {
	mu     sync.Mutex
	w      io.Writer // the terminal
	errw   io.Writer // for warnings; usually the same terminal
	size   func() (width, height int, err error)
	header string // a line above the table, or ""
	all    bool   // -A: a PROFILE column
	wide   bool   // -o wide

	spinner []string
	frame   int
	rows    []app.Installed
	first   map[string]int // the index in rows of each profile's first
	drawn   int            // lines on the screen now

	quit, done chan struct{}
	stopOnce   sync.Once
	restore    func() // undoes enableVT
}

// Spinner frames: braille dots, or ASCII for the Windows console, whose
// default fonts lack them.
var (
	spinnerDots  = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerASCII = []string{"|", "/", "-", `\`}
)

// spinEvery is how often the spinner turns.
const spinEvery = 100 * time.Millisecond

// startLiveList starts a liveList on cmd's stdout and makes it the App's
// reporter, if stdout is a terminal that can show one: not with --verbose,
// which prints every step, or TERM=dumb. It returns nil when it can't.
func (c *cli) startLiveList(cmd *cobra.Command, header string, all, wide bool) *liveList {
	f, ok := cmd.OutOrStdout().(*os.File)
	if !ok || c.verbose || !isTerminal(f) || c.env.getenv("TERM") == "dumb" {
		return nil
	}
	size := func() (int, int, error) { return term.GetSize(int(f.Fd())) }
	if _, _, err := size(); err != nil {
		return nil
	}
	restore, ok := enableVT(f)
	if !ok {
		return nil
	}
	spinner := spinnerDots
	if runtime.GOOS == "windows" {
		spinner = spinnerASCII
	}
	l := newLiveList(f, cmd.ErrOrStderr(), size, header, all, wide, spinner)
	l.restore = restore
	// The bar would draw on the same terminal. It has drawn nothing yet,
	// as nothing has been resolved.
	c.progress.swap(l).stop()
	l.quit, l.done = make(chan struct{}), make(chan struct{})
	go l.spin()
	return l
}

func newLiveList(w, errw io.Writer, size func() (int, int, error), header string, all, wide bool, spinner []string) *liveList {
	return &liveList{w: w, errw: errw, size: size, header: header, all: all, wide: wide, spinner: spinner, first: map[string]int{}}
}

// spin turns the spinner until stop.
func (l *liveList) spin() {
	defer close(l.done)
	t := time.NewTicker(spinEvery)
	defer t.Stop()
	for {
		select {
		case <-l.quit:
			return
		case <-t.C:
			l.mu.Lock()
			l.frame = (l.frame + 1) % len(l.spinner)
			if slices.ContainsFunc(l.rows, func(in app.Installed) bool { return in.Status == app.StatusChecking }) {
				l.draw()
			}
			l.mu.Unlock()
		}
	}
}

// Listed implements app.ListWatcher.
func (l *liveList) Listed(addons []app.Installed) {
	if len(addons) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.first[addons[0].Profile] = len(l.rows)
	l.rows = append(l.rows, addons...)
	l.draw()
}

// Checked implements app.ListWatcher.
func (l *liveList) Checked(profile string, i int, in app.Installed) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.rows[l.first[profile]+i] = in
	l.draw()
}

// Event implements app.Reporter. Only warnings are shown: the spinners
// are the progress.
func (l *liveList) Event(e app.Event) {
	if e.Kind != app.EventWarned {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.erase()
	_, _ = fmt.Fprintln(l.errw, warning(e)) // progress; nothing to fail for
	l.draw()
}

// stop erases the table and stops the spinner. It's safe to call more
// than once.
func (l *liveList) stop() {
	l.stopOnce.Do(func() {
		if l.quit != nil {
			close(l.quit)
			<-l.done
		}
		l.mu.Lock()
		defer l.mu.Unlock()
		l.erase()
		if l.restore != nil {
			l.restore()
		}
	})
}

// lines is the table as it's drawn now: the header, then printList's
// table with a spinner for each addon being checked, fitted to the
// terminal.
func (l *liveList) lines() []string {
	shown := slices.Clone(l.rows)
	for i := range shown {
		if shown[i].Status == app.StatusChecking {
			shown[i].Status = app.Status(l.spinner[l.frame] + " checking")
		}
	}
	var buf bytes.Buffer
	if l.header != "" {
		buf.WriteString(l.header + "\n")
	}
	_ = printList(&buf, shown, l.all, l.wide) // a bytes.Buffer can't fail
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	width, height, err := l.size()
	if err != nil {
		width, height = 0, 0 // unknown: draw it all
	}
	return fit(lines, width, height)
}

// fit cuts lines to a terminal of width and height, so none wraps and the
// cursor can move back up over all of them: one column short of the
// width, since writing the last column wraps in some terminals, and one
// line short of the height, for the cursor's line below. Lines that don't
// fit are counted on the last one instead. A zero width or height isn't
// fitted.
func fit(lines []string, width, height int) []string {
	if height > 1 && len(lines) > height-1 {
		keep := max(height-2, 0)
		lines = append(lines[:keep:keep], fmt.Sprintf("… %d more", len(lines)-keep))
	}
	if width > 1 {
		for i, line := range lines {
			if r := []rune(line); len(r) > width-1 {
				lines[i] = string(r[:width-1])
			}
		}
	}
	return lines
}

// draw writes the table over the one on screen. It's called with mu held.
func (l *liveList) draw() {
	if len(l.rows) == 0 {
		return
	}
	var b strings.Builder
	l.up(&b)
	lines := l.lines()
	for _, line := range lines {
		b.WriteString(line + "\x1b[K\n") // clearing what's left of a longer line
	}
	b.WriteString("\x1b[J") // and any lines below, from a longer table
	_, _ = io.WriteString(l.w, b.String())
	l.drawn = len(lines)
}

// erase clears the table, leaving the cursor where it started. It's
// called with mu held.
func (l *liveList) erase() {
	if l.drawn == 0 {
		return
	}
	var b strings.Builder
	l.up(&b)
	b.WriteString("\x1b[J")
	_, _ = io.WriteString(l.w, b.String())
	l.drawn = 0
}

// up moves the cursor to the start of the table on screen.
func (l *liveList) up(b *strings.Builder) {
	if l.drawn > 0 {
		fmt.Fprintf(b, "\r\x1b[%dA", l.drawn)
	}
}
