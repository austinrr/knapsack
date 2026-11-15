package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"golang.org/x/term"
	"golang.org/x/text/width"

	"github.com/austinrr/knapsack/internal/app"
)

// liveTable draws a command's table on a terminal before the network is
// done with it, and redraws it as each row is finished, with a spinner in
// the rows still waiting (DESIGN.md §8.6). list and check use it, with
// rows of app.Installed and app.Plan. It's their app.Watcher, and the
// App's reporter while it runs, so a warning prints above the table rather
// than through it. stop erases it, so the finished table is printed as it
// would be anywhere else.
//
// The table is redrawn by moving the cursor back up over it, so every line
// it draws must fit on one terminal line, and the whole table on the
// screen: lines are cut to the terminal's width, and rows that don't fit
// are counted on a last line instead.
type liveTable[T any] struct {
	mu     sync.Mutex
	w      io.Writer // the terminal
	errw   io.Writer // for warnings; usually the same terminal
	size   func() (width, height int, err error)
	header string // a line above the table, or ""
	// render writes the table of rows to w, with spin for the spinner's
	// current frame; waiting says whether a row is still waiting.
	render  func(w io.Writer, rows []T, spin string) error
	waiting func(T) bool
	profile func(T) string // the profile a row is in

	spin    spinner
	rows    []T
	first   map[string]int // the index in rows of each profile's first
	drawn   int            // lines on the screen now
	dirty   bool           // a row changed since the last draw
	stopped bool

	tick     *ticker
	stopOnce sync.Once
	restore  func() // undoes enableVT
}

// liveTableFor is what startLiveTable needs to know about a table: as in
// liveTable.
type liveTableFor[T any] struct {
	header  string
	render  func(w io.Writer, rows []T, spin string) error
	waiting func(T) bool
	profile func(T) string
}

// startLiveTable starts a liveTable on cmd's stdout and makes it the
// App's reporter, if stdout can animate (canAnimate) and understands the
// escape sequences it draws with. It returns nil when it can't.
func startLiveTable[T any](c *cli, cmd *cobra.Command, spec liveTableFor[T]) *liveTable[T] {
	f, ok := cmd.OutOrStdout().(*os.File)
	if !ok || !canAnimate(f, c.verbose, c.env.getenv) {
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
	t := newLiveTable(f, cmd.ErrOrStderr(), size, spec)
	t.restore = restore
	// The bar would draw on the same terminal. It has drawn nothing yet,
	// as nothing has been resolved.
	c.progress.swap(t).stop()
	t.tick = startTicker(spinEvery, t.turn)
	return t
}

func newLiveTable[T any](w, errw io.Writer, size func() (int, int, error), spec liveTableFor[T]) *liveTable[T] {
	return &liveTable[T]{
		w: w, errw: errw, size: size, header: spec.header, render: spec.render, waiting: spec.waiting, profile: spec.profile,
		spin: newSpinner(), first: map[string]int{},
	}
}

// turn advances the spinner while any row is waiting, and draws what
// changed since the last turn. Drawing here, not as each row finishes,
// keeps a burst of answers to one redraw a tick.
func (t *liveTable[T]) turn() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	waiting := slices.ContainsFunc(t.rows, t.waiting)
	if waiting {
		t.spin.next()
	}
	if waiting || t.dirty {
		t.draw()
	}
}

// Listed implements app.Watcher. Every row is the profile's.
func (t *liveTable[T]) Listed(rows []T) {
	if len(rows) == 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.first[t.profile(rows[0])] = len(t.rows)
	t.rows = append(t.rows, rows...)
	t.draw()
}

// Finished implements app.Watcher.
func (t *liveTable[T]) Finished(profile string, i int, row T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stopped {
		return
	}
	t.rows[t.first[profile]+i] = row
	t.dirty = true // drawn on the next turn
}

