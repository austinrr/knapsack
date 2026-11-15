package tui

import (
	"context"
	"sync"

	tea "charm.land/bubbletea/v2"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
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
	// Edit runs the CLI's edit loop on a profile (profiles edit, DESIGN.md
	// §8.4), on the terminal, which the TUI has given up meanwhile.
	// saved is false when the edit was cancelled.
	Edit func(ctx context.Context, profile string) (saved bool, res app.ProfileEditResult, err error)
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
	Add(ctx context.Context, profile string, refs []ref.Ref, force bool) ([]app.Result, error)
	UseProfile(ctx context.Context, name string) error
	SetProfile(ctx context.Context, req app.SetProfileRequest) (app.SetProfileResult, error)
	AutoProfiles(ctx context.Context, game string) (app.AutoProfilesResult, error)
	DeleteProfile(ctx context.Context, name string) (app.DeleteProfileResult, error)
}

// Run runs the TUI until the user quits. ctx's values are used, but not
// its cancellation: main cancels it on SIGINT, which, while the TUI has the
// terminal in raw mode, only reaches it from outside, and which a Ctrl-C
// typed in the editor sends too. Bubble Tea handles SIGINT and SIGTERM
// itself, and ignores them while the editor has the terminal.
func Run(ctx context.Context, opts Options) error {
	ctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	b := &bus{}
	rep := newReporter(b)
	m := newModel(ctx, opts.NewApp(rep), opts, b, rep)
	p := tea.NewProgram(m, tea.WithContext(ctx), tea.WithFilter(quitFilter))
	b.set(p.Send)
	_, err := p.Run()
	if err == nil && m.signaled {
		// Quit on a signal from outside, once any job rolled back: not
		// a success.
		return context.Canceled
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
