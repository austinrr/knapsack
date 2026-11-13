package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/austinrr/knapsack/internal/app"
)

// Exit codes, following the diff/grep convention (DESIGN.md §8.6).
const (
	exitOK    = 0
	exitError = 2
)

// env is everything the CLI takes from its surroundings. Tests build their
// own, so they never depend on the real process.
type env struct {
	stdout  io.Writer
	stderr  io.Writer
	getenv  func(string) string
	version string
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// The first signal cancels ctx so in-flight work can roll back. After
	// that, restore default handling so a second Ctrl-C force-quits even if
	// something ignores ctx.
	go func() {
		<-ctx.Done()
		stop()
	}()

	code := run(ctx, os.Args[1:], env{
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		getenv:  os.Getenv,
		version: resolveVersion(buildVersion, debug.ReadBuildInfo),
	})
	stop()
	os.Exit(code)
}

// run executes the CLI with args and returns the process exit code. It is
// the single place where errors are rendered, so tests can drive the whole
// program without spawning a process.
func run(ctx context.Context, args []string, e env) int {
	c := &cli{env: e}
	root := newRootCmd(c)
	// Cobra treats nil args as "use os.Args", so always pass a non-nil
	// slice: run must only ever see the arguments it was given.
	root.SetArgs(append([]string{}, args...))
	root.SetOut(e.stdout)
	root.SetErr(e.stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		renderError(e.stderr, err, c.verbose)
		return exitError
	}
	return exitOK
}

// renderError writes err to w: an "error:" line, then for an app.UserError
// a "hint:" line, and with --verbose the underlying cause (DESIGN.md §8.6).
func renderError(w io.Writer, err error, verbose bool) {
	msg := "error: " + err.Error() + "\n"
	var ue *app.UserError
	if errors.As(err, &ue) {
		msg = "error: " + ue.Msg + "\n"
		if ue.Hint != "" {
			msg += "hint: " + ue.Hint + "\n"
		}
		if verbose && ue.Err != nil {
			msg += "cause: " + ue.Err.Error() + "\n"
		}
	}
	// A failed write to stderr leaves nowhere else to report, so the error
	// is deliberately ignored.
	_, _ = io.WriteString(w, msg)
}
