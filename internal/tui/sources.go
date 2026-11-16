package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
)

// sourcesMsg is every source, and where GitHub's token comes from.
type sourcesMsg struct {
	read      int // which read, of m.sreads
	sources   []app.SourceInfo
	githubKey string // "GITHUB_TOKEN", "GH_TOKEN", "gh" or ""
	// githubKnown is whether githubKey was looked for on this read.
	githubKnown bool
	err         error
}

// sourcesCmd reads the sources, as profilesCmd reads the profiles:
// config alone, not as a job, numbered so only the latest read is used.
// Finding GitHub's token may run gh, which is why it's here, off the
// event loop.
// Where GitHub's token comes from is found once a session, on the first
// read: it may run gh, and it hardly ever changes.
func (m *model) sourcesCmd() tea.Cmd {
	m.sreads++
	a, ctx, read := m.a, m.ctx, m.sreads
	var ghKey func(context.Context) string
	if !m.githubKeyKnown {
		ghKey = m.opts.GitHubKeyFrom
	}
	return func() tea.Msg {
		ss, err := a.Sources(ctx)
		msg := sourcesMsg{read: read, sources: ss, err: err}
		if err == nil && ghKey != nil {
			msg.githubKey, msg.githubKnown = ghKey(ctx), true
		}
		return msg
	}
}

// keyFrom is the KEY column, as sources list shows it, with "—" for a
// source with no key.
func (m *model) keyFrom(s app.SourceInfo) string {
	if d := app.DescribeKey(s, app.KeyFrom(s, m.githubKey)); d != "" {
		return d
	}
	return "—"
}

// sourceDoneMsg is what a change to a source did.
type sourceDoneMsg struct {
	source  string
	key     bool // from the key form, which shows its error
	changed bool // whether config changed: a new key, or a new way of reading
	flash   string
	err     error
}

func (m *model) sourcesKey(s string) (tea.Cmd, bool) {
	var src *app.SourceInfo
	if len(m.sources) > 0 {
		src = &m.sources[m.scursor]
	}
	switch s {
	case "enter", "ctrl+d", "w":
		if src == nil {
			return nil, true
		}
		if !src.NeedsKey {
			// GitHub: its token is never stored, and it has only its API.
			m.flash = "GitHub's token is never stored: set GITHUB_TOKEN or GH_TOKEN, or log in with `gh auth login`."
			if s == "w" {
				m.flash = "GitHub is read only through its API."
			}
			return nil, true
		}
		switch s {
		case "enter":
			return m.openKeyForm(src.Name), true
		case "ctrl+d":
			m.askUnsetKey(*src)
			return nil, true
		}
		return m.toggleAPI(*src), true
	case "j", "down":
		m.scursor++
	case "k", "up":
		m.scursor--
	case "g", "home":
		m.scursor = 0
	case "G", "end":
		m.scursor = len(m.sources) - 1
	default:
		return nil, false
	}
	m.scursor = max(0, min(m.scursor, len(m.sources)-1))
	return nil, true
}

// toggleAPI switches a source between its official API and its website.
func (m *model) toggleAPI(s app.SourceInfo) tea.Cmd {
	to := string(config.APIWebsite)
	if s.API == config.APIWebsite {
		to = string(config.APIOfficial)
	}
	a, name := m.a, s.Name
	return m.configJob("saving", func(ctx context.Context) tea.Msg {
		res, err := a.SetSourceAPI(ctx, name, to)
		msg := sourceDoneMsg{source: name, changed: res.Changed, err: err}
		switch {
		case err != nil:
		case res.API == config.APIWebsite:
			msg.flash = name + " is read from its website now."
		case res.NeedsKey:
			msg.flash = name + " is read through its official API now, which needs a key: press enter."
		default:
			msg.flash = name + " is read through its official API now."
		}
		return msg
	})
}

func (m *model) askUnsetKey(s app.SourceInfo) {
	a, name := m.a, s.Name
	m.bodyOffset = 0
	m.confirm = &confirmModal{
		title:    "key",
		subject:  name,
		question: "Remove " + name + "'s API key from config?",
		note:     "Every profile shares it. The source can't be read through its official API without one.",
		yes:      "remove",
		onYes: func() tea.Cmd {
			return m.configJob("saving", func(ctx context.Context) tea.Msg {
				res, err := a.SetSourceKey(ctx, name, "")
				return sourceDoneMsg{source: name, changed: res.Changed, err: err, flash: keyFlash(name, res, false)}
			})
		},
	}
}

// keyFlash says what setting or removing a key did, with what overrides
// or ignores it, as sources set-key notes.
func keyFlash(name string, res app.SetSourceKeyResult, set bool) string {
	var s string
	switch {
	case set && res.Changed:
		s = "Saved " + name + "'s API key."
	case set:
		s = name + " already had that key."
	case res.Changed:
		s = "Removed " + name + "'s API key."
	default:
		s = name + " had no key in config."
	}
	if res.Overridden {
		s += " $" + res.Env + " is set, and is used instead."
	}
	if set && res.Website {
		s += " It's read from its website, which doesn't use the key: w reads it through its API."
	}
	return s
}

// keyForm is the masked input for a source's API key (DESIGN.md §9.5).
// The key is never drawn, and isn't kept once the job has it.
type keyForm struct {
	source string
	input  textinput.Model
	err    error
	saving bool // the key is with SetSourceKey: enter waits for it
}

