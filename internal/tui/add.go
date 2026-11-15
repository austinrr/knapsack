package tui

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// addModal is the add dialog: refs or URLs, one per line, so a list can
// be pasted, and a Force box (DESIGN.md §9.5).
type addModal struct {
	text     textarea.Model
	force    bool
	onForce  bool     // the Force box has the focus, not the text
	problems []string // why lines didn't parse, from the last ctrl+s
}

func (m *model) openAdd() tea.Cmd {
	t := textarea.New()
	t.Placeholder = "github:owner/repo, or a GitHub, CurseForge or Wago link"
	t.SetHeight(5)
	t.SetWidth(max(20, m.width-6))
	if !m.blink {
		st := t.Styles()
		st.Cursor.Blink = false
		t.SetStyles(st)
	}
	m.add = &addModal{text: t}
	m.bodyOffset = 0
	return m.add.text.Focus()
}

// addMsg is a key or a paste, for the add dialog's text.
func (m *model) addInput(msg tea.Msg) tea.Cmd {
	if m.add.onForce {
		return nil
	}
	var cmd tea.Cmd
	m.add.text, cmd = m.add.text.Update(msg)
	return cmd
}

func (m *model) addKey(k tea.KeyPressMsg) tea.Cmd {
	a := m.add
	switch s := k.String(); s {
	case "esc":
		m.add = nil
	case "ctrl+c":
		return m.globalKey(s)
	case "tab", "shift+tab":
		a.onForce = !a.onForce
		if a.onForce {
			a.text.Blur()
			return nil
		}
		return a.text.Focus()
	case "ctrl+s":
		return m.submitAdd()
	case "space":
		if a.onForce {
			a.force = !a.force
			return nil
		}
		return m.addInput(k)
	default:
		return m.addInput(k)
	}
	return nil
}

// submitAdd parses every line; if any fails, the dialog stays open with
// why and nothing runs, as a malformed ref is a usage error in the CLI
// (DESIGN.md §8.6).
func (m *model) submitAdd() tea.Cmd {
	a := m.add
	var refs []ref.Ref
	a.problems = nil
	for i, line := range strings.Split(a.text.Value(), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		r, err := ref.Parse(line)
		if err != nil {
			a.problems = append(a.problems, fmt.Sprintf("line %d: %v", i+1, err))
			continue
		}
		refs = append(refs, r)
	}
	switch {
	case len(a.problems) > 0:
		return nil
	case len(refs) == 0:
		a.problems = []string{"type a ref or link, one per line"}
		return nil
	}
	m.add = nil
	return m.runAdd(refs, a.force)
}

// runAdd queues Add for the refs.
func (m *model) runAdd(refs []ref.Ref, force bool) tea.Cmd {
	a, profile := m.a, m.profile
	m.flash, m.err = "", nil
	return m.enqueue(job{kind: jobAdd, profile: profile, name: "adding", writes: true,
		run: func(ctx context.Context) tea.Msg {
			res, err := a.Add(ctx, profile, refs, force)
			return doneMsg{kind: jobAdd, profile: profile, refs: refs, results: res, err: err}
		}})
}

// addLines draws the dialog.
func (m *model) addLines(width int) []string {
	a := m.add
	lines := []string{"Refs or links to add to " + m.profile + ", one per line:", ""}
	lines = append(lines, strings.Split(a.text.View(), "\n")...)
	for _, p := range a.problems {
		lines = append(lines, wrap(width, errorStyle.Render("✗ ")+p)...)
	}
	box := "[ ]"
	if a.force {
		box = "[x]"
	}
	force := box + " Force: take over folders no addon owns"
	if a.onForce {
		force = cursorStyle.Render(force)
	}
	return append(lines, "", force)
}

// openPage opens url in the browser, saying so in the status line.
func (m *model) openPage(what, url string) tea.Cmd {
	if url == "" {
		return nil
	}
	open, ctx := m.openURL, m.ctx
	return func() tea.Msg {
		if err := open(ctx, url); err != nil {
			return openedMsg{err: &app.UserError{Msg: "couldn't open " + url + ": " + err.Error(), Hint: "open it in your browser yourself"}}
		}
		return openedMsg{what: what}
	}
}

// openedMsg says the browser was asked to open a page.
type openedMsg struct {
	what string
	err  error
}
