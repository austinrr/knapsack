package tui

import (
	"context"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// doneMsg is what an Update or Remove did, addon by addon; refs are the
// addons it was given, whose rows showed its stages.
type doneMsg struct {
	kind    jobKind
	profile string
	refs    []ref.Ref
	results []app.Result
	err     error
}

// targets are the rows an action acts on: the marked ones, or the
// cursor's when none are (DESIGN.md §9.2).
// Marks on rows the filter hides count too: they're still marked, and
// the remove confirm lists every one.
func (m *model) targets() []row {
	var marked []row
	for _, r := range m.allRows() {
		if m.marks[r.Ref] {
			marked = append(marked, r)
		}
	}
	if len(marked) > 0 {
		return marked
	}
	rows := m.visible()
	if len(rows) == 0 {
		return nil
	}
	return []row{rows[m.cursor]}
}

// notBusy leaves out rows already in a job, so pressing a key twice
// doesn't queue the same work twice. With none left, it says so.
func (m *model) notBusy(rows []row) []row {
	var out []row
	for _, r := range rows {
		if _, busy := m.stages[r.Ref]; !busy {
			out = append(out, r)
		}
	}
	if len(out) == 0 && len(rows) > 0 {
		m.flash = "Already in progress."
	}
	return out
}

// toggleMark marks or unmarks the cursor's row, then moves down, so space
// marks a run of rows.
func (m *model) toggleMark() {
	rows := m.visible()
	if len(rows) == 0 {
		return
	}
	r := rows[m.cursor].Ref
	if m.marks[r] {
		delete(m.marks, r)
	} else {
		m.marks[r] = true
	}
	m.cursor++
	m.scroll()
}

// update updates the targets: at once when each simply has a newer
// release, else through the plan, which says what will happen to the
// rest. An addon known to be up to date is left alone, without asking
// the network again.
func (m *model) update(rows []row) tea.Cmd {
	if rows = m.notBusy(rows); len(rows) == 0 {
		return nil
	}
	simple, current := true, 0
	for _, r := range rows {
		switch r.Status {
		case app.StatusUpdateAvailable:
		case app.StatusUpToDate:
			current++
		case app.StatusInstalled, app.StatusUnknown, app.StatusMissing, app.StatusNotInConfig,
			app.StatusUnmanaged, app.StatusChecking:
			simple = false
		}
	}
	if current == len(rows) {
		if len(rows) == 1 {
			m.flash = rows[0].name + " is up to date; c checks again."
		} else {
			m.flash = "They're up to date; c checks again."
		}
		return nil
	}
	if simple && current == 0 {
		names := make([]ref.Ref, len(rows))
		for i, r := range rows {
			names[i] = r.Ref
		}
		return m.runUpdate(names, app.UpdateOptions{})
	}
	return m.openPlan(rows)
}

// updateAll opens the plan for every addon in the profile, not only those
// the filter shows.
func (m *model) updateAll() tea.Cmd {
	return m.openPlan(m.notBusy(m.allRows()))
}

// runUpdate queues Update for the named addons.
func (m *model) runUpdate(refs []ref.Ref, opts app.UpdateOptions) tea.Cmd {
	a, profile := m.a, m.profile
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.String()
		m.stages[r] = stage{what: "queued"}
	}
	m.marks = map[ref.Ref]bool{}
	m.flash, m.err = "", nil
	return m.enqueue(job{kind: jobUpdate, profile: profile, name: "updating", writes: true,
		run: func(ctx context.Context) tea.Msg {
			res, err := a.Update(ctx, app.Selector{Profiles: []string{profile}, Addons: names}, opts)
			return doneMsg{kind: jobUpdate, profile: profile, refs: refs, results: res, err: err}
		}})
}

// askRemove asks before removing the targets, naming the folders that
// will be deleted.
func (m *model) askRemove(rows []row) tea.Cmd {
	if rows = m.notBusy(rows); len(rows) == 0 {
		return nil
	}
	refs := make([]ref.Ref, len(rows))
	names := make([]string, len(rows))
	var lines []string
	for i, r := range rows {
		refs[i], names[i] = r.Ref, r.name
		folders := "no folders: it's not installed, so it only leaves config"
		if len(r.Folders) > 0 {
			folders = "deletes " + strings.Join(r.Folders, ", ")
		}
		lines = append(lines, boldStyle.Render(r.name)+"  "+faintStyle.Render(folders))
	}
	what := names[0]
	if len(rows) > 1 {
		what = count(len(rows), "addon")
	}
	m.bodyOffset = 0
	m.confirm = &confirmModal{
		title:    "remove",
		question: fmt.Sprintf("Remove %s from %s?", what, m.profile),
		lines:    lines,
		yes:      "remove",
		onYes:    func() tea.Cmd { return m.runRemove(refs) },
	}
	return nil
}

