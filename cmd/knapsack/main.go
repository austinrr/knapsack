package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"golang.org/x/term"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/selfupdate"
	"github.com/austinrr/knapsack/internal/source/github"
)

// Exit codes, following the diff/grep convention (DESIGN.md §8.6).
const (
	exitOK      = 0
	exitPending = 1 // check only: changes are pending
	exitError   = 2
)

// env is everything the CLI takes from its surroundings. Tests build their
// own, so they never depend on the real process.
type env struct {
	stdout  io.Writer
	stderr  io.Writer
	getenv  func(string) string
	version string
	// released is true when version was set at build time, as in a
	// release archive (version.go).
	released bool
	// executable is the running binary's path, symlinks resolved, or ""
	// if it can't be found. It's resolved once, so the path self-update
	// checks is the one it replaces.
	executable string
	// interactive is whether stderr is a terminal, which the update
	// notice needs.
	interactive bool
	// stdin is where set-key reads a piped key. When stdinTerminal,
	// readPassword reads a typed one without echoing it instead.
	stdin         io.Reader
	stdinTerminal bool
	readPassword  func() ([]byte, error)
	// githubURL is GitHub's API, and ghAuthToken asks the GitHub CLI for a
	// token. Tests replace both, so they never reach the real GitHub.
	githubURL   string
	ghAuthToken func(context.Context) string
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

	exe, err := executable()
	if err != nil {
		exe = ""
	}
	if exe != "" && runtime.GOOS == "windows" {
		selfupdate.RemoveOld(exe) // only Windows' self-update leaves one
	}
	code := run(ctx, os.Args[1:], env{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		getenv:      os.Getenv,
		version:     resolveVersion(buildVersion, debug.ReadBuildInfo),
		released:    buildVersion != "",
		executable:  exe,
		interactive: isTerminal(os.Stderr),

		stdin:         os.Stdin,
		stdinTerminal: isTerminal(os.Stdin),
		readPassword:  func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) },

		githubURL:   github.DefaultBaseURL,
		ghAuthToken: ghAuthToken,
	})
	stop()
	os.Exit(code)
}

// executable is the running binary's path with symlinks resolved, so
// self-update replaces the file itself, not a link to it.
func executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
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

	notice := startUpdateCheck(ctx, root, args, c)
	code := exitCode(c, root.ExecuteContext(ctx))
	notice()
	return code
}

// exitCode renders err, if any, and returns the exit code for it.
func exitCode(c *cli, err error) int {
	e := c.env
	if errors.Is(err, errPending) {
		return exitPending
	}
	if err != nil {
		// A command acting on several addons returns each failure
		// separately, so each gets its own error and hint lines.
		var errs addonErrors
		if !errors.As(err, &errs) {
			errs = addonErrors{err}
		}
		for _, err := range errs {
			renderError(e.stderr, err, c.verbose)
		}
		return exitError
	}
	return exitOK
}

// addonErrors is what a command acting on several addons returns when
// any fail: one error per failed addon, then any error that stopped the
// command. run renders each in turn.
type addonErrors []error

func (e addonErrors) Error() string { return errors.Join(e...).Error() }

// renderError writes err to w: an "error:" line, then for an app.UserError
// a "hint:" line, and with --verbose the underlying cause (DESIGN.md §8.6).
func renderError(w io.Writer, err error, verbose bool) {
	msg := "error: " + err.Error() + "\n"
	var ue *app.UserError
	if errors.As(err, &ue) {
		// The error line is err.Error(), not ue.Msg, so context wrapped
		// around the UserError is kept.
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
