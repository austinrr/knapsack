package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/austinrr/knapsack/internal/app"
)

// row is one addon as shown: its listing, with what's known laid over
// it, and its name, unique in the profile.
type row struct {
	app.Installed
	name string
}

// visible is the rows shown: the profile's rows with what's known laid
// over them, narrowed by the filter.
func (m *model) visible() []row { return m.rowsMatching(m.query) }

// allRows is every row, whatever the filter shows.
func (m *model) allRows() []row { return m.rowsMatching("") }

func (m *model) rowsMatching(query string) []row {
	k := m.known[m.profile]
	names := app.UniqueNamesOf(m.rows)
	q := strings.ToLower(query)
	var out []row
	for i, in := range m.rows {
		if kn, ok := k[in.Ref]; ok {
			in.Status, in.Available, in.Err = kn.status, kn.available, kn.err
		}
		if m.checking[in.Ref] {
			in.Status = app.StatusChecking
		}
		// Matching the ref matches its source too.
		if q != "" && !strings.Contains(strings.ToLower(names[i]), q) &&
			!strings.Contains(strings.ToLower(in.Ref.String()), q) {
			continue
		}
		out = append(out, row{Installed: in, name: names[i]})
	}
	return out
}

// summary is the status line's counts: how many addons, and once
// checked, how many have updates or failed.
func (m *model) summary(rows []row) string {
	var checking, updates, failed int
	for _, r := range rows {
		switch r.Status {
		case app.StatusChecking:
			checking++
		case app.StatusUpdateAvailable:
			updates++
		case app.StatusUnknown:
			failed++
		case app.StatusInstalled, app.StatusUpToDate, app.StatusMissing, app.StatusNotInConfig, app.StatusUnmanaged:
		}
	}
	if checking > 0 {
		return fmt.Sprintf("%s checking, %d of %d done", m.spinner(), len(rows)-checking, len(rows))
	}
	parts := []string{app.Count(len(rows), "addon")}
	if updates > 0 {
		parts = append(parts, app.Count(updates, "update"))
	}
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d failed", failed))
	}
	return strings.Join(parts, " · ")
}

func (m *model) spinner() string { return m.spin[m.frame%len(m.spin)] }

// glyph marks a status, so none depends on color alone.
func glyph(s app.Status) string {
	switch s {
	case app.StatusUpToDate:
		return "✓"
	case app.StatusUpdateAvailable:
		return "↑"
	case app.StatusMissing:
		return "○"
	case app.StatusNotInConfig:
		return "!"
	case app.StatusUnknown:
		return "✗"
	case app.StatusInstalled, app.StatusUnmanaged, app.StatusChecking:
	}
	return " "
}

// ago is how long before now t was, roughly: "5m ago", "2d ago".
func ago(t, now time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
	return fmt.Sprintf("%dd ago", int(d.Hours()/24))
}

// dash is s, or "—" when it's empty.
func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