// runRemove queues Remove for the addons.
func (m *model) runRemove(refs []ref.Ref) tea.Cmd {
	a, profile := m.a, m.profile
	names := make([]string, len(refs))
	for i, r := range refs {
		names[i] = r.String()
		m.stages[r] = stage{what: "removing"}
	}
	m.marks = map[ref.Ref]bool{}
	m.flash, m.err = "", nil
	return m.enqueue(job{kind: jobRemove, profile: profile, name: "removing", writes: true,
		run: func(ctx context.Context) tea.Msg {
			res, err := a.Remove(ctx, profile, names)
			return doneMsg{kind: jobRemove, profile: profile, refs: refs, results: res, err: err}
		}})
}

// event shows an update's or remove's progress in its addon's row. A
// check reports the same steps, but shows its own progress (checking),
// so its events are dropped.
func (m *model) event(e app.Event) {
	if e.Profile != m.profile || e.Ref == (ref.Ref{}) || m.running == nil ||
		m.running.kind != jobUpdate && m.running.kind != jobRemove {
		return
	}
	switch e.Kind {
	case app.EventResolving:
		m.stages[e.Ref] = stage{what: "resolving"}
		delete(m.warnings, e.Ref) // a new install's warnings replace the last's
	case app.EventDownloading:
		m.stages[e.Ref] = stage{what: "downloading"}
	case app.EventProgress:
		m.stages[e.Ref] = stage{what: "downloading", done: e.Done, total: e.Total}
	case app.EventWarned:
		m.warnings[e.Ref] = append(m.warnings[e.Ref], e.Message)
	case app.EventInstalled, app.EventRemoved, app.EventSkipped, app.EventFailed:
		delete(m.stages, e.Ref)
	case app.EventChecked:
	}
}

// applyResults records what a job did to each addon, says so in the
// status line, and reloads the profile, whose versions and folders it
// changed, and the details describe shows, if they're of an addon it
// changed. Only the job's own rows stop showing stages: a job still
// queued keeps its "queued".
func (m *model) applyResults(msg doneMsg) tea.Cmd {
	for _, r := range msg.refs {
		delete(m.stages, r)
	}
	if msg.profile != m.profile {
		return nil
	}
	profile, results, err := msg.profile, msg.results, msg.err
	verb := "update"
	if msg.kind == jobRemove {
		verb = "remove"
	}
	k := m.knownFor(profile)
	counts := map[app.Action]int{}
	for _, res := range results {
		counts[res.Action]++
		switch res.Action {
		case app.ActionInstalled, app.ActionUpgraded, app.ActionUpToDate, app.ActionAdopted:
			k[res.Ref] = known{status: app.StatusUpToDate, available: res.Version}
		case app.ActionRemoved:
			delete(k, res.Ref)
			delete(m.warnings, res.Ref)
		case app.ActionFailed:
			kn := k[res.Ref]
			if kn.status == "" {
				kn.status = app.StatusUnknown
			}
			kn.err = res.Err
			k[res.Ref] = kn
		case app.ActionUnchanged, app.ActionNotInConfig:
		}
	}
	// A job that failed as a whole, such as on a lock another process
	// holds, says why; "nothing to update" would hide it.
	m.err = err
	if err == nil || len(results) > 0 {
		m.flash = resultSummary(profile, counts, verb)
	}
	cmd := m.enqueue(m.loadJob())
	if m.describing {
		for _, res := range results {
			if res.Ref == m.describeRef && res.Action != app.ActionRemoved {
				m.details, m.infoErr = nil, nil
				return tea.Batch(cmd, m.enqueue(m.infoJob(m.describeRef)))
			}
		}
	}
	return cmd
}

// resultSummary says what a job did, as the CLI's summary line does:
// "forever: 1 upgraded, 1 failed".
func resultSummary(profile string, counts map[app.Action]int, verb string) string {
	var parts []string
	for _, a := range []app.Action{app.ActionUpgraded, app.ActionInstalled, app.ActionAdopted,
		app.ActionRemoved, app.ActionUpToDate, app.ActionNotInConfig, app.ActionFailed} {
		if n := counts[a]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, a))
		}
	}
	if len(parts) == 0 {
		return profile + ": nothing to " + verb
	}
	return profile + ": " + strings.Join(parts, ", ")
}

// confirmModal is a yes-or-no question, asked in the box (DESIGN.md
// §9.5).
type confirmModal struct {
	title, question string
	// lines are paragraphs, wrapped to the window when drawn, so a list
	// of folders to be deleted is never cut short.
	lines []string
	yes   string // what y does: "remove"
	onYes func() tea.Cmd
}

func (m *model) confirmKey(s string) tea.Cmd {
	switch s {
	case "y", "Y", "enter":
		c := m.confirm
		m.confirm = nil
		return c.onYes()
	case "n", "N", "esc":
		m.confirm = nil
	case "ctrl+c":
		return m.globalKey(s)
	}
	return nil
}

// planModal is what Update will do, built from what's known, with no
// network: Update resolves everything again, so it never acts on stale
// answers (DESIGN.md §9.5).
type planModal struct {
	items  []planItem
	force  bool
	cursor int // over the boxes: each prune candidate, then Force
}

type planItem struct {
	ref      ref.Ref
	name     string
	action   string // "upgrade", "install", "check", "prune"
	from, to string
	prune    bool // for a prune candidate: uninstall it
}

