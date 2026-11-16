package tui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
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
	// rep is the TUI's reporter, which Update reports to as the App's
	// does; failed are the addons Update fails, with why.
	rep        *reporter
	failed     map[ref.Ref]error
	updateErr  error // Update fails as a whole
	setErr     error // SetProfile fails
	auto       app.AutoProfilesResult
	sources    []app.SourceInfo
	keys       []string // what SetSourceKey was given
	envSet     bool
	keyErr     error // SetSourceKey refuses the key
	sameKey    bool  // SetSourceKey finds config already has it
	sourcesErr error
}

// Update installs each named addon at the version its check found,
// reporting each step, and changes the rows as the App's state would.
func (f *fake) Update(_ context.Context, sel app.Selector, opts app.UpdateOptions) ([]app.Result, error) {
	p := sel.Profiles[0]
	f.calls = append(f.calls, fmt.Sprintf("update %s %v prune=%v force=%v", p, sel.Addons, opts.Prune, opts.Force))
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	var out []app.Result
	for _, name := range sel.Addons {
		r, _ := ref.Parse(name)
		i := slices.IndexFunc(f.local, func(in app.Installed) bool { return in.Ref == r })
		res := app.Result{Profile: p, Name: name, Ref: r}
		f.rep.Event(app.Event{Kind: app.EventResolving, Profile: p, Ref: r})
		switch {
		case f.failed[r] != nil:
			res.Action, res.Err = app.ActionFailed, f.failed[r]
			f.rep.Event(app.Event{Kind: app.EventFailed, Profile: p, Ref: r, Err: res.Err})
		case f.local[i].Status == app.StatusNotInConfig:
			res.Action = app.ActionRemoved
			f.local = slices.Delete(f.local, i, i+1)
			f.checked = slices.DeleteFunc(f.checked, func(in app.Installed) bool { return in.Ref == r })
			f.rep.Event(app.Event{Kind: app.EventRemoved, Profile: p, Ref: r})
		default:
			version := "1.0"
			if c := slices.IndexFunc(f.checked, func(in app.Installed) bool { return in.Ref == r }); c >= 0 && f.checked[c].Available != "" {
				version = f.checked[c].Available
			}
			res.Action, res.Version = app.ActionUpgraded, version
			if f.local[i].Status == app.StatusMissing {
				res.Action = app.ActionInstalled
				f.local[i].Status = app.StatusInstalled
			}
			f.local[i].Version = version
			f.rep.Event(app.Event{Kind: app.EventDownloading, Profile: p, Ref: r})
			f.rep.Event(app.Event{Kind: app.EventProgress, Profile: p, Ref: r, Done: 1 << 20, Total: 2 << 20})
			f.rep.Event(app.Event{Kind: app.EventInstalled, Profile: p, Ref: r, Version: version})
		}
		out = append(out, res)
	}
	return out, nil
}

// Add installs each ref not already in the profile at version 1.0.
func (f *fake) Add(_ context.Context, profile string, refs []ref.Ref, force bool) ([]app.Result, error) {
	f.calls = append(f.calls, fmt.Sprintf("add %s %v force=%v", profile, refs, force))
	var out []app.Result
	for _, r := range refs {
		res := app.Result{Profile: profile, Name: r.String(), Ref: r}
		if err := f.failed[r]; err != nil {
			res.Action, res.Err = app.ActionFailed, err
		} else if slices.ContainsFunc(f.local, func(in app.Installed) bool { return in.Ref == r }) {
			res.Action = app.ActionUnchanged
		} else {
			res.Action, res.Version = app.ActionInstalled, "1.0"
			in := app.Installed{Profile: profile, Ref: r, Title: r.ShortName(), Status: app.StatusInstalled,
				Version: "1.0", InstalledAt: now, Folders: []string{r.ShortName()}}
			f.local = append(f.local, in)
			f.checked = append(f.checked, in)
			f.rep.Event(app.Event{Kind: app.EventInstalled, Profile: profile, Ref: r, Version: "1.0"})
		}
		out = append(out, res)
	}
	return out, nil
}

// UseProfile makes name current.
func (f *fake) UseProfile(_ context.Context, name string) error {
	f.calls = append(f.calls, "use "+name)
	for i := range f.profiles {
		f.profiles[i].Current = f.profiles[i].Name == name
	}
	f.current = name
	return nil
}

// SetProfile makes a profile, current if it's the first, or changes one;
// setErr fails it.
func (f *fake) SetProfile(_ context.Context, req app.SetProfileRequest) (app.SetProfileResult, error) {
	f.calls = append(f.calls, fmt.Sprintf("set name=%q path=%q flavor=%q must=%d", req.Name, req.Path, req.Flavor, req.Must))
	if f.setErr != nil {
		return app.SetProfileResult{}, f.setErr
	}
	name := req.Name
	if name == "" {
		name = "retail"
	}
	for i, p := range f.profiles {
		if p.Name == name {
			if req.Path != "" {
				f.profiles[i].Path = req.Path
			}
			if req.Flavor != "" {
				f.profiles[i].Flavor = flavor.Flavor(req.Flavor)
			}
			return app.SetProfileResult{Profile: f.profiles[i], Changed: true}, nil
		}
	}
	p := app.ProfileInfo{Name: name, Path: req.Path, Flavor: "retail", Current: len(f.profiles) == 0}
	f.profiles = append(f.profiles, p)
	if p.Current {
		f.current = name
	}
	return app.SetProfileResult{Profile: p, Created: true, Changed: true, MadeCurrent: p.Current}, nil
}

// AutoProfiles reports auto and makes the profiles it says it created.
func (f *fake) AutoProfiles(context.Context, string) (app.AutoProfilesResult, error) {
	f.calls = append(f.calls, "auto")
	for _, p := range f.auto.Profiles {
		if p.Result.Created {
			f.profiles = append(f.profiles, p.Result.Profile)
			if p.Result.MadeCurrent {
				f.current = p.Result.Profile.Name
			}
		}
	}
	return f.auto, nil
}

// DeleteProfile deletes name; none is current after deleting the current
// one.
func (f *fake) DeleteProfile(_ context.Context, name string) (app.DeleteProfileResult, error) {
	f.calls = append(f.calls, "delete "+name)
	f.profiles = slices.DeleteFunc(f.profiles, func(p app.ProfileInfo) bool { return p.Name == name })
	res := app.DeleteProfileResult{WasCurrent: f.current == name}
	if res.WasCurrent {
		f.current = ""
	}
	return res, nil
}

// Sources lists GitHub, CurseForge and Wago as sources says.
func (f *fake) Sources(context.Context) ([]app.SourceInfo, error) {
	if f.sourcesErr != nil {
		return nil, f.sourcesErr
	}
	if f.sources == nil {
		f.sources = []app.SourceInfo{
			{Name: ref.GitHub, API: config.APIOfficial},
			{Name: ref.CurseForge, NeedsKey: true, Key: app.KeyNone, Env: "KNAPSACK_CURSEFORGE_API_KEY", API: config.APIOfficial},
			{Name: ref.Wago, NeedsKey: true, Key: app.KeyNone, Env: "KNAPSACK_WAGO_API_KEY", API: config.APIWebsite},
		}
	}
	return f.sources, nil
}

