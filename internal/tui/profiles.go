package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/austinrr/knapsack/internal/app"
)

// The views, switched with 1–2 or tab (DESIGN.md §9.2).
const (
	viewAddons = iota
	viewProfiles
	viewCount
)

var viewNames = [viewCount]string{"addons", "profiles"}

// profilesMsg is every profile, from config alone.
type profilesMsg struct {
	profiles []app.ProfileInfo
	err      error
}

// profilesCmd reads the profiles. It isn't a job: Profiles reads only
// config, without a profile's lock, so it needn't wait for one
// (DESIGN.md §9.4).
func (m *model) profilesCmd() tea.Cmd {
	a, ctx := m.a, m.ctx
	return func() tea.Msg {
		ps, err := a.Profiles(ctx)
		return profilesMsg{profiles: ps, err: err}
	}
}

// switchView shows view v, reading the profiles afresh for their view.
func (m *model) switchView(v int) tea.Cmd {
	m.view = (v + viewCount) % viewCount
	if m.view == viewProfiles {
		return m.profilesCmd()
	}
	return nil
}

// show shows profile name's addons, as after using it, read afresh and
// checked as any profile is the first time.
func (m *model) show(name string) tea.Cmd {
	m.profile, m.path = name, ""
	m.loaded, m.loadErr, m.noProfiles = false, nil, false
	m.rows, m.hidden = nil, 0
	m.cursor, m.offset, m.query = 0, 0, ""
	m.filter.SetValue("")
	m.describing = false
	clear(m.marks)
	m.view = viewAddons
	return m.enqueue(m.loadJob())
}

// usedMsg, profileSetMsg, autoMsg, deletedMsg and editedMsg are what the
// profile jobs did.
type (
	usedMsg struct {
		name string
		err  error
	}
	profileSetMsg struct {
		req app.SetProfileRequest
		res app.SetProfileResult
		err error
	}
	autoMsg struct {
		res app.AutoProfilesResult
		err error
	}
	deletedMsg struct {
		name string
		res  app.DeleteProfileResult
		err  error
	}
	editedMsg struct {
		name  string
		saved bool
		res   app.ProfileEditResult
		err   error
	}
)

// profileJob is a job that changes config: quick, but a write.
func (m *model) profileJob(name string, run func(ctx context.Context) tea.Msg) tea.Cmd {
	m.flash, m.err = "", nil
	return m.enqueue(job{kind: jobProfile, name: name, writes: true, run: run})
}

func (m *model) profilesKey(s string) (tea.Cmd, bool) {
	var p *app.ProfileInfo
	if len(m.profiles) > 0 {
		p = &m.profiles[m.pcursor]
	}
	a := m.a
	switch s {
	case "enter":
		if p == nil {
			return nil, true
		}
		name := p.Name
		return m.profileJob("switching", func(ctx context.Context) tea.Msg {
			return usedMsg{name: name, err: a.UseProfile(ctx, name)}
		}), true
	case "n":
		return m.openProfileForm(nil), true
	case "s":
		if p == nil {
			return nil, true
		}
		return m.openProfileForm(p), true
	case "A":
		return m.profileJob("finding the game", func(ctx context.Context) tea.Msg {
			res, err := a.AutoProfiles(ctx, "")
			return autoMsg{res: res, err: err}
		}), true
	case "e":
		if p == nil {
			return nil, true
		}
		return m.edit(p.Name), true
	case "ctrl+d":
		if p == nil {
			return nil, true
		}
		m.askDeleteProfile(p.Name)
		return nil, true
	case "j", "down":
		m.pcursor++
	case "k", "up":
		m.pcursor--
	case "g", "home":
		m.pcursor = 0
	case "G", "end":
		m.pcursor = len(m.profiles) - 1
	default:
		return nil, false
	}
	m.pcursor = max(0, min(m.pcursor, len(m.profiles)-1))
	return nil, true
}

