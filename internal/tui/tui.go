package tui

import (
	"context"
	"errors"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/austinrr/knapsack/internal/app"
)

// Options are what the TUI needs from main, which builds the App
// (DESIGN.md §9.3).
type Options struct {
	// NewApp builds the App with the TUI's Reporter, which must exist
	// before the App does (app.Options.Reporter).
	NewApp func(app.Reporter) *app.App
	// Profile is the profile to open, from -p or $KNAPSACK_PROFILE; ""
	// opens the current one. Opening one never makes it current.
	Profile string
	// Home is the user's home directory, for showing paths with ~.
	Home string
	// Running is the Knapsack running, "v0.2.0 on linux/amd64", for an
	// error's report link (app.ReportURL).
	Running string
}

// backend is what the TUI calls on the App, so tests can use a fake.
// *app.App satisfies it.
type backend interface {
	CurrentProfile(ctx context.Context) (string, error)
	Profiles(ctx context.Context) ([]app.ProfileInfo, error)
	List(ctx context.Context, sel app.Selector, opts app.ListOptions) (app.Listing, error)
	Info(ctx context.Context, profile, name string) (app.Details, error)
	Update(ctx context.Context, sel app.Selector, opts app.UpdateOptions) ([]app.Result, error)
	Remove(ctx context.Context, profile string, names []string) ([]app.Result, error)
}

// Run runs the TUI until the user quits or ctx is cancelled.
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b := &bus{}
	rep := newReporter(b)
	m := newModel(ctx, opts.NewApp(rep), opts, b, rep)
	p := tea.NewProgram(m, tea.WithContext(ctx))
	b.set(p.Send)
	_, err := p.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		// Killed by ctx: a signal, which says why better than Bubble Tea.
		return ctx.Err()
	}
	return err
}

// bus carries messages from jobs' goroutines to the program while they
// run, such as each addon a check finishes (Program.Send, which is safe
// from any goroutine). Tests collect them instead.
type bus struct {
	mu   sync.Mutex
	send func(tea.Msg)
}

func (b *bus) set(send func(tea.Msg)) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.send = send
}

// Send delivers msg, or drops it before the program has started, when no
// job can be running yet.
func (b *bus) Send(msg tea.Msg) {
	b.mu.Lock()
	send := b.send
	b.mu.Unlock()
	if send != nil {
		send(msg)
	}
}

// watcher passes a check's rows to the program as they're listed and
// finished (app.Watcher).
type watcher struct {
	bus     *bus
	profile string
}

// listedMsg is a profile's rows, before the network: those still to be
// checked have app.StatusChecking.
type listedMsg struct {
	profile string
	rows    []app.Installed
}

// finishedMsg is one row, now checked.
type finishedMsg struct {
	profile string
	row     app.Installed
}

func (w watcher) Listed(rows []app.Installed) {
	w.bus.Send(listedMsg{profile: w.profile, rows: append([]app.Installed(nil), rows...)})
}

func (w watcher) Finished(profile string, _ int, row app.Installed) {
	w.bus.Send(finishedMsg{profile: profile, row: row})
}
