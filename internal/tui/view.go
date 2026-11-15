package tui

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/austinrr/knapsack/internal/app"
)

// The smallest window the TUI draws in; below it, it says so instead of
// drawing a broken table (DESIGN.md §9.1).
const (
	minWidth  = 60
	minHeight = 15
)

// Styles use the terminal's own 16 colors, which follow its light or
// dark theme, and Bubble Tea drops them under NO_COLOR.
var (
	boldStyle    = lipgloss.NewStyle().Bold(true)
	faintStyle   = lipgloss.NewStyle().Faint(true)
	accentStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Cyan)
	errorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Red)
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Yellow)
	cursorStyle  = lipgloss.NewStyle().Reverse(true)
	statusStyles = map[app.Status]lipgloss.Style{
		app.StatusUpToDate:        lipgloss.NewStyle().Foreground(lipgloss.Green),
		app.StatusUpdateAvailable: lipgloss.NewStyle().Foreground(lipgloss.Yellow),
		app.StatusMissing:         lipgloss.NewStyle().Foreground(lipgloss.Cyan),
		app.StatusNotInConfig:     lipgloss.NewStyle().Foreground(lipgloss.Magenta),
		app.StatusUnknown:         errorStyle,
		app.StatusChecking:        faintStyle,
	}
)

func (m *model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "knapsack"
	return v
}

// render draws the whole screen: header, box, footer.
func (m *model) render() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minWidth || m.height < minHeight {
		lines := wrap(m.width, fmt.Sprintf("Make the window larger: knapsack needs %d×%d, and this is %d×%d.",
			minWidth, minHeight, m.width, m.height))
		for i, l := range lines {
			lines[i] = ansi.Truncate(l, m.width, "")
		}
		return strings.Join(lines[:min(len(lines), m.height)], "\n")
	}
	lines := m.header()
	lines = append(lines, m.box(m.height-len(lines)-1)...)
	lines = append(lines, m.footer())
	return strings.Join(lines, "\n")
}

// header is the profile and its path, labeled, with the views and help
// on the right.
func (m *model) header() []string {
	profile := "—"
	if m.profile != "" {
		profile = boldStyle.Render(m.profile)
	}
	var tabs []string
	for i, name := range viewNames {
		if i == m.view {
			tabs = append(tabs, accentStyle.Render(fmt.Sprintf("[%d] %s", i+1, name)))
		} else {
			tabs = append(tabs, faintStyle.Render(fmt.Sprintf("%d %s", i+1, name)))
		}
	}
	views := strings.Join(tabs, "  ")
	help := faintStyle.Render("? help")
	left := "Path:    "
	path := app.ShortenHome(m.path, m.opts.Home)
	room := m.width - 2 - ansi.StringWidth(left) - ansi.StringWidth(help) - 2
	return []string{
		spread(" Profile: "+profile, views+" ", m.width),
		spread(" "+left+cutMiddle(dash(path), room), help+" ", m.width),
	}
}