// Event implements app.Reporter. Only warnings are shown: the spinners
// are the progress.
func (t *liveTable[T]) Event(e app.Event) {
	if e.Kind != app.EventWarned {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.erase()
	_, _ = fmt.Fprintln(t.errw, warning(e)) // progress; nothing to fail for
	if !t.stopped {
		// After stop, the finished table is printing, so this one stays
		// erased.
		t.draw()
	}
}

// stop erases the table and stops the spinner. It's safe to call more
// than once.
func (t *liveTable[T]) stop() {
	t.stopOnce.Do(func() {
		t.tick.stop() // before mu, since a turn in progress takes it
		t.mu.Lock()
		defer t.mu.Unlock()
		t.stopped = true
		t.erase()
		if t.restore != nil {
			t.restore()
		}
	})
}

// lines is the table as it's drawn now: the header, then the rendered
// table, fitted to the terminal.
func (t *liveTable[T]) lines() []string {
	var buf bytes.Buffer
	if t.header != "" {
		buf.WriteString(t.header + "\n")
	}
	_ = t.render(&buf, t.rows, t.spin.String()) // a bytes.Buffer can't fail
	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	width, height, err := t.size()
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
			lines[i] = cut(line, width-1)
		}
	}
	return lines
}

// cut shortens line to at most n columns on screen. CJK characters, as
// in a zhCN addon's title, take two columns.
func cut(line string, n int) string {
	cols := 0
	for i, r := range line {
		w := 1
		if k := width.LookupRune(r).Kind(); k == width.EastAsianWide || k == width.EastAsianFullwidth {
			w = 2
		}
		if cols+w > n {
			return line[:i]
		}
		cols += w
	}
	return line
}

// draw writes the table over the one on screen. It's called with mu held.
func (t *liveTable[T]) draw() {
	if len(t.rows) == 0 {
		return
	}
	var b strings.Builder
	t.up(&b)
	lines := t.lines()
	for _, line := range lines {
		b.WriteString(line + "\x1b[K\n") // clearing what's left of a longer line
	}
	b.WriteString("\x1b[J") // and any lines below, from a longer table
	_, _ = io.WriteString(t.w, b.String())
	t.drawn, t.dirty = len(lines), false
}

// erase clears the table, leaving the cursor where it started. It's
// called with mu held.
func (t *liveTable[T]) erase() {
	if t.drawn == 0 {
		return
	}
	var b strings.Builder
	t.up(&b)
	b.WriteString("\x1b[J")
	_, _ = io.WriteString(t.w, b.String())
	t.drawn = 0
}

// up moves the cursor to the start of the table on screen.
func (t *liveTable[T]) up(b *strings.Builder) {
	if t.drawn > 0 {
		fmt.Fprintf(b, "\r\x1b[%dA", t.drawn)
	}
}

// liveList is list's table, with "⠋ checking" for the addons being
// checked.
func liveList(header string, all, wide bool) liveTableFor[app.Installed] {
	return liveTableFor[app.Installed]{
		header: header,
		render: func(w io.Writer, rows []app.Installed, spin string) error {
			shown := slices.Clone(rows)
			for i := range shown {
				if shown[i].Status == app.StatusChecking {
					shown[i].Status = app.Status(spin + " checking")
				}
			}
			return printList(w, shown, all, wide)
		},
		waiting: func(in app.Installed) bool { return in.Status == app.StatusChecking },
		profile: func(in app.Installed) string { return in.Profile },
	}
}

// liveCheck is check's table, with "⠋ checking" as the action for the
// addons being resolved.
func liveCheck(all, wide bool) liveTableFor[app.Plan] {
	return liveTableFor[app.Plan]{
		render: func(w io.Writer, rows []app.Plan, spin string) error {
			shown := slices.Clone(rows)
			for i := range shown {
				if shown[i].Action == app.PlanChecking {
					shown[i].Action = app.PlanAction(spin + " checking")
				}
			}
			out := &printer{w: w}
			printPlans(out, shown, all, wide)
			return out.err
		},
		waiting: func(p app.Plan) bool { return p.Action == app.PlanChecking },
		profile: func(p app.Plan) string { return p.Profile },
	}
}