// openPlan opens the plan for rows, or says there's nothing to do.
func (m *model) openPlan(rows []row) tea.Cmd {
	var items []planItem
	for _, r := range rows {
		it := planItem{ref: r.Ref, name: r.name, from: dash(r.Version), to: dash(r.Available)}
		switch r.Status {
		case app.StatusUpdateAvailable:
			it.action = "upgrade"
		case app.StatusMissing:
			it.action = "install"
		case app.StatusNotInConfig:
			it.action = "prune"
		case app.StatusUpToDate:
			continue
		case app.StatusInstalled, app.StatusUnknown, app.StatusChecking, app.StatusUnmanaged:
			it.action = "check"
		}
		items = append(items, it)
	}
	if len(items) == 0 {
		m.flash = "Everything in " + m.profile + " is up to date."
		return nil
	}
	m.plan = &planModal{items: items}
	m.bodyOffset = 0
	return nil
}

// boxes are the indexes in items of the prune candidates, whose boxes
// the cursor moves over before Force's.
func (p *planModal) boxes() []int {
	var out []int
	for i, it := range p.items {
		if it.action == "prune" {
			out = append(out, i)
		}
	}
	return out
}

func (m *model) planKey(s string) tea.Cmd {
	p := m.plan
	boxes := p.boxes()
	switch s {
	case "j", "down", "tab":
		p.cursor = min(p.cursor+1, len(boxes))
		m.followPlanCursor()
	case "k", "up", "shift+tab":
		p.cursor = max(p.cursor-1, 0)
		m.followPlanCursor()
	case "g", "G", "home", "end", "ctrl+f", "ctrl+b", "pgdown", "pgup":
		m.scrollBody(s)
	case "space":
		if p.cursor < len(boxes) {
			p.items[boxes[p.cursor]].prune = !p.items[boxes[p.cursor]].prune
		} else {
			p.force = !p.force
		}
	case "enter":
		m.plan = nil
		var refs []ref.Ref
		opts := app.UpdateOptions{Force: p.force}
		for _, it := range p.items {
			switch {
			case it.action != "prune":
				refs = append(refs, it.ref)
			case it.prune:
				refs = append(refs, it.ref)
				opts.Prune = true
			}
		}
		if len(refs) == 0 {
			m.flash = "Nothing to update: no prune boxes were checked."
			return nil
		}
		return m.runUpdate(refs, opts)
	case "esc":
		m.plan = nil
	case "ctrl+c":
		return m.globalKey(s)
	}
	return nil
}

// planLines draws the plan, grouped by what will happen, and says which
// line has the cursor, so a long plan can scroll to it.
func (m *model) planLines(width int) (lines []string, cursorLine int) {
	p := m.plan
	boxes := p.boxes()
	box := func(on, cursor bool, label string) string {
		mark := "[ ]"
		if on {
			mark = "[x]"
		}
		s := mark + " " + label
		if cursor {
			return cursorStyle.Render(s)
		}
		return s
	}
	nameWidth := 0
	for _, it := range p.items {
		nameWidth = max(nameWidth, len([]rune(it.name)))
	}
	nameWidth = min(nameWidth, max(10, width/2))
	group := func(action, heading string) {
		first := true
		for i, it := range p.items {
			if it.action != action {
				continue
			}
			if first {
				if len(lines) > 0 {
					lines = append(lines, "")
				}
				lines = append(lines, boldStyle.Render(heading))
				first = false
			}
			name := pad(it.name, nameWidth)
			switch action {
			case "upgrade", "install":
				to := it.to
				if to == "—" {
					to = "newest release" // not checked yet
				}
				lines = append(lines, "  "+name+"  "+it.from+" → "+to)
			case "check":
				lines = append(lines, "  "+name+"  "+faintStyle.Render(it.from))
			case "prune":
				at := 0
				for j, b := range boxes {
					if b == i {
						at = j
					}
				}
				if p.cursor == at {
					cursorLine = len(lines)
				}
				lines = append(lines, "  "+box(it.prune, p.cursor == at, name+"  "+faintStyle.Render(it.from)))
			}
		}
	}
	group("upgrade", "Upgrade")
	group("install", "Install")
	group("check", "Checked when updating")
	group("prune", "Not in config: left installed, unless checked to uninstall")
	lines = append(lines, "")
	if p.cursor == len(boxes) {
		cursorLine = len(lines)
	}
	lines = append(lines, box(p.force, p.cursor == len(boxes), "Force: replace folders no addon owns"))
	return wrapLines(width, lines), cursorLine
}

// followPlanCursor scrolls the plan so its cursor is in view, above the
// "more" line.
func (m *model) followPlanCursor() {
	_, at := m.planLines(m.width - 4)
	h := m.bodyHeight()
	if at < m.bodyOffset {
		m.bodyOffset = at
	}
	if at >= m.bodyOffset+h-1 {
		m.bodyOffset = at - h + 2
	}
	m.bodyOffset = max(0, min(m.bodyOffset, m.maxBodyOffset()))
}

// wrapLines cuts each line to width, in a copy, so a narrow window
// never shortens what a modal keeps.
func wrapLines(width int, lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Truncate(l, width, "…")
	}
	return out
}