// cutMiddle shortens a path to fit width by cutting its middle, keeping
// the client folder at the end: C:\Program Files…\_retail_\Interface\AddOns.
func cutMiddle(path string, width int) string {
	if ansi.StringWidth(path) <= width {
		return path
	}
	// Keep the last three elements: the client folder, Interface and
	// AddOns. A profile's path is in either form on any OS (§5.2).
	sep := "/"
	if strings.Contains(path, `\`) {
		sep = `\`
	}
	tail := ""
	if parts := strings.Split(path, sep); len(parts) > 3 {
		tail = sep + strings.Join(parts[len(parts)-3:], sep)
	}
	head := width - ansi.StringWidth(tail) - 1
	if head < 1 {
		return ansi.TruncateLeft(path, ansi.StringWidth(path)-width+1, "…")
	}
	return ansi.Truncate(path, head, "") + "…" + tail
}

// box is the view, framed, with its title in the top border, height
// lines in all.
func (m *model) box(height int) []string {
	title, body := m.content(m.width-4, height-2)
	if m.scrollable() {
		body = m.bodyWindow(body, height-2)
	}
	inner := m.width - 2
	top := "─ " + ansi.Truncate(title, inner-4, "…") + " ─"
	fill := max(0, inner-ansi.StringWidth(top))
	lines := []string{"┌" + strings.Repeat("─", fill/2) + top + strings.Repeat("─", fill-fill/2) + "┐"}
	for i := range height - 2 {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		lines = append(lines, "│ "+pad(line, inner-2)+" │")
	}
	return append(lines, "└"+strings.Repeat("─", inner)+"┘")
}

// scrollable is whether the box shows describe or help, whose lines
// scroll when they don't fit (bodyOffset); the table scrolls by its
// cursor instead.
func (m *model) scrollable() bool {
	return m.help || m.plan != nil || m.confirm != nil ||
		m.describing && m.loaded && !m.noProfiles && m.loadErr == nil
}

// bodyHeight is how many lines the box holds inside its borders.
func (m *model) bodyHeight() int { return max(1, m.height-5) }

// maxBodyOffset is how far describe or help can scroll: until its last
// line is in view.
func (m *model) maxBodyOffset() int {
	_, body := m.content(m.width-4, m.bodyHeight())
	return max(0, len(body)-m.bodyHeight())
}

// bodyWindow is the part of body that fits in height lines, from
// bodyOffset; when more is below, the last line says so.
func (m *model) bodyWindow(body []string, height int) []string {
	off := max(0, min(m.bodyOffset, len(body)-height))
	body = body[off:]
	if len(body) > height {
		more := len(body) - height + 1
		body = append(body[:height-1:height-1], faintStyle.Render(fmt.Sprintf("↓ %d more · j k to scroll", more)))
	}
	return body
}

// content is the box's title and lines, at most width wide. Describe and
// help may be taller than height; bodyWindow scrolls them.
func (m *model) content(width, height int) (string, []string) {
	switch {
	case m.help:
		return accentStyle.Render("help"), m.helpLines(width)
	// An open dialog comes first: it takes the keys, so it's what's
	// shown, even if a reload failed meanwhile.
	case m.add != nil:
		return accentStyle.Render("add") + "(" + m.profile + ")", m.addLines(width, height)
	case m.plan != nil:
		lines, _ := m.planLines(width)
		return accentStyle.Render("update") + "(" + m.profile + ")", lines
	case m.confirm != nil:
		return m.confirmContent(width)
	case m.form != nil:
		title := "new profile"
		if m.form.existing != nil {
			title = "profile(" + m.form.existing.Name + ")"
		}
		return accentStyle.Render(title), m.formLines(width)
	case m.view == viewProfiles:
		return m.profilesTable(width)
	case !m.loaded:
		return accentStyle.Render("addons"), []string{faintStyle.Render(m.spinner() + " loading")}
	case m.noProfiles:
		return accentStyle.Render("addons"), wrap(width,
			"No profiles yet.",
			"",
			"Make one for each game client with "+boldStyle.Render("knapsack profiles create --auto")+
				", or by path with "+boldStyle.Render("knapsack profiles create --path <AddOns>")+
				", then open knapsack tui again.")
	case m.loadErr != nil:
		return accentStyle.Render("addons"), errorLines(width, m.loadErr, m.opts.Running)
	case m.describing:
		return m.describe(width)
	}
	return m.table(width, height)
}

// confirmContent is the confirm modal: its question, then its
// paragraphs, wrapped.
func (m *model) confirmContent(width int) (string, []string) {
	lines := wrap(width, warnStyle.Render(m.confirm.question), "")
	if m.confirm.note != "" {
		for _, l := range wrap(width-2, faintStyle.Render(m.confirm.note)) {
			lines = append(lines, "  "+l)
		}
	}
	for _, p := range m.confirm.lines {
		for i, l := range wrap(width-4, p) {
			indent := "  "
			if i > 0 {
				indent = "    " // a paragraph's own lines, under its name
			}
			lines = append(lines, indent+l)
		}
	}
	return accentStyle.Render(m.confirm.title) + "(" + m.profile + ")", lines
}

// table is the addons view.
func (m *model) table(width, height int) (string, []string) {
	all := m.visible()
	n := strconv.Itoa(len(m.rows))
	if m.query != "" {
		n = strconv.Itoa(len(all)) + "/" + n
	}
	title := accentStyle.Render("addons") + "(" + m.profile + ")[" + n + "]"
	if m.query != "" && !m.filtering {
		title += " " + faintStyle.Render("/"+m.query)
	}
	if len(m.rows) == 0 {
		msg := "No addons in " + m.profile + "."
		if m.hidden > 0 {
			verb := "weren't"
			if m.hidden == 1 {
				verb = "wasn't"
			}
			msg += fmt.Sprintf(" %s in AddOns %s installed by Knapsack.", count(m.hidden, "folder"), verb)
		}
		return title, wrap(width, msg, "", "Press "+boldStyle.Render("a")+" to add one, by ref or link.")
	}
	if len(all) == 0 {
		return title, []string{faintStyle.Render("No addons match /" + m.query + ".")}
	}

	headers := []string{"NAME", "VERSION", "AVAILABLE", "STATUS", "SOURCE"}
	cells := make([][]string, len(all))
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for i, r := range all {
		status := glyph(r.Status) + " " + string(r.Status)
		if r.Status == app.StatusChecking {
			status = m.spinner() + " checking"
		}
		cells[i] = []string{r.name, dash(r.Version), dash(r.Available), status, r.Ref.Source}
		if r.Status == app.StatusChecking || r.Status == app.StatusInstalled {
			cells[i][2] = ""
		}
		for j, c := range cells[i] {
			widths[j] = max(widths[j], ansi.StringWidth(c))
		}
		// A row in a job shows its stage instead, spanning both columns
		// without widening either.
		if st, ok := m.stages[r.Ref]; ok {
			cells[i][2], cells[i][3] = "", m.spinner()+" "+st.String()
		}
	}
	// NAME gives way when the rest don't fit, down to minName; past that,
	// the columns on the right are cut instead, since the name matters
	// most.
	const gap, minName = 3, 16
	// Each line starts with a mark column: ● for a marked row.
	width -= 2
	rest := 0
	for _, w := range widths[1:] {
		rest += w + gap
	}
	widths[0] = max(min(widths[0], minName), min(widths[0], width-rest))

	// A row in a job spans AVAILABLE and STATUS, so its stage needn't
	// widen either.
	spanned := widths[2] + gap + widths[3]
	line := func(mark string, cs []string, style func(j int, s string) string) string {
		parts := make([]string, 0, len(cs))
		for j := 0; j < len(cs); j++ {
			w := widths[j]
			if j == 2 && cs[2] == "" && ansi.StringWidth(cs[3]) > widths[3] {
				parts = append(parts, style(3, pad(ansi.Truncate(cs[3], spanned, "…"), spanned)))
				j++
				continue
			}
			parts = append(parts, style(j, pad(ansi.Truncate(cs[j], w, "…"), w)))
		}
		return mark + ansi.Truncate(strings.Join(parts, strings.Repeat(" ", gap)), width, "…")
	}
	lines := []string{line("  ", headers, func(_ int, s string) string { return boldStyle.Render(s) })}
	page := height - 1
	for i := m.offset; i < len(all) && i < m.offset+page; i++ {
		r := all[i]
		mark := "  "
		if m.marks[r.Ref] {
			mark = accentStyle.Render("●") + " "
		}
		if i == m.cursor {
			lines = append(lines, mark+cursorStyle.Render(pad(ansi.Strip(line("", cells[i], plain)), width)))
			continue
		}
		lines = append(lines, line(mark, cells[i], func(j int, s string) string {
			if j == 3 {
				if _, busy := m.stages[r.Ref]; busy {
					return faintStyle.Render(s)
				}
				if st, ok := statusStyles[r.Status]; ok {
					return st.Render(s)
				}
			}
			return s
		}))
	}
	return title, lines
}

func plain(_ int, s string) string { return s }

// pageSize is how many rows the table shows at once.
func (m *model) pageSize() int {
	// Two header lines, the footer, the box's borders and the column
	// headers.
	return max(1, m.height-6)
}

// describe is one addon's details: what the row has at once, and what
// Info reads from its TOC when it returns.
func (m *model) describe(width int) (string, []string) {
	var r *row
	for _, v := range m.visible() {
		if v.Ref == m.describeRef {
			r = &v
			break
		}
	}
	if r == nil {
		return accentStyle.Render("describe"), []string{faintStyle.Render("This addon is no longer in " + m.profile + ".")}
	}
	title := accentStyle.Render("describe") + "(" + m.profile + "/" + r.name + ")"
	var lines []string
	field := func(label string, values ...string) {
		for i, v := range values {
			l := ""
			if i == 0 {
				l = label + ":"
			}
			lines = append(lines, ansi.Truncate(faintStyle.Render(pad(l, 12))+v, width, "…"))
		}
	}
	waiting := faintStyle.Render(m.spinner())
	toc := func(f func(d *app.Details) string) string {
		if m.details == nil {
			return waiting
		}
		return dash(f(m.details))
	}
	field("Title", dash(r.Title))
	field("Ref", r.Ref.String())
	status := string(r.Status)
	if r.Version != "" && r.Available != "" && r.Available != r.Version {
		status += "   " + r.Version + " → " + r.Available
	}
	if st, ok := statusStyles[r.Status]; ok {
		status = st.Render(glyph(r.Status) + " " + status)
	}
	if st, ok := m.stages[r.Ref]; ok {
		status = faintStyle.Render(m.spinner() + " " + st.String())
	}
	field("Status", status)
	for _, w := range m.warnings[r.Ref] {
		lines = append(lines, strings.Repeat(" ", 12)+warnStyle.Render("warning: ")+w)
	}
	// Why the check or Info failed comes first, under the status it
	// explains, so a long list of folders never pushes it out of view.
	for _, err := range []error{r.Err, m.infoErr} {
		if err != nil {
			for _, l := range errorLines(width-12, err, m.opts.Running) {
				lines = append(lines, strings.Repeat(" ", 12)+l)
			}
		}
	}
	field("Author", toc(func(d *app.Details) string { return d.Author }))
	field("Notes", toc(func(d *app.Details) string { return d.Notes }))
	field("Version", dash(r.Version))
	installed := "—"
	if !r.InstalledAt.IsZero() {
		installed = r.InstalledAt.UTC().Format("2006-01-02 15:04 UTC") + " (" + ago(r.InstalledAt, m.now()) + ")"
	}
	field("Installed", installed)
	field("Interface", toc(func(d *app.Details) string {
		s := make([]string, len(d.Interfaces))
		for i, n := range d.Interfaces {
			s[i] = strconv.Itoa(n)
		}
		return strings.Join(s, ", ")
	}))
	if len(r.Folders) == 0 {
		field("Folders", "—")
	} else {
		field("Folders", r.Folders...)
	}
	field("Source", r.Ref.URL())
	return title, lines
}

// errorLines is err as the CLI renders it: the error, its hint and its
// report link, wrapped to width.
func errorLines(width int, err error, running string) []string {
	paras := []string{errorStyle.Render("error: ") + err.Error()}
	var ue *app.UserError
	if errors.As(err, &ue) && ue.Hint != "" {
		paras = append(paras, faintStyle.Render("hint: ")+ue.Hint)
	}
	if u := app.ReportURL(err, running); u != "" {
		paras = append(paras, faintStyle.Render("report: ")+u)
	}
	return wrap(width, paras...)
}

// helpLines is every key, for the view open under help.
func (m *model) helpLines(width int) []string {
	type key = struct{ keys, what string }
	section := func(name string, keys ...key) []string {
		lines := []string{boldStyle.Render(name)}
		for _, k := range keys {
			lines = append(lines, ansi.Truncate("  "+accentStyle.Render(pad(k.keys, 16))+k.what, width, "…"))
		}
		return append(lines, "")
	}
	var lines []string
	if m.view == viewProfiles {
		lines = section("Profiles",
			key{"enter", "use the profile: make it current, and show its addons"},
			key{"n", "new profile, by path"},
			key{"s", "set its path or flavor"},
			key{"A", "find World of Warcraft; make a profile per client"},
			key{"e", "edit it in your editor"},
			key{"ctrl+d", "delete it; its addon files stay"},
		)
		return append(lines, m.helpEverywhere(section)...)
	}
	lines = section("Addons",
		key{"enter", "describe the addon"},
		key{"a", "add addons, by ref or link"},
		key{"u", "update the marked addons, or this one"},
		key{"U", "update all: shows the plan first"},
		key{"ctrl+d", "remove the marked addons, or this one"},
		key{"space", "mark or unmark the addon"},
		key{"ctrl+\\", "clear marks"},
		key{"o", "open the addon's page in the browser"},
		key{"e", "edit the profile in your editor"},
		key{"c", "check every addon again"},
		key{"/", "filter by name, ref or source"},
		key{"esc", "clear the filter"},
	)
	lines = append(lines, section("Describe",
		key{"u", "update this addon"},
		key{"ctrl+d", "remove it"},
		key{"o", "open its page"},
		key{"r", "open the error's report link, when it has one"},
		key{"esc", "back to addons"},
		key{"j k", "scroll, when it doesn't fit"},
	)...)
	return append(lines, m.helpEverywhere(section)...)
}

// helpEverywhere is the keys every view has.
func (m *model) helpEverywhere(section func(string, ...struct{ keys, what string }) []string) []string {
	type key = struct{ keys, what string }
	return section("Everywhere",
		key{"1 2  tab", "addons, profiles view"},
		key{"j k  ↓ ↑", "move"},
		key{"g G", "top, bottom"},
		key{"ctrl+f ctrl+b", "page down, up"},
		key{"?", "this help; esc closes it"},
		key{"q  ctrl+c", "quit"},
	)
}

// footer is the view's main keys on the left and the status on the
// right, or the filter while it's open, or the quit question.
func (m *model) footer() string {
	switch {
	case m.confirmQuit:
		return " " + warnStyle.Render("Quit and cancel "+m.running.name+"? ") + boldStyle.Render("y") + "/" + boldStyle.Render("n")
	case m.filtering:
		return " " + m.filter.View()
	}
	var keys []string
	switch {
	case m.help:
		keys = []string{"esc", "close", "j k", "scroll"}
	case m.add != nil:
		keys = []string{"ctrl+s", "add", "tab", "force box", "esc", "cancel"}
	case m.plan != nil:
		keys = []string{"enter", "update", "space", "check box", "j k", "move", "esc", "cancel"}
	case m.confirm != nil && m.confirm.info:
		keys = []string{"enter", "close", "j k", "scroll"}
	case m.confirm != nil:
		keys = []string{"y", m.confirm.yes, "n", "cancel"}
	case m.form != nil:
		keys = []string{"enter", "save", "tab", "next field", "esc", "cancel"}
	case m.view == viewProfiles && len(m.profiles) == 0:
		keys = []string{"A", "find the game", "n", "new", "?", "help", "q", "quit"}
	case m.view == viewProfiles:
		keys = []string{"enter", "use", "n", "new", "s", "set", "A", "find the game", "e", "edit",
			"ctrl+d", "delete", "tab", "addons", "?", "help"}
	case !m.loaded || m.noProfiles:
		keys = []string{"q", "quit"}
	case m.loadErr != nil:
		keys = []string{"c", "try again", "q", "quit"}
	case m.describing:
		keys = []string{"u", "update", "ctrl+d", "remove", "o", "open", "esc", "back", "j k", "scroll"}
	case len(m.rows) == 0:
		keys = []string{"a", "add", "c", "check", "?", "help", "q", "quit"}
	default:
		keys = []string{"u", "update", "U", "update all", "a", "add", "enter", "describe", "ctrl+d", "remove",
			"space", "mark", "o", "open", "c", "check", "/", "filter", "?", "help"}
	}
	// The status comes first; hints that don't fit beside it are left
	// out whole, from the end, since ? lists them all anyway.
	status := m.status() + " "
	room := m.width - ansi.StringWidth(status) - 2
	left := ""
	for i := 0; i < len(keys); i += 2 {
		hint := boldStyle.Render(keys[i]) + " " + keys[i+1]
		next := left + "  " + hint
		if left == "" {
			next = " " + hint
		}
		if ansi.StringWidth(next) > room {
			break
		}
		left = next
	}
	return spread(left, status, m.width)
}

// status is the footer's right side: what's running, the last error, or
// the profile's counts.
func (m *model) status() string {
	switch {
	case m.err != nil:
		s := m.err.Error()
		var ue *app.UserError
		if errors.As(m.err, &ue) && ue.Hint != "" {
			s += " · " + ue.Hint
		}
		return errorStyle.Render("✗ " + s)
	case !m.loaded || m.loadErr != nil || m.noProfiles:
		return ""
	case m.running != nil && m.running.kind != jobCheck && len(m.checking) == 0:
		return faintStyle.Render(m.spinner() + " " + m.running.name)
	case m.flash != "":
		return m.flash
	}
	return faintStyle.Render(m.summary(m.visible()))
}

// spread puts left and right at either end of a width-wide line. When
// they don't both fit, right wins and left is cut.
func spread(left, right string, width int) string {
	if ansi.StringWidth(right) > width-1 {
		return " " + ansi.Truncate(right, width-1, "…")
	}
	rw := ansi.StringWidth(right)
	left = ansi.Truncate(left, width-rw-1, "…")
	return left + strings.Repeat(" ", width-rw-ansi.StringWidth(left)) + right
}

// pad fills s with spaces to width.
func pad(s string, width int) string {
	return s + strings.Repeat(" ", max(0, width-ansi.StringWidth(s)))
}

// wrap word-wraps paragraphs to width, an empty one being a blank line.
func wrap(width int, paras ...string) []string {
	var lines []string
	for _, p := range paras {
		if p == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, strings.Split(lipgloss.Wrap(p, width, ""), "\n")...)
	}
	return lines
}