func (m *model) openKeyForm(source string) tea.Cmd {
	t := textinput.New()
	t.Prompt = ""
	t.EchoMode = textinput.EchoPassword
	t.EchoCharacter = '•'
	t.Placeholder = "paste the key"
	t.SetWidth(m.formWidth())
	if !m.blink {
		st := t.Styles()
		st.Cursor.Blink = false
		t.SetStyles(st)
	}
	m.keyForm = &keyForm{source: source, input: t}
	return m.keyForm.input.Focus()
}

func (m *model) keyFormKey(k tea.KeyPressMsg) tea.Cmd {
	switch s := k.String(); s {
	case "esc":
		m.keyForm = nil
		return nil
	case "ctrl+c":
		return m.globalKey(s)
	case "enter":
		if m.keyForm.saving {
			return nil
		}
		return m.submitKey()
	}
	return m.keyInput(k)
}

func (m *model) keyInput(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	m.keyForm.input, cmd = m.keyForm.input.Update(msg)
	return cmd
}

// submitKey sends the key to SetSourceKey, which checks it as set-key
// does, and drops it from the model.
func (m *model) submitKey() tea.Cmd {
	f := m.keyForm
	key := strings.TrimSpace(f.input.Value())
	if key == "" {
		f.err = &app.UserError{Msg: "no key was typed", Hint: "paste it, or esc to cancel; ctrl+d removes a key"}
		return nil
	}
	// The key leaves the model with the job: the form stays, empty, to
	// show what SetSourceKey makes of it, as checkKey may refuse it.
	f.input.SetValue("")
	f.err, f.saving = nil, true
	a, name := m.a, f.source
	return m.configJob("saving", func(ctx context.Context) tea.Msg {
		res, err := a.SetSourceKey(ctx, name, key)
		return sourceDoneMsg{source: name, changed: res.Changed, err: err, flash: keyFlash(name, res, true), key: true}
	})
}

func (m *model) keyFormLines(width int) []string {
	f := m.keyForm
	lines := wrap(width, "The API key for "+boldStyle.Render(f.source)+", shared by every profile, written to config "+
		"readable only by you:", "")
	lines = append(lines, ansi.Truncate(accentStyle.Render(pad("Key:", 9))+f.input.View(), width, "…"))
	switch {
	case f.saving:
		lines = append(lines, "", faintStyle.Render(m.spinner()+" saving"))
	case f.err != nil:
		lines = append(lines, "")
		lines = append(lines, errorLines(width, f.err, m.opts.Running)...)
	}
	return lines
}

// sourceResult handles what a change to a source did: says so, reads the
// sources again, and forgets what was checked for that source's addons,
// which a key or a way of reading changes the answer for.
func (m *model) sourceResult(msg sourceDoneMsg) tea.Cmd {
	if msg.key && m.keyForm != nil {
		if m.keyForm.saving = false; msg.err != nil {
			m.keyForm.err = msg.err // to paste a corrected one
			return m.sourcesCmd()
		}
		m.keyForm = nil
	}
	if m.err = msg.err; msg.err == nil {
		m.flash = msg.flash
		if msg.changed && m.forgetSource(msg.source) {
			m.flash += " Press 1, then c, to check its addons again."
		}
	}
	return m.sourcesCmd()
}

// forgetSource forgets what was checked for source's addons, in every
// profile, and puts the rows shown back to unchecked, so the table, its
// counts and u agree. It reports whether anything was forgotten.
func (m *model) forgetSource(source string) bool {
	forgot := false
	for _, k := range m.known {
		for r := range k {
			if r.Source == source {
				delete(k, r)
				forgot = true
			}
		}
	}
	for i, r := range m.rows {
		if r.Ref.Source != source {
			continue
		}
		switch r.Status {
		case app.StatusUpToDate, app.StatusUpdateAvailable, app.StatusUnknown:
			m.rows[i].Status, m.rows[i].Available, m.rows[i].Err = app.StatusInstalled, "", nil
			forgot = true
		case app.StatusInstalled, app.StatusMissing, app.StatusNotInConfig, app.StatusUnmanaged, app.StatusChecking:
		}
	}
	return forgot
}

// sourcesTable is the sources view: NAME, STATUS, READ VIA and KEY, as
// sources list shows them.
func (m *model) sourcesTable(width int) (string, []string) {
	title := accentStyle.Render("sources") + fmt.Sprintf("[%d]", len(m.sources))
	if !m.sourcesRead {
		return title, []string{faintStyle.Render(m.spinner() + " loading")}
	}
	headers := []string{"NAME", "STATUS", "READ VIA", "KEY"}
	widths := []int{4, 6, 8, 3}
	cells := make([][]string, len(m.sources))
	for i, s := range m.sources {
		via := "official API"
		if s.API == config.APIWebsite {
			via = "website"
		}
		cells[i] = []string{s.Name, s.Status(), via, m.keyFrom(s)}
		for j, c := range cells[i] {
			widths[j] = max(widths[j], ansi.StringWidth(c))
		}
	}
	const gap = 3
	line := func(cs []string) string {
		parts := make([]string, len(cs))
		for j, c := range cs {
			parts[j] = pad(c, widths[j])
		}
		return "  " + strings.Join(parts, strings.Repeat(" ", gap))
	}
	lines := []string{boldStyle.Render(line(headers))}
	for i, cs := range cells {
		l := line(cs)
		switch {
		case i == m.scursor:
			l = "  " + cursorStyle.Render(pad(strings.TrimPrefix(l, "  "), width-2))
		case !m.sources[i].Ready():
			// The status column, s.Status(), is the row's second.
			l = strings.Replace(l, cs[1], warnStyle.Render(cs[1]), 1)
		}
		lines = append(lines, ansi.Truncate(l, width, "…"))
	}
	return title, lines
}
