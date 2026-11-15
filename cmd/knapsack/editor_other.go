//go:build !windows

package main

import (
	"context"
	"os/exec"
)

// editorCmd runs editor on path through the shell, as git runs it, so it
// can carry arguments or be a quoted path. path is an argument to the
// script, not part of it, so nothing in it is interpreted. The command is
// the user's own $EDITOR, which is meant to run: gosec's G204 can't tell
// that from a tainted one.
func editorCmd(ctx context.Context, editor, path string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "sh", path) //nolint:gosec // G204: the user's $EDITOR, as above
}