func (m *model) askDeleteProfile(name string) {
	a := m.a
	m.bodyOffset = 0
	m.confirm = &confirmModal{
		title:    "delete",
		question: fmt.Sprintf("Delete profile %s?", name),
		note: "Its addon files stay in AddOns; only Knapsack's record of the profile " +
			"and what it installed goes. To manage them again, make the profile again and add them.",
		yes: "delete",
		onYes: func() tea.Cmd {
			return m.profileJob("deleting", func(ctx context.Context) tea.Msg {
				res, err := a.DeleteProfile(ctx, name)
				return deletedMsg{name: name, res: res, err: err}
			})
		},
	}
}

// profileResult handles what a profile job did: says so, reads the
// profiles again, and shows a profile's addons when it's become current
// or its addons changed.
func (m *model) profileResult(msg tea.Msg) tea.Cmd {
	cmds := []tea.Cmd{m.profilesCmd()}
	switch msg := msg.(type) {
	case usedMsg:
		if m.err = msg.err; msg.err == nil {
			m.flash = "Switched to profile " + msg.name + "."
			cmds = append(cmds, m.show(msg.name))
		}
	case profileSetMsg:
		if msg.err != nil {
			// The form is still open: it shows the error, with its hint.
			if m.form != nil {
				m.form.err = msg.err
			} else {
				m.err = msg.err
			}
			return tea.Batch(cmds...)
		}
		m.form = nil
		name := msg.res.Profile.Name
		switch {
		case msg.res.Created:
			m.flash = "Created profile " + name + "."
		case msg.res.Changed:
			m.flash = "Saved profile " + name + "."
		default:
			m.flash = "Profile " + name + " unchanged."
		}
		switch {
		case msg.res.MadeCurrent || m.profile == "":
			cmds = append(cmds, m.show(name))
		case name == m.profile && msg.res.Changed:
			cmds = append(cmds, m.enqueue(m.loadJob()))
		}
	case autoMsg:
		if m.err = msg.err; msg.err != nil {
			return tea.Batch(cmds...)
		}
		m.showAutoResult(msg.res)
		for _, p := range msg.res.Profiles {
			if p.Result.MadeCurrent {
				cmds = append(cmds, m.show(p.Result.Profile.Name))
				m.view = viewProfiles // the result is shown over this view
			}
		}
	case deletedMsg:
		if m.err = msg.err; msg.err != nil {
			return tea.Batch(cmds...)
		}
		m.flash = "Deleted profile " + msg.name + "; its addon files are untouched."
		delete(m.known, msg.name)
		delete(m.checked, msg.name)
		if msg.name == m.profile {
			// None is current now; the addons view says so, with the fix.
			m.profile = ""
			cmds = append(cmds, m.show(""))
			m.view = viewProfiles
		}
	case editedMsg:
		if m.err = msg.err; msg.err != nil {
			return tea.Batch(cmds...)
		}
		m.flash = editSummary(msg)
		if msg.saved && msg.res.Changed && msg.name == m.profile {
			cmds = append(cmds, m.enqueue(m.loadJob()))
		}
	}
	return tea.Batch(cmds...)
}

// editSummary says what an edit did, and what installs or uninstalls the
// addons it added or took out, as profiles edit's notes do.
func editSummary(msg editedMsg) string {
	if !msg.saved || !msg.res.Changed {
		return "Profile " + msg.name + " unchanged."
	}
	s := "Saved profile " + msg.name + "."
	if n := len(msg.res.Added); n > 0 {
		s += fmt.Sprintf(" %s added: U installs them.", count(n, "addon"))
	}
	if n := len(msg.res.Removed); n > 0 {
		s += fmt.Sprintf(" %s taken out: U with their prune boxes uninstalls them.", count(n, "addon"))
	}
	return s
}