// SetSourceKey stores or removes a key; keys records each, for tests to
// check, and envSet says its variable overrides config.
func (f *fake) SetSourceKey(_ context.Context, name, key string) (app.SetSourceKeyResult, error) {
	f.calls = append(f.calls, fmt.Sprintf("set-key %s (%d chars)", name, len(key)))
	f.keys = append(f.keys, key)
	if f.keyErr != nil {
		return app.SetSourceKeyResult{}, f.keyErr
	}
	res := app.SetSourceKeyResult{Changed: !f.sameKey, Overridden: f.envSet, Env: "KNAPSACK_" + strings.ToUpper(name) + "_API_KEY"}
	for i, s := range f.sources {
		if s.Name == name {
			f.sources[i].Key = app.KeyConfig
			if key == "" {
				f.sources[i].Key = app.KeyNone
			}
			res.Website = s.API == config.APIWebsite
		}
	}
	return res, nil
}

// SetSourceAPI changes how a source is read.
func (f *fake) SetSourceAPI(_ context.Context, name, api string) (app.SetSourceAPIResult, error) {
	f.calls = append(f.calls, "set-api "+name+" "+api)
	res := app.SetSourceAPIResult{Changed: true, API: config.API(api)}
	for i, s := range f.sources {
		if s.Name == name {
			f.sources[i].API = config.API(api)
			res.NeedsKey = api == string(config.APIOfficial) && s.Key == app.KeyNone
		}
	}
	return res, nil
}

