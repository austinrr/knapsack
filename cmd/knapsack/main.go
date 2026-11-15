package main

import (
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"syscall"

	"golang.org/x/term"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/selfupdate"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/source/curseforge"
	"github.com/austinrr/knapsack/internal/source/github"
	"github.com/austinrr/knapsack/internal/source/wago"
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
	// platform is the OS and architecture, as "linux/amd64", for an issue
	// report.
	platform string
	// home is the user's home directory, or "" if there's none.
	home string
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
	readPassword  func(context.Context) ([]byte, error)
	// githubURL is GitHub's API, and ghAuthToken asks the GitHub CLI for a
	// token. Tests replace both, so they never reach the real GitHub.
	githubURL   string
	ghAuthToken func(context.Context) string
	// wagoURL and curseforgeURL are Wago Addons' and CurseForge's APIs,
	// and wagoSiteURL and curseforgeSiteURL their websites, which tests
	// replace too.
	wagoURL           string
	wagoSiteURL       string
	curseforgeURL     string
	curseforgeSiteURL string
	// cfwidgetURL is CFWidget, where a CurseForge slug's ID is looked up.
	cfwidgetURL string
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
		platform:    runtime.GOOS + "/" + runtime.GOARCH,
		executable:  exe,
		home:        userHome(),
		interactive: isTerminal(os.Stderr),

		stdin: os.Stdin,
		// term.IsTerminal, not isTerminal: /dev/null and NUL are
		// character devices too, but not terminals to read a key at.
		stdinTerminal: term.IsTerminal(int(os.Stdin.Fd())),
		readPassword:  readPassword,

		githubURL:   github.DefaultBaseURL,
		ghAuthToken: ghAuthToken,
		wagoURL:     wago.DefaultBaseURL,
		wagoSiteURL: wago.DefaultSiteURL,

		curseforgeURL:     curseforge.DefaultBaseURL,
		curseforgeSiteURL: curseforge.DefaultSiteURL,
		cfwidgetURL:       curseforge.DefaultWidgetURL,
	})
	stop()
	os.Exit(code)
}

// userHome is the user's home directory, or "" when there's none: it only
// narrows where set-profile --auto looks.
func userHome() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

// readPassword reads a line from the terminal on stdin without echoing it.
// Ctrl-C cancels ctx, which the read itself can't see, so it's watched
// here: the terminal gets its echo back, and the read is abandoned to the
// process's exit. Otherwise the second Ctrl-C would kill the process with
// echo still off.
func readPassword(ctx context.Context) ([]byte, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return nil, err
	}
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, err := term.ReadPassword(fd)
		done <- result{b, err}
	}()
	select {
	case r := <-done:
		return r.b, r.err
	case <-ctx.Done():
		_ = term.Restore(fd, state) // the best that can be done; the error is ctx's
		return nil, ctx.Err()
	}
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
	c := &cli{env: e, rawArgs: args}
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
			renderError(e.stderr, err, c.verbose, e.version+" on "+e.platform)
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
// A source's website answering unexpectedly also gets a "report:" line, a
// link to open an issue about it, filled in with the error and running,
// the version and platform ("v0.2.0 on linux/amd64").
func renderError(w io.Writer, err error, verbose bool, running string) {
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
	if errors.Is(err, source.ErrUnexpectedWebsite) {
		msg += "report: " + issueURL(err, running) + "\n"
	}
	// A failed write to stderr leaves nowhere else to report, so the error
	// is deliberately ignored.
	_, _ = io.WriteString(w, msg)
}

// issueURL is a link that opens an issue about err, a source's website
// answering unexpectedly, with the error and the Knapsack running filled
// in. The error names the addon and the request, but nothing of the
// user's.
func issueURL(err error, running string) string {
	body := "Knapsack couldn't read a source's website:\n\n```\n" + err.Error() + "\n```\n\n" +
		"Knapsack " + running + "\n"
	q := url.Values{"title": {"Unexpected answer from a source's website"}, "body": {body}}
	return app.IssuesURL + "/new?" + q.Encode()
}
