package tui

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

var updateGolden = flag.Bool("update", false, "rewrite golden files in testdata/")

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// fake is the App, with a profile's rows before and after a check.
type fake struct {
	profiles []app.ProfileInfo
	current  string
	hidden   int
	local    []app.Installed // List without a check
	checked  []app.Installed // List with one; nil checks nothing
	details  map[ref.Ref]app.Details
	listErr  error
	checkErr error
	infoErr  error
	calls    []string
}

func (f *fake) CurrentProfile(context.Context) (string, error) {
	if f.current == "" {
		return "", &app.UserError{Msg: "no current profile", Hint: "run `knapsack profiles use <name>`"}
	}
	return f.current, nil
}

func (f *fake) Profiles(context.Context) ([]app.ProfileInfo, error) {
	return f.profiles, nil
}

func (f *fake) List(_ context.Context, sel app.Selector, opts app.ListOptions) (app.Listing, error) {
	p := sel.Profiles[0]
	if !opts.Check {
		f.calls = append(f.calls, "list "+p)
		return app.Listing{Addons: f.local, Hidden: f.hidden}, f.listErr
	}
	f.calls = append(f.calls, "check "+p)
	pending := make([]app.Installed, len(f.local))
	for i, r := range f.local {
		if r.Status == app.StatusInstalled {
			r.Status = app.StatusChecking
		}
		pending[i] = r
	}
	opts.Watch.Listed(pending)
	if f.checkErr != nil {
		return app.Listing{}, f.checkErr
	}
	for i, r := range f.checked {
		opts.Watch.Finished(p, i, r)
	}
	return app.Listing{Addons: f.checked, Hidden: f.hidden}, nil
}

func (f *fake) Info(_ context.Context, profile, name string) (app.Details, error) {
	f.calls = append(f.calls, "info "+profile+" "+name)
	r, _ := ref.Parse(name)
	if f.infoErr != nil {
		return app.Details{}, f.infoErr
	}
	return f.details[r], nil
}

// harness drives a model as Bubble Tea would, running each command at
// once and delivering what jobs send on the bus before their results.
// With hold, commands are kept to run later instead, so a test can look
// at the screen while a job is in progress.
type harness struct {
	t       *testing.T
	m       *model
	f       *fake
	sent    []tea.Msg
	hold    bool
	pending []tea.Cmd
	quit    bool
}

func newHarness(t *testing.T, f *fake, opts Options) *harness {
	t.Helper()
	h := &harness{t: t, f: f}
	b := &bus{}
	b.set(func(msg tea.Msg) { h.sent = append(h.sent, msg) })
	if opts.Running == "" {
		opts.Running = "v0.0.0-test on testos/testarch"
	}
	h.m = newModel(context.Background(), f, opts, b)
	h.m.tickEvery = 0
	h.m.spin = spinnerDots // the same frames on every OS
	h.m.now = func() time.Time { return now }
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.run(h.m.Init())
	return h
}

func (h *harness) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if h.hold {
		h.pending = append(h.pending, cmd)
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			h.run(c)
		}
		return
	}
	if _, ok := msg.(tea.QuitMsg); ok {
		h.quit = true
		return
	}
	sent := h.sent
	h.sent = nil
	for _, s := range sent {
		h.send(s)
	}
	if msg != nil {
		h.send(msg)
	}
}

// release runs the commands held so far.
func (h *harness) release() {
	h.hold = false
	pending := h.pending
	h.pending = nil
	for _, c := range pending {
		h.run(c)
	}
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.m.Update(msg)
	h.run(cmd)
}

// keys presses each key in turn: a character, or a name such as
// "enter", "esc" or "ctrl+c".
func (h *harness) keys(keys ...string) {
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "backspace":
			msg = tea.KeyPressMsg{Code: tea.KeyBackspace}
		case "ctrl+c":
			msg = tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		h.send(msg)
	}
}

// golden compares the screen, without its colors, with
// testdata/<name>.golden.
func (h *harness) golden(name string) {
	h.t.Helper()
	lines := strings.Split(ansi.Strip(h.m.render()), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " ")
	}
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll("testdata", 0o750); err != nil {
			h.t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o600); err != nil {
			h.t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		h.t.Fatalf("%v (run go test ./internal/tui -update to create it)", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		h.t.Errorf("screen %s (-want +got):\n%s", name, diff)
	}
}

