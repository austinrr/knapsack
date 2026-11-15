package tui

import (
	"context"
	"os/exec"
)

// browserCmd opens url with the OS's handler for it. rundll32 takes the
// URL as one argument, which start, through cmd.exe, would split at & in
// a query string.
func browserCmd(ctx context.Context, url string) *exec.Cmd {
	return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url) //nolint:gosec // G204: a URL Knapsack built (Ref.URL, app.ReportURL), as one argument, with no shell
}
