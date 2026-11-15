package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/ref"
)

// reporter is the App's Reporter: it passes each event to the program as
// an eventMsg, from whichever goroutine reports it. httpx reports
// progress on every read, so EventProgress is coalesced instead: only the
// latest for each addon is kept, and the model takes them on its spinner
// tick, every 100 ms, as the CLI's bar redraws (DESIGN.md §9.4).
type reporter struct {
	bus      *bus
	mu       sync.Mutex
	progress map[progressKey]app.Event
}

type progressKey struct {
	profile string
	ref     ref.Ref
}

// eventMsg is one event a job reported.
type eventMsg struct{ app.Event }

func newReporter(b *bus) *reporter {
	return &reporter{bus: b, progress: map[progressKey]app.Event{}}
}

// Event implements app.Reporter. It's safe for concurrent use.
func (r *reporter) Event(e app.Event) {
	k := progressKey{e.Profile, e.Ref}
	r.mu.Lock()
	if e.Kind == app.EventProgress {
		r.progress[k] = e
		r.mu.Unlock()
		return
	}
	// Any other event for the addon supersedes its progress.
	delete(r.progress, k)
	r.mu.Unlock()
	r.bus.Send(eventMsg{e})
}

// drain takes the latest progress for each addon reported since the last
// drain.
func (r *reporter) drain() []app.Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.progress) == 0 {
		return nil
	}
	out := make([]app.Event, 0, len(r.progress))
	for k, e := range r.progress {
		out = append(out, e)
		delete(r.progress, k)
	}
	return out
}

// stage is where an addon is in a job, shown across a row's AVAILABLE and
// STATUS columns.
type stage struct {
	what        string // "queued", "resolving", "downloading", "removing"
	done, total int64  // bytes, while downloading; total is -1 when unknown
}

func (s stage) String() string {
	if s.what != "downloading" || s.done == 0 {
		return s.what
	}
	if s.total > 0 && s.done >= s.total {
		return "installing"
	}
	return "downloading " + shortProgress(s.done, s.total)
}

// shortProgress is formatProgress with the unit said once when both
// sizes share it, "1.2 of 4.5 MiB", to fit a row.
func shortProgress(done, total int64) string {
	d, t := formatBytes(done), formatBytes(total)
	if total >= 0 && done >= 1<<10 {
		if du, tu := unit(d), unit(t); du == tu {
			return strings.TrimSuffix(d, " "+du) + " of " + t
		}
	}
	return formatProgress(done, total)
}

// unit is the unit formatBytes gave: "MiB".
func unit(s string) string { return s[strings.LastIndexByte(s, ' ')+1:] }

// formatProgress and formatBytes show bytes downloaded of those expected
// as the CLI's progress bar does: "1.2 MiB of 4.5 MiB", or only what's
// downloaded when the total is unknown (negative).
func formatProgress(done, total int64) string {
	if total < 0 {
		return formatBytes(done)
	}
	return formatBytes(done) + " of " + formatBytes(total)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