// showAutoResult shows what AutoProfiles found and did, as profiles
// create --auto reports it.
func (m *model) showAutoResult(res app.AutoProfilesResult) {
	var lines []string
	game, clientWidth := "", 0
	for _, p := range res.Profiles {
		clientWidth = max(clientWidth, ansi.StringWidth(p.Client))
	}
	for _, p := range res.Profiles {
		if p.Game != game {
			game = p.Game
			lines = append(lines, "In "+app.ShortenHome(game, m.opts.Home)+":")
		}
		var what string
		switch p.Skip {
		case app.NotSkipped:
			what = boldStyle.Render(p.Result.Profile.Name) + "  already set up"
			if p.Result.Created {
				what = boldStyle.Render(p.Result.Profile.Name) + "  created"
			}
		case app.SkipNoAddOns:
			what = faintStyle.Render("skipped: never started, no Interface/AddOns yet")
		case app.SkipNameTaken:
			var taken *app.NameTakenError
			if errors.As(p.Err, &taken) {
				what = faintStyle.Render(fmt.Sprintf("skipped: %q already uses %s; n makes this copy a profile of its own",
					taken.Name, app.ShortenHome(taken.Path, m.opts.Home)))
			}
		case app.SkipFailed:
			what = errorStyle.Render("skipped: ") + p.Err.Error()
		}
		lines = append(lines, "  "+pad(p.Client, clientWidth)+"  "+what)
	}
	m.bodyOffset = 0
	m.confirm = &confirmModal{
		title:    "profiles",
		question: "Found World of Warcraft:",
		lines:    lines,
		info:     true,
	}
}

// profileForm makes a profile (n) or changes one (s): its AddOns path,
// name and flavor, for SetProfile, which expands ~ and completes a client
// folder to Interface/AddOns, as --path does (DESIGN.md §9.5).
type profileForm struct {
	existing *app.ProfileInfo // the profile s changes, or nil for n
	fields   []textinput.Model
	labels   []string
	focus    int
	err      error
}

func (m *model) openProfileForm(p *app.ProfileInfo) tea.Cmd {
	f := &profileForm{existing: p}
	add := func(label, value, placeholder string) {
		t := textinput.New()
		t.Prompt = ""
		t.Placeholder = placeholder
		t.SetValue(value)
		t.SetWidth(max(10, m.width-20))
		if !m.blink {
			st := t.Styles()
			st.Cursor.Blink = false
			t.SetStyles(st)
		}
		f.fields = append(f.fields, t)
		f.labels = append(f.labels, label)
	}
	if p == nil {
		add("Path", "", "the client folder, such as …/World of Warcraft/_retail_")
		add("Name", "", "optional: the flavor's name, such as retail")
		add("Flavor", "", "optional: worked out from the path")
	} else {
		add("Path", p.Path, "")
		add("Flavor", string(p.Flavor), "")
	}
	m.form = f
	m.bodyOffset = 0
	return f.fields[0].Focus()
}

func (m *model) formKey(k tea.KeyPressMsg) tea.Cmd {
	f := m.form
	switch s := k.String(); s {
	case "esc":
		m.form = nil
		return nil
	case "ctrl+c":
		return m.globalKey(s)
	case "tab", "down":
		return f.move(1)
	case "shift+tab", "up":
		return f.move(-1)
	case "enter":
		return m.submitProfileForm()
	}
	return m.formInput(k)
}

// formInput is a key or a paste for the focused field.
func (m *model) formInput(msg tea.Msg) tea.Cmd {
	f := m.form
	var cmd tea.Cmd
	f.fields[f.focus], cmd = f.fields[f.focus].Update(msg)
	return cmd
}

func (f *profileForm) move(by int) tea.Cmd {
	f.fields[f.focus].Blur()
	f.focus = (f.focus + by + len(f.fields)) % len(f.fields)
	return f.fields[f.focus].Focus()
}

func (f *profileForm) value(label string) string {
	for i, l := range f.labels {
		if l == label {
			return strings.TrimSpace(f.fields[i].Value())
		}
	}
	return ""
}

// submitProfileForm sends the form to SetProfile. Changing a profile
// sends only the fields that changed, as profiles set takes them.
func (m *model) submitProfileForm() tea.Cmd {
	f := m.form
	req := app.SetProfileRequest{Path: f.value("Path"), Name: f.value("Name"), Flavor: f.value("Flavor"), Must: app.MustBeNew}
	if p := f.existing; p != nil {
		req = app.SetProfileRequest{Name: p.Name, Must: app.MustExist}
		if path := f.value("Path"); path != p.Path {
			req.Path = path
		}
		if fl := f.value("Flavor"); fl != string(p.Flavor) {
			req.Flavor = fl
		}
		if req.Path == "" && req.Flavor == "" {
			m.form = nil
			m.flash = "Profile " + p.Name + " unchanged."
			return nil
		}
	} else if req.Path == "" {
		f.err = errors.New("the path is needed: the client folder, or its Interface/AddOns")
		return nil
	}
	f.err = nil
	a := m.a
	return m.profileJob("saving", func(ctx context.Context) tea.Msg {
		res, err := a.SetProfile(ctx, req)
		return profileSetMsg{req: req, res: res, err: err}
	})
}