// Remove uninstalls each named addon.
func (f *fake) Remove(_ context.Context, profile string, names []string) ([]app.Result, error) {
	f.calls = append(f.calls, fmt.Sprintf("remove %s %v", profile, names))
	var out []app.Result
	for _, name := range names {
		r, _ := ref.Parse(name)
		f.local = slices.DeleteFunc(f.local, func(in app.Installed) bool { return in.Ref == r })
		f.checked = slices.DeleteFunc(f.checked, func(in app.Installed) bool { return in.Ref == r })
		f.rep.Event(app.Event{Kind: app.EventRemoved, Profile: profile, Ref: r})
		out = append(out, app.Result{Profile: profile, Name: name, Ref: r, Action: app.ActionRemoved})
	}
	return out, nil
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
	// A check reports each addon's steps, as the App's does.
	for _, r := range pending {
		if r.Status == app.StatusChecking {
			f.rep.Event(app.Event{Kind: app.EventResolving, Profile: p, Ref: r.Ref})
		}
	}
	if f.checkErr != nil {
		return app.Listing{}, f.checkErr
	}
	for i, r := range f.checked {
		f.rep.Event(app.Event{Kind: app.EventChecked, Profile: p, Ref: r.Ref})
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
	// opened are the URLs the TUI opened in the browser, or tried to,
	// failing with openErr.
	opened  []string
	openErr error
}

func newHarness(t *testing.T, f *fake, opts Options) *harness {
	t.Helper()
	h := &harness{t: t, f: f}
	b := &bus{}
	b.set(func(msg tea.Msg) { h.sent = append(h.sent, msg) })
	if opts.Running == "" {
		opts.Running = "v0.0.0-test on testos/testarch"
	}
	f.rep = newReporter(b)
	h.m = newModel(context.Background(), f, opts, b, f.rep)
	h.m.tickEvery = 0
	h.m.spin = spinnerDots // the same frames on every OS
	h.m.now = func() time.Time { return now }
	h.m.blink = false
	h.m.exec = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
		return func() tea.Msg { return fn(c.Run()) }
	}
	h.m.openURL = func(_ context.Context, url string) error {
		if h.openErr != nil {
			return h.openErr
		}
		h.opened = append(h.opened, url)
		return nil
	}
	// The filter's cursor blink sleeps; the harness runs commands at once.
	st := h.m.filter.Styles()
	st.Cursor.Blink = false
	h.m.filter.SetStyles(st)
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

// step runs the first held command and delivers its result, holding any
// commands that follows from it: the next job, say.
func (h *harness) step() {
	h.t.Helper()
	if len(h.pending) == 0 {
		h.t.Fatal("no command to step")
	}
	c := h.pending[0]
	h.pending = h.pending[1:]
	msg := c()
	if batch, ok := msg.(tea.BatchMsg); ok {
		h.pending = append(append([]tea.Cmd(nil), batch...), h.pending...)
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
		case "ctrl+d":
			msg = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		case `ctrl+\`:
			msg = tea.KeyPressMsg{Code: '\\', Mod: tea.ModCtrl}
		case "space":
			msg = tea.KeyPressMsg{Code: ' ', Text: " "}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		case "shift+tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
		case "ctrl+s":
			msg = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
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
	if len(lines) > h.m.height {
		h.t.Errorf("screen %s is %d lines, taller than the window's %d", name, len(lines), h.m.height)
	}
	for i, l := range lines {
		// A line wider than the window wraps and shifts everything below.
		if w := ansi.StringWidth(l); w > h.m.width {
			h.t.Errorf("screen %s line %d is %d wide, wider than the window's %d: %q", name, i+1, w, h.m.width, l)
		}
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

// writeJob is a job that changes something and runs until cancelled,
// closing stopped when it does.
func writeJob(stopped chan struct{}) job {
	return job{name: "updating", writes: true, run: func(ctx context.Context) tea.Msg {
		<-ctx.Done()
		close(stopped)
		return nil
	}}
}

// The job the quit question is about can finish before it's answered:
// the question goes, rather than drawing a job that's gone.
func TestQuitQuestionOutlivesJob(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.hold = true
	h.run(h.m.enqueue(job{name: "updating", writes: true, run: func(context.Context) tea.Msg { return nil }}))
	h.keys("q")
	if !h.m.confirmQuit {
		t.Fatal("q during an update didn't ask")
	}
	h.release()
	if h.m.confirmQuit || h.quit {
		t.Fatalf("after the job finished: confirmQuit %v, quit %v; want neither", h.m.confirmQuit, h.quit)
	}
	h.golden("checked")
}

// Ctrl-C quits from the filter too, which takes every other key.
func TestCtrlCInFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("/", "w", "ctrl+c")
	if !h.quit {
		t.Fatal("ctrl+c in the filter didn't quit")
	}
}

// Ctrl-C at the quit question answers yes: the job is cancelled, to roll
// back, and the TUI waits for it. Another Ctrl-C while it waits quits at
// once.
func TestCtrlCQuitting(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	stopped := make(chan struct{})
	h.hold = true
	h.run(h.m.enqueue(writeJob(stopped)))
	h.keys("ctrl+c", "ctrl+c")
	if h.quit || !h.m.quitting {
		t.Fatalf("quit %v, quitting %v; want it waiting for the update", h.quit, h.m.quitting)
	}
	select {
	case <-h.m.ctx.Done():
	default:
		t.Fatal("ctrl+c at the question didn't cancel the update")
	}
	h.keys("q")
	if len(h.pending) != 1 {
		t.Fatalf("q while waiting did something: %d commands", len(h.pending))
	}
	// Commands are held, so look at what the third one returns.
	_, cmd := h.m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("a third ctrl+c did nothing")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("a third ctrl+c didn't quit at once")
	}
	h.release()
	<-stopped
}

// Describe scrolls when it doesn't fit, and the reason a check failed is
// near the top, above a long list of folders.
func TestDescribeScrolls(t *testing.T) {
	t.Parallel()
	f := sample()
	folders := []string{"DBM-Core"}
	for _, raid := range []string{"Aberrus", "Amirdrassil", "Nerubar", "Undermine", "Manaforge", "Vault", "Party", "Delves", "Dungeons", "Challenges"} {
		folders = append(folders, "DBM-"+raid)
	}
	f.checked[2].Folders = folders
	h := newHarness(t, f, Options{})
	h.keys("j", "j", "enter")
	h.golden("describe_long")
	h.keys("G")
	h.golden("describe_long_end")
	h.keys("g")
	h.golden("describe_long")
}

// Help fits the smallest window by scrolling.
func TestHelpSmallWindow(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.send(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	h.keys("?")
	h.golden("help_small")
	h.keys("G")
	h.golden("help_small_end")
}

// A title too long for the window is cut, not wrapped.
func TestLongTitle(t *testing.T) {
	t.Parallel()
	f := sample()
	long := "classic-era-hardcore-test-copy"
	f.profiles = []app.ProfileInfo{{Name: long, Path: "/games/wow/_classic_era_/Interface/AddOns"}}
	f.local[0].Title = "Baganator, the bag and bank replacement for every client"
	f.checked[0].Title = f.local[0].Title
	h := newHarness(t, f, Options{Profile: long})
	h.send(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	h.golden("long_title")
	h.keys("enter")
	h.golden("long_title_describe")
}

// Pressing c while a check is running or queued doesn't queue another.
func TestCheckNotTwice(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.hold = true
	h.keys("c", "c", "c")
	h.release()
	if diff := cmp.Diff([]string{"list forever", "check forever"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// Only the addon being described is read: opening another, or closing
// describe, drops a queued Info.
func TestInfoNotQueuedTwice(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.hold = true
	h.keys("c")            // a check, for the Infos to wait behind
	h.keys("enter", "esc") // Baganator, closed
	h.keys("j", "enter", "esc", "j", "enter")
	h.release()
	want := []string{"list forever", "check forever", "info forever " + plater.String()}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// u on an addon with an update installs it at once, with no plan; then
// what's known says it's up to date, and the profile is read again.
func TestUpdateOne(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("u")
	want := []string{
		"update forever [" + baganator.String() + "] prune=false force=false",
		"list forever",
	}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	h.golden("updated_one")
}

// An addon known to be up to date isn't updated: u says so, and c checks
// again.
func TestUpdateUpToDate(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("j", "u")
	if len(f.calls) != 0 {
		t.Errorf("calls = %v, want none", f.calls)
	}
	if want := "Details! is up to date; c checks again."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
}

// U shows the plan, from what's known; a prune box and Force can be
// checked, and enter updates exactly what the plan lists.
func TestUpdateAll(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	// U plans for every addon, not only those the filter shows.
	h.keys("/", "b", "a", "g", "enter")
	f.calls = nil
	h.keys("U")
	h.golden("plan")
	h.keys("space", "j", "space")
	h.golden("plan_checked")
	h.keys("enter")
	want := []string{
		fmt.Sprintf("update forever [%s %s %s %s] prune=true force=true", baganator, plater, leatrix, weak),
		"list forever",
	}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	h.keys("esc") // the filter
	h.golden("updated_all")
}

// Unchecked, a prune candidate isn't named, so it stays installed, as
// update without --prune leaves it; esc runs nothing.
func TestUpdatePlanNoPrune(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("U", "esc")
	if len(f.calls) != 0 || h.m.plan != nil {
		t.Fatalf("esc: calls %v, plan open %v; want neither", f.calls, h.m.plan != nil)
	}
	h.keys("U", "enter")
	want := fmt.Sprintf("update forever [%s %s %s] prune=false force=false", baganator, plater, leatrix)
	if len(f.calls) == 0 || f.calls[0] != want {
		t.Errorf("calls = %v, want %q first", f.calls, want)
	}
}

// Marked rows that aren't all simply outdated go through the plan, which
// lists only them. Marks are cleared once the update starts.
func TestUpdateMarked(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("space", "j", "j", "space")
	h.golden("marked")
	h.keys("u")
	if h.m.plan == nil || len(h.m.plan.items) != 2 {
		t.Fatalf("plan = %+v, want Baganator's upgrade and Leatrix's install", h.m.plan)
	}
	h.keys("enter")
	if want := fmt.Sprintf("update forever [%s %s] prune=false force=false", baganator, leatrix); f.calls[0] != want {
		t.Errorf("calls[0] = %q, want %q", f.calls[0], want)
	}
	if len(h.m.marks) != 0 {
		t.Errorf("marks after updating = %v, want none", h.m.marks)
	}
	h.keys("space", "space", `ctrl+\`)
	if len(h.m.marks) != 0 {
		t.Errorf("marks after ctrl+\\ = %v, want none", h.m.marks)
	}
}

// An addon that fails to update shows why in describe.
func TestUpdateFailed(t *testing.T) {
	t.Parallel()
	f := sample()
	f.failed = map[ref.Ref]error{baganator: &app.UserError{
		Msg:  "github austinrr/baganator-mirror: rate limited",
		Hint: "set GITHUB_TOKEN, or log in with `gh auth login`",
	}}
	h := newHarness(t, f, Options{})
	h.keys("u")
	if want := "forever: 1 failed"; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	h.keys("enter")
	h.golden("update_failed_describe")
}

// ctrl+d asks first, naming the folders it deletes; n cancels, y removes.
// From describe, removing goes back to the table.
func TestRemove(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("ctrl+d")
	h.golden("confirm_remove")
	h.keys("n")
	if len(f.calls) != 0 || h.m.confirm != nil {
		t.Fatalf("n: calls %v, question open %v; want neither", f.calls, h.m.confirm != nil)
	}
	h.keys("enter", "ctrl+d", "y")
	want := []string{"info forever " + baganator.String(), "remove forever [" + baganator.String() + "]", "list forever"}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if h.m.describing {
		t.Error("still describing a removed addon")
	}
	if _, ok := h.m.known["forever"][baganator]; ok {
		t.Error("what was known about the removed addon wasn't forgotten")
	}
	h.golden("removed")
}

// A row in a job shows its stage, with download progress taken from the
// reporter on the spinner's tick, the latest only.
func TestProgress(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	h.m.running = &job{kind: jobUpdate, name: "updating", writes: true}
	h.m.stages[leatrix] = stage{what: "queued"}
	h.send(eventMsg{app.Event{Kind: app.EventResolving, Profile: "forever", Ref: plater}})
	h.send(eventMsg{app.Event{Kind: app.EventDownloading, Profile: "forever", Ref: baganator}})
	for _, done := range []int64{100 << 10, 900 << 10, 1258291} {
		f.rep.Event(app.Event{Kind: app.EventProgress, Profile: "forever", Ref: baganator, Done: done, Total: 4718592})
	}
	h.send(tickMsg{})
	h.golden("progress")
	f.rep.Event(app.Event{Kind: app.EventProgress, Profile: "forever", Ref: baganator, Done: 4718592, Total: 4718592})
	h.send(tickMsg{})
	if got := h.m.stages[baganator].String(); got != "installing" {
		t.Errorf("stage once downloaded = %q, want installing", got)
	}
}

// The reporter is used from many goroutines at once: progress is
// coalesced to the latest for each addon, and any other event for an
// addon drops its pending progress and is sent at once.
func TestReporterCoalesces(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var sent []tea.Msg
	b := &bus{}
	b.set(func(msg tea.Msg) { mu.Lock(); sent = append(sent, msg); mu.Unlock() })
	r := newReporter(b)
	refs := []ref.Ref{baganator, details, plater}
	var wg sync.WaitGroup
	for _, x := range refs {
		for n := range 50 {
			wg.Go(func() {
				r.Event(app.Event{Kind: app.EventProgress, Profile: "forever", Ref: x, Done: int64(n), Total: 100})
			})
		}
	}
	wg.Wait()
	r.Event(app.Event{Kind: app.EventInstalled, Profile: "forever", Ref: plater})
	got := r.drain()
	if len(got) != 2 {
		t.Fatalf("drained %d events, want the latest for Baganator and Details!", len(got))
	}
	for _, e := range got {
		if e.Kind != app.EventProgress || e.Ref == plater {
			t.Errorf("drained %+v, want only progress, none for Plater", e)
		}
	}
	if len(r.drain()) != 0 {
		t.Error("a second drain returned progress again")
	}
	if len(sent) != 1 || sent[0].(eventMsg).Kind != app.EventInstalled {
		t.Errorf("sent %v, want only Plater's Installed", sent)
	}
}

func TestStageString(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		s    stage
		want string
	}{
		{stage{what: "resolving"}, "resolving"},
		{stage{what: "downloading"}, "downloading"},
		{stage{what: "downloading", done: 1258291, total: 4718592}, "downloading 1.2 of 4.5 MiB"},
		{stage{what: "downloading", done: 900 << 10, total: 4718592}, "downloading 900.0 KiB of 4.5 MiB"},
		{stage{what: "downloading", done: 512, total: -1}, "downloading 512 B"},
		{stage{what: "downloading", done: 4718592, total: 4718592}, "installing"},
	} {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("%+v = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// An update that fails as a whole keeps its error in the status line
// after the reload, and doesn't claim there was nothing to update.
func TestUpdateErrorKept(t *testing.T) {
	t.Parallel()
	f := sample()
	f.updateErr = &app.UserError{
		Msg:  `profile "forever" is in use by another knapsack process`,
		Hint: "wait for it to finish",
	}
	h := newHarness(t, f, Options{})
	h.keys("u")
	if h.m.err == nil || h.m.flash != "" {
		t.Fatalf("after the reload: err %v, flash %q; want the error and no flash", h.m.err, h.m.flash)
	}
	if got := ansi.Strip(h.m.status()); !strings.Contains(got, "in use by another knapsack process") {
		t.Errorf("status = %q, want the error", got)
	}
	// The next action clears it.
	f.updateErr = nil
	h.keys("u")
	if h.m.err != nil {
		t.Errorf("err after updating again = %v, want none", h.m.err)
	}
}

// One job ending leaves the stages of the next, still queued.
func TestQueuedStageKept(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.hold = true
	h.keys("u")                // Baganator's update, running
	h.keys("j", "ctrl+d", "y") // Details!'s remove, queued behind it
	if got := h.m.stages[details].what; got != "removing" {
		t.Fatalf("Details! stage = %q, want removing", got)
	}
	h.step() // the update finishes
	if _, ok := h.m.stages[baganator]; ok {
		t.Error("Baganator still shows a stage after its update")
	}
	if got := h.m.stages[details].what; got != "removing" {
		t.Errorf("Details! stage after the update = %q, want removing still", got)
	}
}

// A row already in a job isn't queued again.
func TestNotQueuedTwice(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.hold = true
	h.keys("u", "u", "ctrl+d")
	if h.m.confirm != nil {
		t.Error("ctrl+d asked to remove an addon that's updating")
	}
	if want := "Already in progress."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	h.release()
	if diff := cmp.Diff([]string{"update forever [" + baganator.String() + "] prune=false force=false", "list forever"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// Drawing the remove confirm in a narrow window doesn't shorten its list
// of folders for a wider one.
func TestConfirmKeepsLines(t *testing.T) {
	t.Parallel()
	f := sample()
	f.local[0].Folders = []string{"Baganator", "Baganator_Categories", "Baganator_Sorting", "Baganator_Themes"}
	f.checked[0].Folders = f.local[0].Folders
	h := newHarness(t, f, Options{})
	h.keys("ctrl+d")
	before := slices.Clone(h.m.confirm.lines)
	h.send(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	h.m.render()
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	if diff := cmp.Diff(before, h.m.confirm.lines); diff != "" {
		t.Errorf("confirm lines changed by drawing (-before +after):\n%s", diff)
	}
	h.golden("confirm_remove_folders")
}

// Marks on rows the filter hides still count: ctrl+d asks about the
// marked addon, not the row under the cursor.
func TestMarksHiddenByFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("space", "/", "p", "l", "a", "t", "enter", "ctrl+d")
	if h.m.confirm == nil || !strings.Contains(h.m.confirm.question, "Baganator") {
		t.Fatalf("confirm = %+v, want it to ask about the marked Baganator", h.m.confirm)
	}
}

// Updating the addon being described reads its details again.
func TestDescribeRefreshedAfterUpdate(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	h.keys("enter")
	f.calls = nil
	h.keys("u")
	want := []string{
		"update forever [" + baganator.String() + "] prune=false force=false",
		"list forever",
		"info forever " + baganator.String(),
	}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// An addon's warnings are its last install's: a new update starts afresh.
func TestWarningsReset(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.m.running = &job{kind: jobUpdate, writes: true}
	warn := func(msg string) {
		h.send(eventMsg{app.Event{Kind: app.EventWarned, Profile: "forever", Ref: baganator, Message: msg}})
	}
	warn("built for an older Interface")
	h.send(eventMsg{app.Event{Kind: app.EventResolving, Profile: "forever", Ref: baganator}})
	warn("missing dependency Syndicator")
	if diff := cmp.Diff([]string{"missing dependency Syndicator"}, h.m.warnings[baganator]); diff != "" {
		t.Errorf("warnings (-want +got):\n%s", diff)
	}
}

// What an action did is said until the next key; then the counts are
// back.
func TestFlashUntilNextKey(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("u")
	if h.m.flash == "" {
		t.Fatal("no flash after updating")
	}
	h.keys("j")
	if h.m.flash != "" {
		t.Errorf("flash after j = %q, want none", h.m.flash)
	}
}

// a opens the add dialog; a pasted list with a line that isn't a ref
// stays open, says which line and why, and adds nothing.
func TestAddProblems(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("a")
	h.golden("add_empty")
	h.send(tea.PasteMsg{Content: "https://github.com/kemayo/silverdragon\nbaganator\n"})
	h.keys("ctrl+s")
	if h.m.add == nil || len(f.calls) != 0 {
		t.Fatalf("dialog open %v, calls %v; want it open and nothing added", h.m.add != nil, f.calls)
	}
	h.golden("add_problems")
	// An empty dialog says what to type.
	h.m.add.text.SetValue("  \n")
	h.keys("ctrl+s")
	if want := []string{"type a ref or link, one per line"}; !slices.Equal(h.m.add.problems, want) {
		t.Errorf("problems = %v, want %v", h.m.add.problems, want)
	}
	h.keys("esc")
	if h.m.add != nil || len(f.calls) != 0 {
		t.Errorf("esc: dialog open %v, calls %v; want neither", h.m.add != nil, f.calls)
	}
}

// ctrl+s adds every ref, with Force when its box is checked; q and
// digits are text in the dialog, not keys. Afterwards the new addon is
// a row, known to be up to date.
func TestAdd(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("a", "q", "1")
	if h.quit || h.m.add == nil {
		t.Fatal("q in the add dialog closed it or quit")
	}
	h.m.add.text.SetValue("https://github.com/Kemayo/SilverDragon/releases\ngithub:austinrr/baganator-mirror")
	h.keys("tab", "space", "ctrl+s")
	silver := ref.Ref{Source: ref.GitHub, ID: "kemayo/silverdragon"}
	want := []string{
		fmt.Sprintf("add forever [%s %s] force=true", silver, baganator),
		"list forever",
	}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if want := "forever: 1 installed, 1 unchanged"; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	if k := h.m.known["forever"][silver]; k.status != app.StatusUpToDate {
		t.Errorf("silverdragon known as %q, want up to date", k.status)
	}
	h.golden("added")
}

// A paste goes into the filter while it's open.
func TestPasteFilter(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("/")
	h.send(tea.PasteMsg{Content: "wago"})
	if h.m.query != "wago" || len(h.m.visible()) != 2 {
		t.Errorf("query %q, %d rows; want wago and 2", h.m.query, len(h.m.visible()))
	}
}

// o opens the addon's page, from the table or describe; r opens the
// report link of a website that answered unexpectedly.
func TestOpen(t *testing.T) {
	t.Parallel()
	f := sample()
	f.checked[2].Err = fmt.Errorf("wago plater: %w: no data-page", source.ErrUnexpectedWebsite)
	h := newHarness(t, f, Options{})
	h.keys("o")
	h.keys("j", "j", "enter", "o", "r")
	if len(h.opened) != 3 {
		t.Fatalf("opened %v, want Baganator's page, Plater's and a report", h.opened)
	}
	if h.opened[0] != baganator.URL() || h.opened[1] != plater.URL() || !strings.HasPrefix(h.opened[2], app.IssuesURL+"/new?") {
		t.Errorf("opened %v", h.opened)
	}
	if want := "Opened the report in the browser."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	// An error with no report link says so.
	h.keys("esc", "k", "k", "enter", "r")
	if len(h.opened) != 3 || !strings.Contains(h.m.flash, "no report link") {
		t.Errorf("r with no link: opened %v, flash %q", h.opened, h.m.flash)
	}
	// A browser that won't start is an error in the status line.
	h.openErr = errors.New(`exec: "xdg-open": executable file not found in $PATH`)
	h.keys("o")
	if h.m.err == nil || !strings.Contains(h.m.err.Error(), "couldn't open") {
		t.Errorf("err = %v, want couldn't open", h.m.err)
	}
}

// An addon Add couldn't add opens the dialog again with only the failed
// refs, each with why and its hint, and Force as it was.
func TestAddFailedReopens(t *testing.T) {
	t.Parallel()
	f := sample()
	silver := ref.Ref{Source: ref.GitHub, ID: "kemayo/silverdragon"}
	f.failed = map[ref.Ref]error{silver: &app.UserError{
		Msg:  "github kemayo/silverdragon: folder SilverDragon in AddOns belongs to no addon",
		Hint: "check Force to replace it with the release",
	}}
	h := newHarness(t, f, Options{})
	h.keys("a")
	h.m.add.text.SetValue("github:kemayo/silverdragon\ngithub:leatrix/leatrix-maps")
	h.keys("ctrl+s")
	if h.m.add == nil {
		t.Fatal("the dialog didn't open again on the failed addon")
	}
	if got := h.m.add.text.Value(); got != silver.String() {
		t.Errorf("dialog text = %q, want only the failed ref", got)
	}
	if h.m.add.force {
		t.Error("Force was checked, though it wasn't before")
	}
	h.golden("add_failed")
}

// Adopting records an installed addon in config at the version on disk,
// unchecked: it isn't claimed to be up to date.
func TestAdoptedUnchecked(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.send(jobDoneMsg{doneMsg{kind: jobAdd, profile: "forever", refs: []ref.Ref{weak},
		results: []app.Result{{Profile: "forever", Ref: weak, Action: app.ActionAdopted, Version: "5.17.1"}}}})
	if k, ok := h.m.known["forever"][weak]; ok {
		t.Errorf("adopted WeakAuras known as %+v, want unchecked", k)
	}
}

// Many problems are cut to what fits, so the Force box stays in view.
func TestAddManyProblems(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("a")
	var bad []string
	for i := range 20 {
		bad = append(bad, fmt.Sprintf("nope%d", i))
	}
	h.m.add.text.SetValue(strings.Join(bad, "\n"))
	h.keys("ctrl+s")
	h.golden("add_many_problems")
}

// The dialog is drawn while it's open, even over a reload that failed,
// since it's what takes the keys.
func TestAddOverLoadError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("a")
	h.m.loadErr = errors.New("config.toml: line 3: invalid flavor")
	if title, _ := h.m.content(76, 19); !strings.Contains(ansi.Strip(title), "add(") {
		t.Errorf("box title = %q, want the add dialog's", ansi.Strip(title))
	}
	if got := ansi.Strip(h.m.footer()); !strings.Contains(got, "ctrl+s add") {
		t.Errorf("footer = %q, want the dialog's keys", got)
	}
}

// A page opening replaces an earlier error with saying so.
func TestOpenClearsError(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.m.err = errors.New("an old check's error")
	h.keys("o")
	if h.m.err != nil || !strings.HasPrefix(h.m.flash, "Opened") {
		t.Errorf("err %v, flash %q; want no error and Opened", h.m.err, h.m.flash)
	}
}

// A paste while the quit question is asked goes nowhere; one while the
// Force box has the focus moves it back to the text.
func TestPasteRouting(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("a", "tab")
	h.send(tea.PasteMsg{Content: "github:a/b"})
	if h.m.add.onForce || h.m.add.text.Value() != "github:a/b" {
		t.Errorf("onForce %v, text %q; want the text focused, with the paste", h.m.add.onForce, h.m.add.text.Value())
	}
	h.hold = true
	h.run(h.m.enqueue(writeJob(make(chan struct{}))))
	h.keys("ctrl+c")
	if !h.m.confirmQuit {
		t.Fatal("ctrl+c during a write didn't ask")
	}
	h.send(tea.PasteMsg{Content: "more"})
	if got := h.m.add.text.Value(); got != "github:a/b" {
		t.Errorf("text after a paste at the quit question = %q, want it unchanged", got)
	}
}

// twoProfiles is sample with a second profile, retail, besides the
// current one.
func twoProfiles() *fake {
	f := sample()
	f.profiles = append(f.profiles, app.ProfileInfo{
		Name: "retail", Flavor: "retail", Addons: []ref.Ref{baganator},
		Path: "/home/me/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft/_retail_/Interface/AddOns",
	})
	f.profiles[0].Flavor = "forever"
	f.profiles[0].Addons = []ref.Ref{baganator, details, plater, leatrix}
	return f
}

// tab, 2 and 1 switch views; the profiles view lists each profile, the
// current one marked. No Home: ~ isn't used on Windows (app.ShortenHome),
// so the screen would differ by OS.
func TestProfilesView(t *testing.T) {
	t.Parallel()
	h := newHarness(t, twoProfiles(), Options{})
	h.keys("tab")
	h.golden("profiles")
	h.keys("1")
	if h.m.view != viewAddons {
		t.Errorf("view after 1 = %d, want addons", h.m.view)
	}
	h.keys("2", "?")
	h.golden("profiles_help")
	h.keys("esc", "shift+tab")
	if h.m.view != viewAddons {
		t.Errorf("view after shift+tab = %d, want addons", h.m.view)
	}
}

// enter uses the profile: it's made current, and its addons are shown,
// read and checked afresh.
func TestUseProfile(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("space", "2", "j", "enter")
	if diff := cmp.Diff([]string{"use retail", "list retail", "check retail"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if h.m.view != viewAddons || h.m.profile != "retail" || len(h.m.marks) != 0 {
		t.Errorf("view %d, profile %q, marks %v; want retail's addons, unmarked", h.m.view, h.m.profile, h.m.marks)
	}
}

// n makes a profile by path; an error stays in the form, with its hint,
// to fix and save again.
func TestNewProfile(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	f.setErr = &app.UserError{
		Msg:  `/games/wow is the game folder, with _retail_, _classic_ in it`,
		Hint: `pass one client folder: --path "/games/wow/_retail_"`,
	}
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "n")
	h.golden("profile_new")
	h.keys("enter")
	if h.m.form == nil || h.m.form.err == nil || len(f.calls) != 0 {
		t.Fatalf("an empty path was sent: calls %v", f.calls)
	}
	h.send(tea.PasteMsg{Content: "/games/wow"})
	h.keys("tab", "c", "l", "a", "s", "s", "i", "c", "enter")
	h.golden("profile_error")
	f.setErr = nil
	h.keys("enter")
	want := []string{
		`set name="classic" path="/games/wow" flavor="" must=1`,
		`set name="classic" path="/games/wow" flavor="" must=1`,
	}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if h.m.form != nil || h.m.flash != "Created profile classic." {
		t.Errorf("form open %v, flash %q", h.m.form != nil, h.m.flash)
	}
}

// s changes a profile, sending only the fields that changed; unchanged,
// it sends nothing.
func TestSetProfile(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "s", "enter")
	if len(f.calls) != 0 || h.m.flash != "Profile forever unchanged." {
		t.Errorf("unchanged: calls %v, flash %q", f.calls, h.m.flash)
	}
	h.keys("s", "tab")
	h.m.form.fields[1].SetValue("classic")
	h.keys("enter")
	if diff := cmp.Diff([]string{`set name="forever" path="" flavor="classic" must=2`, "list forever"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// With no profiles, the TUI opens on the profiles view, where A finds the
// game; the profile made current is then shown.
func TestNoProfilesAuto(t *testing.T) {
	t.Parallel()
	retail := app.ProfileInfo{Name: "retail", Flavor: "retail", Path: "/Applications/World of Warcraft/_retail_/Interface/AddOns", Current: true}
	f := &fake{auto: app.AutoProfilesResult{Profiles: []app.AutoProfile{
		{Game: "/Applications/World of Warcraft", Client: "_retail_", Result: app.SetProfileResult{Profile: retail, Created: true, Changed: true, MadeCurrent: true}},
		{Game: "/Applications/World of Warcraft", Client: "_ptr_", Skip: app.SkipNoAddOns},
	}}}
	h := newHarness(t, f, Options{})
	if h.m.view != viewProfiles {
		t.Fatalf("view = %d, want profiles", h.m.view)
	}
	h.golden("no_profiles")
	h.keys("A")
	h.golden("auto")
	h.keys("enter")
	if h.m.profile != "retail" || !h.m.loaded {
		t.Errorf("profile %q, loaded %v; want retail's addons read", h.m.profile, h.m.loaded)
	}
}

// ctrl+d deletes a profile after a confirm saying its addon files stay;
// deleting the current one leaves none current.
func TestDeleteProfile(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "ctrl+d")
	h.golden("profile_delete")
	h.keys("y")
	if len(f.calls) == 0 || f.calls[0] != "delete forever" {
		t.Fatalf("calls = %v, want delete forever first", f.calls)
	}
	if h.m.profile != "" || len(h.m.profiles) != 1 {
		t.Errorf("profile %q, %d profiles; want none shown and retail left", h.m.profile, len(h.m.profiles))
	}
	if _, ok := h.m.known["forever"]; ok {
		t.Error("what was known about the deleted profile was kept")
	}
}

// e hands the terminal to the editor, from either view; afterwards the
// profile is read again, and the status line says what to run for the
// addons the edit added.
func TestEditProfile(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	var edited []string
	opts := Options{Edit: func(_ context.Context, name string) (bool, app.ProfileEditResult, error) {
		edited = append(edited, name)
		return true, app.ProfileEditResult{Changed: true, Added: []ref.Ref{weak}}, nil
	}}
	h := newHarness(t, f, opts)
	f.calls = nil
	h.keys("e")
	if diff := cmp.Diff([]string{"forever"}, edited); diff != "" {
		t.Errorf("edited (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"list forever"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if want := "Saved profile forever. 1 addon added: U installs them."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	h.keys("2", "j", "e")
	if len(edited) != 2 || edited[1] != "retail" {
		t.Errorf("edited = %v, want retail second", edited)
	}
}

// e waits for no job: one running or queued could hold the lock its
// save needs, so it says to wait instead.
func TestEditWaitsForJobs(t *testing.T) {
	t.Parallel()
	called := false
	h := newHarness(t, twoProfiles(), Options{Edit: func(context.Context, string) (bool, app.ProfileEditResult, error) {
		called = true
		return false, app.ProfileEditResult{}, nil
	}})
	h.hold = true
	h.keys("u", "e")
	if called || !strings.HasPrefix(h.m.flash, "Wait for updating") {
		t.Errorf("editor ran %v, flash %q; want it not run, and a wait", called, h.m.flash)
	}
}

// A SIGINT or SIGTERM from outside is caught: Bubble Tea's quit on it
// becomes a signalMsg, which waits for a write to roll back, then quits.
// The TUI's own quit passes, and Run then reports the signal.
func TestSignalQuit(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	if _, ok := quitFilter(h.m, tea.InterruptMsg{}).(signalMsg); !ok {
		t.Fatal("SIGINT's InterruptMsg wasn't caught")
	}
	if _, ok := quitFilter(h.m, tea.QuitMsg{}).(signalMsg); !ok {
		t.Fatal("SIGTERM's QuitMsg wasn't caught")
	}
	stopped := make(chan struct{})
	h.hold = true
	h.run(h.m.enqueue(writeJob(stopped)))
	h.send(signalMsg{})
	if h.quit || !h.m.quitting || !h.m.signaled {
		t.Fatalf("quit %v, quitting %v, signaled %v; want it waiting for the write", h.quit, h.m.quitting, h.m.signaled)
	}
	h.release()
	<-stopped
	if !h.quit {
		t.Fatal("didn't quit once the write stopped")
	}
	if _, ok := quitFilter(h.m, tea.QuitMsg{}).(tea.QuitMsg); !ok {
		t.Error("the TUI's own quit was caught")
	}
}

// Switching from a scrolled describe to profiles starts the table at its
// top.
func TestSwitchResetsScroll(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	f.checked[0].Folders = make([]string, 30)
	for i := range f.checked[0].Folders {
		f.checked[0].Folders[i] = fmt.Sprintf("Baganator_%d", i)
	}
	h := newHarness(t, f, Options{})
	h.keys("enter", "G", "2")
	if got := ansi.Strip(h.m.render()); !strings.Contains(got, "NAME") || !strings.Contains(got, "forever (*)") {
		t.Errorf("profiles view after a scrolled describe:\n%s", got)
	}
}

// The profiles table scrolls to keep its cursor in view.
func TestProfilesScroll(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	for i := range 20 {
		f.profiles = append(f.profiles, app.ProfileInfo{Name: fmt.Sprintf("test-%02d", i), Flavor: "retail", Path: fmt.Sprintf("/games/wow%02d/_retail_/Interface/AddOns", i)})
	}
	h := newHarness(t, f, Options{})
	h.send(tea.WindowSizeMsg{Width: minWidth, Height: minHeight})
	h.keys("2", "G")
	h.golden("profiles_scrolled")
	h.keys("ctrl+b")
	if h.m.pcursor != len(f.profiles)-1-h.m.pageSize() {
		t.Errorf("pcursor after ctrl+b = %d", h.m.pcursor)
	}
}

// enter twice in the form saves once.
func TestFormSavesOnce(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "n")
	h.m.form.fields[0].SetValue("/games/wow/_classic_")
	h.hold = true
	h.keys("enter", "enter")
	h.golden("profile_saving")
	h.release()
	if n := len(slices.DeleteFunc(slices.Clone(f.calls), func(c string) bool { return !strings.HasPrefix(c, "set ") })); n != 1 {
		t.Errorf("calls = %v, want one set", f.calls)
	}
}

// s can't empty a field: SetProfile would take it as unchanged.
func TestSetEmptied(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "s")
	h.m.form.fields[1].SetValue("")
	h.keys("enter")
	if len(f.calls) != 0 || h.m.form == nil || h.m.form.err == nil {
		t.Errorf("calls %v, form open %v; want nothing sent and the form's error", f.calls, h.m.form != nil)
	}
}

// A new path for the profile shown forgets what was checked, and checks
// again.
func TestSetPathChecksAgain(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("2", "s")
	h.m.form.fields[0].SetValue("/games/other/_forever_/Interface/AddOns")
	h.keys("enter")
	want := []string{`set name="forever" path="/games/other/_forever_/Interface/AddOns" flavor="" must=2`, "list forever", "check forever"}
	if diff := cmp.Diff(want, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// The addons view, with no profiles, points to the profiles view.
func TestNoProfilesAddonsView(t *testing.T) {
	t.Parallel()
	h := newHarness(t, &fake{}, Options{})
	h.keys("1")
	h.golden("no_profiles_addons")
}

// An older read of the profiles, answering after a later one, is
// dropped.
func TestStaleProfilesRead(t *testing.T) {
	t.Parallel()
	f := twoProfiles()
	h := newHarness(t, f, Options{})
	h.keys("2")
	stale := profilesMsg{read: h.m.preads - 1, profiles: f.profiles[:1]}
	h.send(stale)
	if len(h.m.profiles) != 2 {
		t.Errorf("%d profiles after a stale read, want 2", len(h.m.profiles))
	}
}

// The form's fields follow the window's width.
func TestFormResizes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, twoProfiles(), Options{})
	h.keys("2", "n")
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	if got, want := h.m.form.fields[0].Width(), h.m.formWidth(); got != want {
		t.Errorf("field width %d after resizing, want %d", got, want)
	}
}

// ghFromCLI is a GitHubKeyFrom that finds gh's token.
func ghFromCLI(context.Context) string { return "gh" }

// The sources view lists each source as sources list does: ready or
// needing a key, read through its API or website, and where its key is.
func TestSourcesView(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{GitHubKeyFrom: ghFromCLI})
	h.keys("3")
	h.golden("sources")
	h.keys("?")
	h.golden("sources_help")
}

// enter on a source asks for its key, masked; enter saves it. The key
// is never drawn, nor kept once the job has it, and what was checked for
// the source's addons is forgotten, since the key changes the answer.
func TestSetKey(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	const key = "cf-SECRET-0123456789"
	h.keys("3", "j", "enter")
	h.send(tea.PasteMsg{Content: key})
	screen := ansi.Strip(h.m.render())
	if strings.Contains(screen, key) || strings.Contains(screen, "SECRET") {
		t.Fatalf("the key was drawn:\n%s", screen)
	}
	h.golden("key_form")
	h.keys("enter")
	if diff := cmp.Diff([]string{fmt.Sprintf("set-key curseforge (%d chars)", len(key))}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
	if f.keys[0] != key {
		t.Errorf("SetSourceKey got %q, want the pasted key", f.keys[0])
	}
	if h.m.keyForm != nil {
		t.Error("the key form, with the key, is still in the model")
	}
	for _, s := range []string{h.m.flash, ansi.Strip(h.m.render()), fmt.Sprint(h.sent)} {
		if strings.Contains(s, "SECRET") {
			t.Errorf("the key appears in %q", s)
		}
	}
	if want := "Saved curseforge's API key. Press 1, then c, to check its addons again."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	if _, ok := h.m.known["forever"][weak]; ok {
		t.Error("WeakAuras, from CurseForge, is still known as checked")
	}
}

// An empty key isn't sent; one the environment overrides says so.
func TestSetKeyNotes(t *testing.T) {
	t.Parallel()
	f := sample()
	f.envSet = true
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("3", "j", "enter", "enter")
	if len(f.calls) != 0 || h.m.keyForm == nil || h.m.keyForm.err == nil {
		t.Fatalf("an empty key: calls %v; want none, and the form's error", f.calls)
	}
	h.m.keyForm.input.SetValue("abc")
	h.keys("enter")
	if !strings.Contains(h.m.flash, "$KNAPSACK_CURSEFORGE_API_KEY is set, and is used instead.") {
		t.Errorf("flash = %q, want the override noted", h.m.flash)
	}
}

// ctrl+d removes a key, after a confirm.
func TestUnsetKey(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("3", "j", "ctrl+d")
	h.golden("key_remove")
	h.keys("y")
	if diff := cmp.Diff([]string{"set-key curseforge (0 chars)"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// w switches between the official API and the website, saying when the
// API needs a key.
func TestToggleAPI(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("3", "j", "j", "w")
	// The message goes on to say how to check Wago's addons again.
	if want := "wago is read through its official API now, which needs a key: press enter."; !strings.HasPrefix(h.m.flash, want) {
		t.Errorf("flash = %q, want it to start %q", h.m.flash, want)
	}
	h.keys("w")
	if diff := cmp.Diff([]string{"set-api wago official", "set-api wago website"}, f.calls); diff != "" {
		t.Errorf("calls (-want +got):\n%s", diff)
	}
}

// GitHub's row says why keys and the website don't apply to it.
func TestGitHubSource(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	f.calls = nil
	h.keys("3", "enter")
	if h.m.keyForm != nil || !strings.Contains(h.m.flash, "never stored") {
		t.Errorf("enter on GitHub: form open %v, flash %q", h.m.keyForm != nil, h.m.flash)
	}
	h.keys("w")
	if len(f.calls) != 0 || !strings.Contains(h.m.flash, "only through its API") {
		t.Errorf("w on GitHub: calls %v, flash %q", f.calls, h.m.flash)
	}
}

// Forgetting what was checked for a source puts its rows back to
// unchecked, so the table and u agree.
func TestForgetSourceRows(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("3", "j", "enter")
	h.m.keyForm.input.SetValue("abc")
	h.keys("enter", "1")
	for _, r := range h.m.visible() {
		if r.Ref == weak && r.Status != app.StatusNotInConfig {
			t.Errorf("WeakAuras is %q; want it as it was, not in config, which no check changes", r.Status)
		}
		if r.Ref.Source == ref.CurseForge && r.Status != app.StatusInstalled && r.Status != app.StatusNotInConfig {
			t.Errorf("%s is %q after CurseForge's key changed, want unchecked", r.name, r.Status)
		}
	}
	// A CurseForge addon that was checked is unchecked again.
	f := sample()
	f.checked[4].Status, f.checked[4].Available = app.StatusUpdateAvailable, "5.18.0"
	f.local[4].Status = app.StatusInstalled
	h = newHarness(t, f, Options{})
	h.keys("3", "j", "enter")
	h.m.keyForm.input.SetValue("abc")
	h.keys("enter", "1")
	for _, r := range h.m.visible() {
		if r.Ref == weak && (r.Status != app.StatusInstalled || r.Available != "") {
			t.Errorf("WeakAuras is %q, %q after CurseForge's key changed; want unchecked", r.Status, r.Available)
		}
	}
}

// A key SetSourceKey refuses keeps the form open, empty, with why, to
// paste a corrected one.
func TestKeyRefused(t *testing.T) {
	t.Parallel()
	f := sample()
	f.keyErr = &app.UserError{Msg: "the API key has spaces or line breaks in it", Hint: "an API key is one word with no spaces"}
	h := newHarness(t, f, Options{})
	h.keys("3", "j", "enter")
	h.send(tea.PasteMsg{Content: "two words"})
	h.keys("enter")
	if h.m.keyForm == nil || h.m.keyForm.err == nil || h.m.keyForm.input.Value() != "" {
		t.Fatalf("form %+v; want it open, empty, with the error", h.m.keyForm)
	}
	h.golden("key_refused")
}

// The key field follows the window's width.
func TestKeyFormResizes(t *testing.T) {
	t.Parallel()
	h := newHarness(t, sample(), Options{})
	h.keys("3", "j", "enter")
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	if got, want := h.m.keyForm.input.Width(), h.m.formWidth(); got != want {
		t.Errorf("key field width %d after resizing, want %d", got, want)
	}
}

// Where GitHub's token comes from is looked for once a session.
func TestGitHubKeyOnce(t *testing.T) {
	t.Parallel()
	looks := 0
	h := newHarness(t, sample(), Options{GitHubKeyFrom: func(context.Context) string { looks++; return "gh" }})
	h.keys("3", "1", "3", "j", "w")
	if looks != 1 || h.m.githubKey != "gh" {
		t.Errorf("looked %d times, found %q; want once, gh", looks, h.m.githubKey)
	}
}

// Saving the key config already has forgets nothing.
func TestSameKeyForgetsNothing(t *testing.T) {
	t.Parallel()
	f := sample()
	f.sameKey = true
	f.checked[4].Status = app.StatusUpdateAvailable
	h := newHarness(t, f, Options{})
	h.keys("3", "j", "enter")
	h.m.keyForm.input.SetValue("abc")
	h.keys("enter")
	if want := "curseforge already had that key."; h.m.flash != want {
		t.Errorf("flash = %q, want %q", h.m.flash, want)
	}
	if _, ok := h.m.known["forever"][weak]; !ok {
		t.Error("what was known about WeakAuras was forgotten for an unchanged key")
	}
}

// A failed read keeps the sources last read, with the error.
func TestSourcesReadFails(t *testing.T) {
	t.Parallel()
	f := sample()
	h := newHarness(t, f, Options{})
	h.keys("3")
	f.sourcesErr = errors.New("config.toml: permission denied")
	h.keys("1", "3")
	if len(h.m.sources) != 3 || h.m.err == nil {
		t.Errorf("%d sources, err %v; want the 3 read before, and the error", len(h.m.sources), h.m.err)
	}
}
