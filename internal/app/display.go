package app

import (
	"fmt"
	"strings"

	"github.com/austinrr/knapsack/internal/ref"
)

// The helpers here are how the frontends show what the use cases return,
// so the CLI and the TUI say it the same way.

// NameRow is one row's profile, name and ref, for UniqueNames.
type NameRow struct {
	Profile, Name string
	Ref           ref.Ref
}

// UniqueNames returns each row's name, with its ref's ID added when
// another row in the same profile shows the same name, ignoring case,
// since titles and repo names needn't be unique: "Details! (a/details)"
// and "Details! (b/details)" (DESIGN.md §8.4). A row with no ref, an
// unmanaged folder, keeps its name.
func UniqueNames(rows []NameRow) []string {
	seen := map[[2]string]int{}
	for _, r := range rows {
		seen[[2]string{r.Profile, strings.ToLower(r.Name)}]++
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Name
		if seen[[2]string{r.Profile, strings.ToLower(r.Name)}] > 1 && r.Ref != (ref.Ref{}) {
			out[i] = fmt.Sprintf("%s (%s)", r.Name, r.Ref.ID)
		}
	}
	return out
}

// UniqueNamesOf is UniqueNames for rows that say their own NameRow, as
// Installed and Plan do.
func UniqueNamesOf[T interface{ NameRow() NameRow }](rows []T) []string {
	named := make([]NameRow, len(rows))
	for i, r := range rows {
		named[i] = r.NameRow()
	}
	return UniqueNames(named)
}

// NameRow is the addon's profile, the name it's shown by, and its ref.
func (in Installed) NameRow() NameRow {
	return NameRow{Profile: in.Profile, Name: in.Name(), Ref: in.Ref}
}

// NameRow is the planned addon's profile, the name it's shown by, and its
// ref.
func (p Plan) NameRow() NameRow {
	return NameRow{Profile: p.Profile, Name: p.DisplayName(), Ref: p.Ref}
}

// Count is n things, as "1 addon" or "2 addons".
func Count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

// FormatProgress shows bytes downloaded of those expected, "1.2 MiB of
// 4.5 MiB", or only what's downloaded when the total is unknown
// (negative), as Event's Done and Total have them.
func FormatProgress(done, total int64) string {
	if total < 0 {
		return FormatBytes(done)
	}
	return FormatBytes(done) + " of " + FormatBytes(total)
}

// FormatBytes shows a size in KiB or MiB with one decimal, as progress
// needs no more precision.
func FormatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

// Status is how ready the source is, as sources list shows it: "ready",
// or "needs key" for one read through its official API without a key.
func (s SourceInfo) Status() string {
	if !s.Ready() {
		return "needs key"
	}
	return "ready"
}

// KeyFrom is where the source's key comes from: "config", the environment
// variable that overrides it, or "" for none. For GitHub, whose token
// Knapsack never stores, it's github: where the frontend found it
// ("GITHUB_TOKEN", "GH_TOKEN", "gh" or "").
func KeyFrom(s SourceInfo, github string) string {
	switch {
	case !s.NeedsKey:
		return github
	case s.Key == KeyEnv:
		return s.Env
	case s.Key == KeyConfig:
		return "config"
	}
	return ""
}

// DescribeKey says where the source's key comes from, from KeyFrom, as
// sources list shows it: "set in config", "from $KNAPSACK_WAGO_API_KEY",
// "from gh auth token". It's "" for a source with no key, which each
// frontend shows its own way.
func DescribeKey(s SourceInfo, from string) string {
	switch {
	case from == "config":
		return "set in config"
	case from == "gh":
		return "from gh auth token"
	case from != "":
		return "from $" + from
	case !s.NeedsKey:
		return "none (60 requests an hour)"
	}
	return ""
}