var (
	baganator = ref.Ref{Source: ref.GitHub, ID: "austinrr/baganator-mirror"}
	details   = ref.Ref{Source: ref.Wago, ID: "VBNBxKx5"}
	plater    = ref.Ref{Source: ref.Wago, ID: "plater"}
	leatrix   = ref.Ref{Source: ref.GitHub, ID: "leatrix/leatrix-maps"}
	weak      = ref.Ref{Source: ref.CurseForge, ID: "weakauras-2"}
)

func forever() []app.ProfileInfo {
	return []app.ProfileInfo{{
		Name: "forever", Current: true,
		Path: `C:\Program Files (x86)\World of Warcraft\_forever_\Interface\AddOns`,
	}}
}

// sample is a profile with an addon in each state, before and after a
// check: one with an update, one up to date, one whose check failed, one
// not installed and one only in state.
func sample() *fake {
	at := now.Add(-50 * time.Hour)
	installed := func(r ref.Ref, title, version string, folders ...string) app.Installed {
		return app.Installed{Profile: "forever", Ref: r, Title: title, Status: app.StatusInstalled,
			Version: version, InstalledAt: at, Folders: folders}
	}
	local := []app.Installed{
		installed(baganator, "Baganator", "851", "Baganator", "Baganator_Categories"),
		installed(details, "Details!", "11.2.0", "Details"),
		installed(plater, "Plater", "610", "Plater"),
		{Profile: "forever", Ref: leatrix, Status: app.StatusMissing},
		installed(weak, "WeakAuras", "5.17.1", "WeakAuras"),
	}
	local[4].Status = app.StatusNotInConfig
	checked := append([]app.Installed(nil), local...)
	checked[0].Status, checked[0].Available = app.StatusUpdateAvailable, "854"
	checked[1].Status, checked[1].Available = app.StatusUpToDate, "11.2.0"
	checked[2].Status = app.StatusUnknown
	checked[2].Err = &app.UserError{
		Msg:  "wago plater: rate limited",
		Hint: "wait a minute and press c to check again",
	}
	return &fake{
		profiles: forever(), current: "forever", hidden: 23,
		local: local, checked: checked,
		details: map[ref.Ref]app.Details{baganator: {
			Author: "plusmouse", Notes: "Bag and bank replacement with sorting, categories and search",
			Interfaces: []int{110205, 50500},
		}},
	}
}

