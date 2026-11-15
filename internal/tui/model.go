package tui

import (
	"context"
	"runtime"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// spinEvery is how often the spinner turns, as the CLI's does.
const spinEvery = 100 * time.Millisecond

// Spinner frames: braille dots, or ASCII on Windows, whose console fonts
// lack them, as in the CLI.
var (
	spinnerDots  = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerASCII = []string{"|", "/", "-", `\`}
)

// job is one app call, run in the background. Jobs run one at a time, in
// the order started, because profile locks are file locks: a second lock
// from this process would fail as another process's does (DESIGN.md §9.4).
type job struct {
	kind    jobKind
	profile string
	// name says what's running, for the status line and quitting:
	// "checking".
	name string
	// writes is whether it changes anything, so quitting asks first.
	writes bool
	run    func(ctx context.Context) tea.Msg
}

// jobKind is what a job does, for the status line and for not queueing
// the same work twice.
type jobKind int

const (
	jobOther jobKind = iota
	jobLoad
	jobCheck
	jobInfo
)

// jobDoneMsg is a job's result, delivered when it finishes.
type jobDoneMsg struct{ msg tea.Msg }

// loadedMsg is a profile's rows from config, state and AddOns, without
// the network.
type loadedMsg struct {
	noProfiles bool
	profile    string
	path       string
	listing    app.Listing
	err        error
}

// checkedMsg ends a check: every row, as checked, or why the check
// stopped.
type checkedMsg struct {
	profile string
	listing app.Listing
	err     error
}

// infoMsg is one addon's details, for describe.
type infoMsg struct {
	profile string
	ref     ref.Ref
	details app.Details
	err     error
}

type tickMsg struct{}

// known is what the network said about one addon, kept apart from the
// rows so a reload never forgets a check (DESIGN.md §9.4).
type known struct {
	status    app.Status
	available string
	err       error
}

func knownFrom(row app.Installed) known {
	return known{status: row.Status, available: row.Available, err: row.Err}
}

type model struct {
	// ctx is the jobs' context, which cancel cancels when quitting.
	ctx    context.Context
	cancel context.CancelFunc
	a      backend
	opts   Options
	bus    *bus
	now    func() time.Time
	// spin is the spinner's frames, and tickEvery how often it turns;
	// zero in tests, which draw single frames.
	spin      []string
	frame     int
	tickEvery time.Duration
	ticking   bool

	width, height int

	// The profile shown: loaded once its first list arrives.
	loaded     bool
	noProfiles bool
	loadErr    error
	profile    string
	path       string
	rows       []app.Installed
	hidden     int // unmanaged folders, not shown

	known    map[string]map[ref.Ref]known
	checking map[ref.Ref]bool
	checked  map[string]bool // profiles checked this session
	// err is why the last job failed, for the status line.
	err error

	cursor, offset int
	filter         textinput.Model
	filtering      bool   // the filter's input is open
	query          string // the filter applied

	describing bool
	// bodyOffset is how far describe or help is scrolled.
	bodyOffset  int
	describeRef ref.Ref
	details     *app.Details // nil until Info returns
	infoErr     error        // why Info failed
	help        bool
	confirmQuit bool
	quitting    bool

	queue   []job
	running *job
}

func newModel(ctx context.Context, a backend, opts Options, b *bus) *model {
	spin := spinnerDots
	if runtime.GOOS == "windows" {
		spin = spinnerASCII
	}
	f := textinput.New()
	f.Prompt = "/"
	ctx, cancel := context.WithCancel(ctx)
	return &model{
		ctx: ctx, cancel: cancel, a: a, opts: opts, bus: b,
		now:       time.Now,
		spin:      spin,
		tickEvery: spinEvery,
		profile:   opts.Profile,
		known:     map[string]map[ref.Ref]known{},
		checking:  map[ref.Ref]bool{},
		checked:   map[string]bool{},
		filter:    f,
	}
}

func (m *model) Init() tea.Cmd {
	return m.enqueue(m.loadJob())
}

// enqueue adds j to the queue, starting it if nothing is running.
func (m *model) enqueue(j job) tea.Cmd {
	m.queue = append(m.queue, j)
	return m.next()
}

// next starts the first queued job, if none is running.
func (m *model) next() tea.Cmd {
	if m.running != nil || len(m.queue) == 0 || m.quitting {
		return nil
	}
	j := m.queue[0]
	m.queue = m.queue[1:]
	m.running = &j
	ctx := m.ctx
	return tea.Batch(func() tea.Msg { return jobDoneMsg{j.run(ctx)} }, m.startTick())
}

// pending reports whether a job of kind for the profile is running or
// queued.
func (m *model) pending(kind jobKind, profile string) bool {
	if m.running != nil && m.running.kind == kind && m.running.profile == profile {
		return true
	}
	for _, j := range m.queue {
		if j.kind == kind && j.profile == profile {
			return true
		}
	}
	return false
}

// dropQueued takes queued jobs of kind off the queue, as an Info whose
// describe was closed. A running one finishes; its result is ignored.
func (m *model) dropQueued(kind jobKind) {
	kept := m.queue[:0]
	for _, j := range m.queue {
		if j.kind != kind {
			kept = append(kept, j)
		}
	}
	m.queue = kept
}

// busy is whether anything is in progress, which the spinner shows.
func (m *model) busy() bool { return m.running != nil || len(m.checking) > 0 }

func (m *model) startTick() tea.Cmd {
	if m.ticking || m.tickEvery == 0 {
		return nil
	}
	m.ticking = true
	return tea.Tick(m.tickEvery, func(time.Time) tea.Msg { return tickMsg{} })
}

// loadJob lists the profile from config, state and AddOns: the instant
// rows, before any check. With no profile chosen, it's the current one.
func (m *model) loadJob() job {
	a, want := m.a, m.profile
	return job{kind: jobLoad, profile: want, name: "loading", run: func(ctx context.Context) tea.Msg {
		profiles, err := a.Profiles(ctx)
		if err != nil {
			return loadedMsg{err: err}
		}
		if len(profiles) == 0 {
			return loadedMsg{noProfiles: true}
		}
		name := want
		if name == "" {
			if name, err = a.CurrentProfile(ctx); err != nil {
				return loadedMsg{err: err}
			}
		}
		msg := loadedMsg{profile: name}
		for _, p := range profiles {
			if p.Name == name {
				msg.path = p.Path
			}
		}
		// An unknown profile is List's error, with its hint.
		msg.listing, msg.err = a.List(ctx, app.Selector{Profiles: []string{name}}, app.ListOptions{})
		return msg
	}}
}

// checkJob asks each addon's source for its newest release, as list
// does, with each row sent as it's finished.
func (m *model) checkJob() job {
	a, b, profile := m.a, m.bus, m.profile
	return job{kind: jobCheck, profile: profile, name: "checking", run: func(ctx context.Context) tea.Msg {
		l, err := a.List(ctx, app.Selector{Profiles: []string{profile}},
			app.ListOptions{Check: true, Watch: watcher{bus: b, profile: profile}})
		return checkedMsg{profile: profile, listing: l, err: err}
	}}
}

// infoJob reads an addon's details for describe.
func (m *model) infoJob(r ref.Ref) job {
	a, profile := m.a, m.profile
	return job{kind: jobInfo, profile: profile, name: "reading", run: func(ctx context.Context) tea.Msg {
		d, err := a.Info(ctx, profile, r.String())
		return infoMsg{profile: profile, ref: r, details: d, err: err}
	}}
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.SetWidth(max(1, m.width-3))
		m.scroll()
		return m, nil
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tickMsg:
		m.frame++
		m.ticking = false
		if m.busy() {
			return m, m.startTick()
		}
		return m, nil
	case jobDoneMsg:
		m.running = nil
		// The job the quit question was about is over: nothing is left
		// to cancel.
		m.confirmQuit = false
		cmd := m.result(msg.msg)
		if m.quitting {
			return m, tea.Quit
		}
		return m, tea.Batch(cmd, m.next())
	case listedMsg:
		if msg.profile == m.profile {
			m.rows = msg.rows
			for i, row := range msg.rows {
				if row.Status == app.StatusChecking {
					// Only m.checking says a row is in progress, so a
					// check that stops leaves none spinning.
					m.rows[i].Status = app.StatusInstalled
					m.checking[row.Ref] = true
				} else {
					m.knownFor(m.profile)[row.Ref] = knownFrom(row)
				}
			}
			m.scroll()
		}
		return m, m.startTick()
	case finishedMsg:
		if msg.profile == m.profile {
			m.knownFor(m.profile)[msg.row.Ref] = knownFrom(msg.row)
			delete(m.checking, msg.row.Ref)
		}
		return m, nil
	}
	return m, nil
}

// result handles a finished job's result.
func (m *model) result(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case loadedMsg:
		m.loaded = true
		m.noProfiles, m.loadErr = msg.noProfiles, msg.err
		if msg.noProfiles || msg.err != nil {
			m.rows, m.hidden = nil, 0
			return nil
		}
		m.err = nil
		m.profile, m.path = msg.profile, msg.path
		m.rows, m.hidden = msg.listing.Addons, msg.listing.Hidden
		m.forgetGone()
		m.scroll()
		if !m.checked[m.profile] {
			m.checked[m.profile] = true
			return m.enqueue(m.checkJob())
		}
	case checkedMsg:
		clear(m.checking)
		if msg.profile != m.profile {
			return nil
		}
		m.err = msg.err
		if msg.err == nil {
			m.rows, m.hidden = msg.listing.Addons, msg.listing.Hidden
			for _, row := range msg.listing.Addons {
				m.knownFor(m.profile)[row.Ref] = knownFrom(row)
			}
			m.scroll()
		}
	case infoMsg:
		if msg.profile == m.profile && msg.ref == m.describeRef {
			// Failed, the TOC's fields are just unknown.
			m.details, m.infoErr = &msg.details, msg.err
		}
	}
	return nil
}

func (m *model) knownFor(profile string) map[ref.Ref]known {
	k := m.known[profile]
	if k == nil {
		k = map[ref.Ref]known{}
		m.known[profile] = k
	}
	return k
}

// forgetGone forgets what's known about addons no longer in the profile,
// as after an edit or a CLI command.
func (m *model) forgetGone() {
	in := map[ref.Ref]bool{}
	for _, row := range m.rows {
		in[row.Ref] = true
	}
	k := m.knownFor(m.profile)
	for r := range k {
		if !in[r] {
			delete(k, r)
		}
	}
}

// recheck forgets what's known about the profile and checks it again,
// after reading it afresh. A profile that didn't load is only reloaded;
// once it does, it's checked as any is the first time.
func (m *model) recheck() tea.Cmd {
	if m.noProfiles || m.loadErr != nil || m.profile == "" {
		return m.enqueue(m.loadJob())
	}
	if m.pending(jobCheck, m.profile) {
		return nil // pressing c again doesn't queue another
	}
	delete(m.known, m.profile)
	m.checked[m.profile] = true
	return tea.Batch(m.enqueue(m.loadJob()), m.enqueue(m.checkJob()))
}

// key handles a key press: in a modal, the filter or help when one is
// open, else in the view; global keys are tried last, so the filter can
// type q and digits (DESIGN.md §9.3).
func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	switch {
	case m.quitting:
		// Waiting for a cancelled job to roll back; another Ctrl-C quits
		// at once, as the CLI's second one does (DESIGN.md §9.6).
		if s == "ctrl+c" {
			return tea.Quit
		}
		return nil
	case m.confirmQuit:
		switch s {
		case "y", "Y", "enter", "ctrl+c":
			return m.quit()
		case "n", "N", "esc":
			m.confirmQuit = false
		}
		return nil
	case m.filtering:
		switch s {
		case "ctrl+c":
			return m.globalKey(s)
		case "esc":
			m.filtering, m.query = false, ""
			m.filter.Blur()
			m.filter.SetValue("")
		case "enter":
			m.filtering = false
			m.filter.Blur()
		default:
			var cmd tea.Cmd
			m.filter, cmd = m.filter.Update(k)
			m.query = m.filter.Value()
			m.cursor, m.offset = 0, 0
			return cmd
		}
		m.scroll()
		return nil
	case m.help:
		switch s {
		case "esc", "?":
			m.help = false
			return nil
		}
		if m.scrollBody(s) {
			return nil
		}
	case m.describing:
		if s == "esc" {
			m.describing, m.details = false, nil
			m.dropQueued(jobInfo)
			return nil
		}
		if m.scrollBody(s) {
			return nil
		}
	default:
		if cmd, ok := m.addonsKey(s); ok {
			return cmd
		}
	}
	return m.globalKey(s)
}

// addonsKey handles the addons view's own keys.
func (m *model) addonsKey(s string) (tea.Cmd, bool) {
	rows := m.visible()
	switch s {
	case "enter":
		if len(rows) == 0 {
			return nil, true
		}
		r := rows[m.cursor].Ref
		m.describing, m.describeRef, m.details, m.infoErr = true, r, nil, nil
		m.bodyOffset = 0
		m.dropQueued(jobInfo) // only this addon's details are wanted now
		return m.enqueue(m.infoJob(r)), true
	case "c":
		return m.recheck(), true
	case "/":
		m.filtering = true
		m.filter.SetValue(m.query)
		m.filter.CursorEnd()
		return m.filter.Focus(), true
	case "esc":
		if m.query != "" {
			m.query = ""
			m.filter.SetValue("")
			m.scroll()
		}
		return nil, true
	case "j", "down":
		m.cursor++
	case "k", "up":
		m.cursor--
	case "g", "home":
		m.cursor = 0
	case "G", "end":
		m.cursor = len(rows) - 1
	case "ctrl+f", "pgdown":
		m.cursor += m.pageSize()
	case "ctrl+b", "pgup":
		m.cursor -= m.pageSize()
	default:
		return nil, false
	}
	m.scroll()
	return nil, true
}

func (m *model) globalKey(s string) tea.Cmd {
	switch s {
	case "?":
		m.help, m.bodyOffset = true, 0
	case "q", "ctrl+c":
		if m.running != nil && m.running.writes {
			m.confirmQuit = true
			return nil
		}
		return m.quit()
	}
	return nil
}

// quit cancels whatever is running and quits once it has stopped, so an
// install in progress rolls back first.
func (m *model) quit() tea.Cmd {
	m.confirmQuit = false
	if m.running == nil || !m.running.writes {
		m.cancel() // a read is just abandoned
		return tea.Quit
	}
	m.quitting = true
	m.queue = nil
	m.cancel()
	return nil
}

// scrollBody scrolls describe or help, which can be taller than the
// box, and reports whether s was a key that does.
func (m *model) scrollBody(s string) bool {
	switch s {
	case "j", "down":
		m.bodyOffset++
	case "k", "up":
		m.bodyOffset--
	case "g", "home":
		m.bodyOffset = 0
	case "G", "end":
		m.bodyOffset = m.maxBodyOffset()
	case "ctrl+f", "pgdown":
		m.bodyOffset += m.bodyHeight()
	case "ctrl+b", "pgup":
		m.bodyOffset -= m.bodyHeight()
	default:
		return false
	}
	m.bodyOffset = max(0, min(m.bodyOffset, m.maxBodyOffset()))
	return true
}

// scroll keeps the cursor on a row and in view.
func (m *model) scroll() {
	n := len(m.visible())
	m.cursor = max(0, min(m.cursor, n-1))
	page := m.pageSize()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if page > 0 && m.cursor >= m.offset+page {
		m.offset = m.cursor - page + 1
	}
	m.offset = max(0, min(m.offset, n-page))
}