// formLines draws the form.
func (m *model) formLines(width int) []string {
	f := m.form
	var lines []string
	if f.existing == nil {
		lines = wrap(width, "A new profile, for one game client's AddOns folder:", "")
	} else {
		lines = wrap(width, "Change profile "+boldStyle.Render(f.existing.Name)+"; its addons stay:", "")
	}
	for i, t := range f.fields {
		label := pad(f.labels[i]+":", 9)
		if i == f.focus {
			label = accentStyle.Render(label)
		} else {
			label = faintStyle.Render(label)
		}
		lines = append(lines, ansi.Truncate(label+t.View(), width, "…"))
	}
	if f.err != nil {
		lines = append(lines, "")
		lines = append(lines, errorLines(width, f.err, m.opts.Running)...)
	}
	return lines
}

// edit hands the terminal to the editor on a profile, as profiles edit
// does, then reads it again (DESIGN.md §9.6).
func (m *model) edit(name string) tea.Cmd {
	if m.opts.Edit == nil {
		return nil
	}
	m.flash, m.err = "", nil
	edit, ctx := m.opts.Edit, m.ctx
	var done editedMsg
	run := execFunc(func() error {
		done.saved, done.res, done.err = edit(ctx, name)
		return nil
	})
	return m.exec(run, func(error) tea.Msg {
		done.name = name
		return done
	})
}

// execFunc runs a function as tea.Exec's command, once Bubble Tea has
// given up the terminal. The editor uses the terminal itself, so the
// streams it's offered aren't needed.
type execFunc func() error

func (f execFunc) Run() error        { return f() }
func (execFunc) SetStdin(io.Reader)  {}
func (execFunc) SetStdout(io.Writer) {}
func (execFunc) SetStderr(io.Writer) {}

// profilesTable is the profiles view: NAME, with (*) for the current
// one, FLAVOR, how many ADDONS config has, and PATH.
func (m *model) profilesTable(width int) (string, []string) {
	title := accentStyle.Render("profiles") + fmt.Sprintf("[%d]", len(m.profiles))
	if len(m.profiles) == 0 {
		return title, wrap(width,
			"No profiles yet.",
			"",
			"Press "+boldStyle.Render("A")+" to find World of Warcraft and make a profile for each game client, or "+
				boldStyle.Render("n")+" to make one by path.")
	}
	headers := []string{"NAME", "FLAVOR", "ADDONS", "PATH"}
	cells := make([][]string, len(m.profiles))
	widths := []int{4, 6, 6, 4}
	for i, p := range m.profiles {
		name := p.Name
		if p.Current {
			name += " (*)"
		}
		cells[i] = []string{name, string(p.Flavor), fmt.Sprint(len(p.Addons)), app.ShortenHome(p.Path, m.opts.Home)}
		for j, c := range cells[i][:3] {
			widths[j] = max(widths[j], ansi.StringWidth(c))
		}
	}
	const gap = 3
	widths[3] = max(4, width-2-widths[0]-widths[1]-widths[2]-3*gap)
	line := func(cs []string) string {
		parts := make([]string, len(cs))
		for j, c := range cs {
			if j == 3 {
				c = cutMiddle(c, widths[3])
			}
			parts[j] = pad(ansi.Truncate(c, widths[j], "…"), widths[j])
		}
		return "  " + strings.Join(parts, strings.Repeat(" ", gap))
	}
	lines := []string{boldStyle.Render(line(headers))}
	for i, cs := range cells {
		l := line(cs)
		if i == m.pcursor {
			l = "  " + cursorStyle.Render(pad(strings.TrimPrefix(l, "  "), width-2))
		}
		lines = append(lines, ansi.Truncate(l, width, "…"))
	}
	return title, lines
}
