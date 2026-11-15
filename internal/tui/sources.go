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
	err       error
}

// sourcesCmd reads the sources, as profilesCmd reads the profiles:
// config alone, not as a job, numbered so only the latest read is used.
// Finding GitHub's token may run gh, which is why it's here, off the
// event loop.
func (m *model) sourcesCmd() tea.Cmd {
	m.sreads++
	a, ctx, read, ghKey := m.a, m.ctx, m.sreads, m.opts.GitHubKeyFrom
	return func() tea.Msg {
		ss, err := a.Sources(ctx)
		msg := sourcesMsg{read: read, sources: ss, err: err}
		if ghKey != nil {
			msg.githubKey = ghKey(ctx)
		}
		return msg
	}
}

// sourceStatus and keyFrom are the STATUS and KEY columns, as sources
// list shows them.
func sourceStatus(s app.SourceInfo) string {
	if !s.Ready() {
		return "needs key"
	}
	return "ready"
}

func (m *model) keyFrom(s app.SourceInfo) string {
	switch {
	case !s.NeedsKey && m.githubKey == "gh":
		return "from gh auth token"
	case !s.NeedsKey && m.githubKey != "":
		return "from $" + m.githubKey
	case !s.NeedsKey:
		return "none (60 requests an hour)"
	case s.Key == app.KeyEnv:
		return "from $" + s.Env
	case s.Key == app.KeyConfig:
		return "set in config"
	}
	return "—"
}

// sourceDoneMsg is what a change to a source did.
type sourceDoneMsg struct {
	source string
	flash  string
	err    error
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

// sourceJob changes a source's settings in config.
func (m *model) sourceJob(name string, run func(ctx context.Context) tea.Msg) tea.Cmd {
	m.flash, m.err = "", nil
	return m.enqueue(job{kind: jobProfile, name: name, writes: true, run: run})
}

// toggleAPI switches a source between its official API and its website.
func (m *model) toggleAPI(s app.SourceInfo) tea.Cmd {
	to := string(config.APIWebsite)
	if s.API == config.APIWebsite {
		to = string(config.APIOfficial)
	}
	a, name := m.a, s.Name
	return m.sourceJob("saving", func(ctx context.Context) tea.Msg {
		res, err := a.SetSourceAPI(ctx, name, to)
		msg := sourceDoneMsg{source: name, err: err}
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
			return m.sourceJob("saving", func(ctx context.Context) tea.Msg {
				res, err := a.SetSourceKey(ctx, name, "")
				return sourceDoneMsg{source: name, err: err, flash: keyFlash(name, res, false)}
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
	m.keyForm = nil // the key leaves the model with the job
	a, name := m.a, f.source
	return m.sourceJob("saving", func(ctx context.Context) tea.Msg {
		res, err := a.SetSourceKey(ctx, name, key)
		return sourceDoneMsg{source: name, err: err, flash: keyFlash(name, res, true)}
	})
}

func (m *model) keyFormLines(width int) []string {
	f := m.keyForm
	lines := wrap(width, "The API key for "+boldStyle.Render(f.source)+", shared by every profile, written to config "+
		"readable only by you:", "")
	lines = append(lines, ansi.Truncate(accentStyle.Render(pad("Key:", 9))+f.input.View(), width, "…"))
	if f.err != nil {
		lines = append(lines, "")
		lines = append(lines, errorLines(width, f.err, m.opts.Running)...)
	}
	return lines
}

// sourceResult handles what a change to a source did: says so, reads the
// sources again, and forgets what was checked for that source's addons,
// which a key or a way of reading changes the answer for.
func (m *model) sourceResult(msg sourceDoneMsg) tea.Cmd {
	if m.err = msg.err; msg.err == nil {
		m.flash = msg.flash
		changed := false
		for _, k := range m.known {
			for r := range k {
				if r.Source == msg.source {
					delete(k, r)
					changed = true
				}
			}
		}
		if changed {
			m.flash += " c checks its addons again."
		}
	}
	return m.sourcesCmd()
}

// sourcesTable is the sources view: NAME, STATUS, READ VIA and KEY, as
// sources list shows them.
func (m *model) sourcesTable(width int) (string, []string) {
	title := accentStyle.Render("sources") + fmt.Sprintf("[%d]", len(m.sources))
	headers := []string{"NAME", "STATUS", "READ VIA", "KEY"}
	widths := []int{4, 6, 8, 3}
	cells := make([][]string, len(m.sources))
	for i, s := range m.sources {
		via := "official API"
		if s.API == config.APIWebsite {
			via = "website"
		}
		cells[i] = []string{s.Name, sourceStatus(s), via, m.keyFrom(s)}
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
		case cs[1] == "needs key":
			l = strings.Replace(l, "needs key", warnStyle.Render("needs key"), 1)
		}
		lines = append(lines, ansi.Truncate(l, width, "…"))
	}
	return title, lines
}
