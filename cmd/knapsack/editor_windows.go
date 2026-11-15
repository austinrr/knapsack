package main

import (
	"context"
	"os/exec"
	"syscall"
)

// editorCmd runs editor on path through cmd.exe, as git for Windows does,
// so it can carry arguments or be a quoted path. cmd.exe doesn't parse a
// command line the way Go quotes one, so the line is given as it is: /S
// strips only the outer quotes, leaving editor and the quoted path, which
// is ours and has no quotes in it. The command is the user's own $EDITOR,
// which is meant to run: gosec's G204 can't tell that from a tainted one.
func editorCmd(ctx context.Context, editor, path string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "cmd.exe") //nolint:gosec // G204: the user's $EDITOR, as above
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /S /C "` + editor + ` "` + path + `""`}
	return cmd
}
