//go:build !darwin && !windows

package tui

import (
	"context"
	"os/exec"
)

// browserCmd opens url with the desktop's handler for it.
func browserCmd(ctx context.Context, url string) *exec.Cmd {
	return exec.CommandContext(ctx, "xdg-open", url) //nolint:gosec // G204: a URL Knapsack built (Ref.URL, app.ReportURL), as one argument, with no shell
}
