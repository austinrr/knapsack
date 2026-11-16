package tui

import (
	"context"
	"errors"
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
	// conflicts is how many folders Add found in the way, when the dialog
	// opened again on its failures, for Force's label.
	conflicts int
}

func (m *model) openAdd() tea.Cmd {
	t := textarea.New()
	t.Placeholder = "github:owner/repo, or a GitHub, CurseForge or Wago link"
	t.SetHeight(5)
	t.SetWidth(m.addWidth())
	if !m.blink {
		st := t.Styles()
		st.Cursor.Blink = false
		t.SetStyles(st)
	}
	m.add = &addModal{text: t}
	m.bodyOffset = 0
	return m.add.text.Focus()
}

// addWidth is the text area's width: the box's, less its prompt.
func (m *model) addWidth() int { return max(1, m.width-6) }

// addInput is a key or a paste for the dialog's text. A paste while the
// Force box has the focus moves it back to the text, rather than being
// lost.
func (m *model) addInput(msg tea.Msg) tea.Cmd {
	var focus tea.Cmd
	if m.add.onForce {
		if _, paste := msg.(tea.PasteMsg); !paste {
			return nil
		}
		m.add.onForce = false
		focus = m.add.text.Focus()
	}
	var cmd tea.Cmd
	m.add.text, cmd = m.add.text.Update(msg)
	return tea.Batch(focus, cmd)
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
			return doneMsg{kind: jobAdd, profile: profile, refs: refs, force: force, results: res, err: err}
		}})
}

// reopenAdd opens the dialog again on the addons Add couldn't add, each
// with why and its hint, so a folder conflict's fix, Force, is a tab and
// a space away rather than lost with the closed dialog.
func (m *model) reopenAdd(msg doneMsg) tea.Cmd {
	var failed []app.Result
	for _, res := range msg.results {
		if res.Action == app.ActionFailed {
			failed = append(failed, res)
		}
	}
	if len(failed) == 0 || m.add != nil {
		return nil
	}
	cmd := m.openAdd()
	a := m.add
	a.force = msg.force
	lines := make([]string, len(failed))
	for i, res := range failed {
		lines[i] = res.Ref.String()
		if ce, ok := errors.AsType[*app.FolderConflictError](res.Err); ok {
			a.conflicts += len(ce.Folders)
		}
		err := conflictHint(res.Err, "check Force")
		p := err.Error() // which names the addon
		var ue *app.UserError
		if errors.As(err, &ue) && ue.Hint != "" {
			p += " · hint: " + ue.Hint
		}
		a.problems = append(a.problems, p)
	}
	a.text.SetValue(strings.Join(lines, "\n"))
	return cmd
}

// addLines draws the dialog in height lines: the problems are cut to
// what fits, so the Force box below them is always in view.
func (m *model) addLines(width, height int) []string {
	a := m.add
	lines := []string{"Refs or links to add to " + m.profile + ", one per line:", ""}
	lines = append(lines, strings.Split(a.text.View(), "\n")...)
	// Whole problems while they fit, keeping a line for saying how many
	// more there are, above a blank line and the Force box.
	room := height - len(lines) - 2
	for i, p := range a.problems {
		wrapped := wrap(width, errorStyle.Render("✗ ")+p)
		last := i == len(a.problems)-1
		if len(wrapped) > room || !last && len(wrapped) == room {
			lines = append(lines, faintStyle.Render(fmt.Sprintf("… and %d more", len(a.problems)-i)))
			break
		}
		lines = append(lines, wrapped...)
		room -= len(wrapped)
	}
	box := "[ ]"
	if a.force {
		box = "[x]"
	}
	force := box + " Force: remove and replace the " + addonFolders(a.conflicts)
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
