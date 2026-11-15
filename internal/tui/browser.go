package tui

import "context"

// openBrowser starts the OS's handler on url without waiting for the
// browser, and without letting it write on the TUI's terminal. The
// handler only hands url to the browser, but quitting right after o
// mustn't stop it doing so, so ctx's cancellation doesn't reach it.
func openBrowser(ctx context.Context, url string) error {
	cmd := browserCmd(context.WithoutCancel(ctx), url)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }() // reaped; its exit says nothing useful
	return nil
}
