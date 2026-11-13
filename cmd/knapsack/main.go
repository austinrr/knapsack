package main

import (
	"context"
	"io"
	"os"
	"os/signal"
)

// Exit codes, following the diff/grep convention (DESIGN.md §8.6).
const (
	exitOK    = 0
	exitError = 2
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

// run executes the CLI with args and returns the process exit code. It is
// the single place where errors are rendered, so tests can drive the whole
// program without spawning a process.
func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	root := newRootCmd()
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)

	if err := root.ExecuteContext(ctx); err != nil {
		renderError(stderr, err)
		return exitError
	}
	return exitOK
}

// renderError writes err to w as a single "error: ..." line.
func renderError(w io.Writer, err error) {
	// A failed write to stderr leaves nowhere else to report, so the error
	// is deliberately ignored.
	_, _ = io.WriteString(w, "error: "+err.Error()+"\n")
}
