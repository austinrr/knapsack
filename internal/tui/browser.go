package tui

import "context"

// openBrowser starts the OS's handler on url without waiting for the
// browser. Its stdin, stdout and stderr are left nil, the null device,
// so it never writes on the TUI's terminal. The handler only hands url
// to the browser, but quitting right after o mustn't stop it doing so,
// so ctx's cancellation doesn't reach it; ctx is there for the linter's
// noctx, which rules out exec.Command.
func openBrowser(ctx context.Context, url string) error {
	cmd := browserCmd(context.WithoutCancel(ctx), url)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reaped; its exit says nothing useful
	return nil
}
