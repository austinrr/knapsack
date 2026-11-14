package app

import (
	"github.com/austinrr/knapsack/internal/ref"
)

// Reporter receives progress events while a use case runs, so frontends can
// show progress without the app printing anything. Implementations must be
// safe for concurrent use: resolving and downloading run in parallel.
type Reporter interface {
	Event(Event)
}

// EventKind says what happened to an addon.
type EventKind int

// The events a use case reports, in the order an addon goes through them.
const (
	EventResolving   EventKind = iota // looking up the newest release
	EventDownloading                  // downloading it
	EventInstalled                    // installed
	EventRemoved                      // uninstalled
	EventSkipped                      // nothing to do; Message says why
	EventWarned                       // installed with a warning in Message
	EventFailed                       // failed; Err says why
)

// Event is one step for one addon.
type Event struct {
	Kind    EventKind
	Profile string
	Name    string // what the user typed, for Remove, Update and Check; Ref is zero if it matched nothing
	Ref     ref.Ref
	Version string // the release, once resolved
	Message string // for Skipped and Warned
	Err     error  // for Failed
}

// NopReporter ignores every event.
type NopReporter struct{}

// Event implements Reporter.
func (NopReporter) Event(Event) {}

// Action is what a use case did to one addon.
type Action string

// The actions in a Result.
const (
	ActionInstalled Action = "installed"
	ActionUpgraded  Action = "upgraded"   // a different release replaced the installed one
	ActionAdopted   Action = "adopted"    // installed already; now in config too
	ActionUnchanged Action = "unchanged"  // Add: already in config
	ActionUpToDate  Action = "up to date" // Update: the newest release is installed
	// ActionNotInConfig is Update without --prune: installed, but not in
	// config, so left alone.
	ActionNotInConfig Action = "not in config"
	ActionRemoved     Action = "removed"
	ActionFailed      Action = "failed"
)

// Result is the outcome for one addon. A failure for one addon doesn't stop
// the others: it's recorded here, and the method's own error is kept for
// problems that stop everything.
type Result struct {
	Profile string
	// Name is what the user typed, for Remove and for Update with named
	// addons; Ref is what it named, and is zero when the name matched no
	// addon.
	Name     string
	Ref      ref.Ref
	Action   Action
	Version  string
	Folders  []string
	Warnings []string
	Err      error // set when Action is ActionFailed
}