func TestChecked(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.golden("checked")
	if diff := cmp.Diff([]string{"list forever", "check forever"}, h.f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// While a check runs, rows not yet checked say so, and the status counts
// them.
func TestChecking(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	// A check that has listed its rows and finished one, as c starts.
	clear(h.m.known)
	pending := append([]app.Installed(nil), f.local...)
	for i := range 3 {
		pending[i].Status = app.StatusChecking
	}
	h.send(listedMsg{profile: "forever", rows: pending})
	h.send(finishedMsg{profile: "forever", row: f.checked[1]})
	h.golden("checking")
	// The rest, as they finish.
	h.send(finishedMsg{profile: "forever", row: f.checked[0]})
	h.send(finishedMsg{profile: "forever", row: f.checked[2]})
	h.golden("checked")
}

// When Info fails, describe shows the TOC's fields as unknown and the
// error in the status line.
func TestDescribeInfoError(t *testing.T) {
	t.Parallel()
	f := sample()
	f.infoErr = errors.New("read Baganator.toc: permission denied")
	h := newHarness(t, f, Options{})
	h.keys("enter")
	h.golden("describe_info_error")
}

// A row whose check failed shows its error, hint and status in describe,
// and the TOC's details once Info returns.
func TestDescribe(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("enter")
	h.golden("describe")
	h.keys("esc", "j", "j", "enter")
	h.golden("describe_failed")
	h.keys("esc")
	h.golden("checked")
}

func TestFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("/", "w", "a", "g")
	h.golden("filter_typing")
	h.keys("enter")
	h.golden("filter_kept")
	// Typing q into the filter is the filter's, not quitting.
	h.keys("/", "q")
	if h.quit {
		t.Fatal("q in the filter quit")
	}
	h.keys("esc")
	h.golden("checked")
}

func TestHelp(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("?")
	h.golden("help")
	h.keys("esc")
	h.golden("checked")
}

func TestEmptyProfile(t *testing.T) {
	t.Parallel()
	h := newHarness(t, &fake{profiles: forever(), current: "forever", hidden: 23}, Options{})
	h.golden("empty")
}

func TestNoProfiles(t *testing.T) {
	t.Parallel()
	h := newHarness(t, &fake{}, Options{})
	h.golden("no_profiles")
}

// A profile that can't be listed, such as one -p names that doesn't
// exist, shows the error and its hint.
func TestLoadError(t *testing.T) {
	t.Parallel()
	f := &fake{profiles: forever(), listErr: &app.UserError{
		Msg: `unknown profile "retial"`, Hint: "run `knapsack profiles list` to see your profiles",
	}}
	h := newHarness(t, f, Options{Profile: "retial"})
	h.golden("load_error")
	// c tries again, and checks once the profile loads.
	f.listErr = nil
	h.keys("c")
	if diff := cmp.Diff([]string{"list retial", "list retial", "check retial"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

func TestTooSmall(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.send(tea.WindowSizeMsg{Width: 50, Height: 10})
	h.golden("too_small")
}

// A check that stops, such as on a profile another process holds, keeps
// the rows and says why in the status line.
func TestCheckError(t *testing.T) {
	t.Parallel()
	f := sample()
	f.checkErr = &app.UserError{
		Msg:  `profile "forever" is in use by another knapsack process`,
		Hint: "wait for it to finish",
	}
	h := newHarness(t, f, Options{})
	h.golden("check_error")
}

// A profile is checked once a session: reloading it doesn't check again,
// but c does, after forgetting what was known.
func TestCheckOnce(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	h.run(h.m.enqueue(h.m.loadJob()))
	h.keys("c")
	want := []string{"list forever", "check forever", "list forever", "list forever", "check forever"}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// Jobs run one at a time: describe's Info waits for the check.
func TestJobsQueue(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.hold = true
	h.keys("c")
	if len(h.pending) != 1 {
		t.Fatalf("%d jobs started at once, want 1", len(h.pending))
	}
	h.keys("enter")
	if len(h.pending) != 1 || len(h.m.queue) != 2 {
		t.Fatalf("started %d, queued %d; want the check and info queued behind the load", len(h.pending), len(h.m.queue))
	}
	h.release()
	want := []string{"list forever", "check forever", "info forever " + baganator.String()}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// Quitting during a read quits at once; during a job that writes, it asks,
// and yes cancels the job and quits once it has stopped.
func TestQuit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.hold = true
	h.keys("c")
	h.keys("q")
	h.release()
	if !h.quit {
		t.Fatal("q during a check didn't quit")
	}

	h = newHarness(t, sample(), Options{})
	stopped := make(chan struct{})
	h.hold = true
	h.run(h.m.enqueue(job{name: "updating", writes: true, run: func(ctx context.Context) tea.Msg {
		<-ctx.Done()
		close(stopped)
		return nil
	}}))
	h.keys("q")
	if h.quit || !h.m.confirmQuit {
		t.Fatal("q during an update didn't ask first")
	}
	h.golden("confirm_quit")
	h.keys("n")
	if h.m.confirmQuit {
		t.Fatal("n didn't close the question")
	}
	h.keys("q", "y")
	if h.quit {
		t.Fatal("quit before the update stopped")
	}
	h.release()
	<-stopped
	if !h.quit {
		t.Fatal("didn't quit once the update stopped")
	}
}

// What's known about addons gone from the profile, as after an edit, is
// forgotten; what's known about the rest survives a reload.
func TestForgetGone(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.local = f.local[:2]
	h.run(h.m.enqueue(h.m.loadJob()))
	k := h.m.known["forever"]
	if len(k) != 2 || k[baganator].status != app.StatusUpdateAvailable {
		t.Errorf("known after reload = %v, want Baganator's update and Details!", k)
	}
}

func TestCutMiddle(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		path  string
		width int
		want  string
	}{
		{`/games/wow/_retail_/Interface/AddOns`, 40, `/games/wow/_retail_/Interface/AddOns`},
		{`C:\Program Files (x86)\World of Warcraft\_forever_\Interface\AddOns`, 50, `C:\Program Files (x86)…\_forever_\Interface\AddOns`},
		{`~/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft/_retail_/Interface/AddOns`, 44, `~/Games/battlenet…/_retail_/Interface/AddOns`},
		// Too narrow for the client folder: the end is kept.
		{`/games/wow/_retail_/Interface/AddOns`, 12, `…face/AddOns`},
	} {
		if got := cutMiddle(tt.path, tt.width); got != tt.want {
			t.Errorf("cutMiddle(%q, %d) = %q, want %q", tt.path, tt.width, got, tt.want)
		}
		if got := ansi.StringWidth(cutMiddle(tt.path, tt.width)); got > tt.width {
			t.Errorf("cutMiddle(%q, %d) is %d wide", tt.path, tt.width, got)
		}
	}
}
