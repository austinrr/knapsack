package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
)

// editorEnvs are the variables that name the editor, in the order they're
// read, as kubectl and git read theirs.
var editorEnvs = []string{"KNAPSACK_EDITOR", "VISUAL", "EDITOR"}

// editorCommand is the editor to run: the first of editorEnvs that's set,
// else vi, or Notepad on Windows.
func editorCommand(getenv func(string) string) string {
	for _, k := range editorEnvs {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
	}
	if runtime.GOOS == "windows" {
		return "notepad"
	}
	return "vi"
}

// runEditor runs editor on path, on the terminal, and waits for it. The
// editor is run by the shell, as kubectl and git run it, so it can carry
// arguments ("code --wait") or be a quoted path with spaces.
func runEditor(ctx context.Context, editor, path string) error {
	// The command is the user's own $EDITOR, which is meant to run: gosec's
	// G204 can't tell that from a tainted one. The path is ours, and on
	// Unix is passed as an argument, not in the script.
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/C", editor+` "`+path+`"`) //nolint:gosec // G204: the user's $EDITOR, as above
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", editor+` "$1"`, "sh", path) //nolint:gosec // G204: the user's $EDITOR, as above
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor %q: %w", editor, err)
	}
	return nil
}

// noteMark starts each line of the notes edit puts above what's edited:
// what's being edited, how to cancel, and the error with the last try. On
// save, the lines with it are dropped, wherever they are, so a file's own
// comments, config.toml's, are kept.
const noteMark = "#| "

// edit opens text in the user's editor, after notes, the lines that say
// what's being edited, and passes what's saved to save. When save finds
// it invalid (app.ErrInvalidEdit), the editor opens again with the error
// above the text, as kubectl edit does, until it's saved as valid, or
// left unchanged, which cancels. On any other failure, the text is kept
// in a file whose path the error gives, so nothing typed is lost.
func (c *cli) edit(cmd *cobra.Command, notes []string, text []byte, save func(edited []byte) error) (saved bool, err error) {
	dir, err := c.app.EditDir()
	if err != nil {
		return false, fmt.Errorf("edit: %w", err)
	}
	f, err := os.CreateTemp(dir, "edit-*.toml") // 0600: config can hold API keys
	if err != nil {
		return false, fmt.Errorf("edit: %w", err)
	}
	path := f.Name()
	_ = f.Close() // reopened by WriteFile; nothing written yet
	keep := false
	defer func() {
		if !keep {
			_ = os.Remove(path) // a temporary copy; nothing else uses it
		}
	}()

	editor := editorCommand(c.env.getenv)
	var lastErr error
	for {
		shown := withNotes(notes, lastErr, text)
		if err := os.WriteFile(path, shown, 0o600); err != nil {
			return false, fmt.Errorf("edit: %w", err)
		}
		if err := c.env.runEditor(cmd.Context(), editor, path); err != nil {
			return false, err
		}
		got, err := os.ReadFile(path)
		if err != nil {
			return false, fmt.Errorf("edit: %w", err)
		}
		if bytes.Equal(got, shown) {
			if lastErr != nil {
				keep = true
				return false, &app.UserError{
					Msg:  "edit cancelled: " + lastErr.Error(),
					Hint: "your edit is kept in " + path,
					Err:  lastErr,
				}
			}
			return false, nil
		}
		text = withoutNotes(got)
		err = save(text)
		switch {
		case err == nil:
			return true, nil
		case errors.Is(err, app.ErrInvalidEdit):
			lastErr = err
		default:
			keep = true
			return false, fmt.Errorf("%w (your edit is kept in %s)", err, path)
		}
	}
}

// withNotes puts notes, and err when there is one, above text, each line
// marked with noteMark.
func withNotes(notes []string, err error, text []byte) []byte {
	var b bytes.Buffer
	for _, n := range notes {
		b.WriteString(noteMark + n + "\n")
	}
	if err != nil {
		for line := range strings.SplitSeq(err.Error(), "\n") {
			b.WriteString(noteMark + "error: " + line + "\n")
		}
	}
	if b.Len() > 0 {
		b.WriteString(strings.TrimSpace(noteMark) + "\n")
	}
	b.Write(text)
	return b.Bytes()
}

// withoutNotes drops the lines withNotes added.
func withoutNotes(data []byte) []byte {
	var out bytes.Buffer
	for line := range bytes.Lines(data) {
		trimmed := bytes.TrimRight(line, "\r\n")
		if bytes.HasPrefix(trimmed, []byte(noteMark)) || bytes.Equal(trimmed, []byte(strings.TrimSpace(noteMark))) {
			continue
		}
		out.Write(line)
	}
	return out.Bytes()
}

func newProfilesEditCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "edit <name>",
		Short: "Edit a profile's folder, flavor and addons in your editor",
		Long: `Edit a profile in your editor: its AddOns path, its flavor, and the
addons it should have. The editor is $KNAPSACK_EDITOR, $VISUAL or
$EDITOR, else vi (Notepad on Windows).

Save and quit to apply; quit without saving to cancel. If what's saved
isn't valid, the editor opens again with the error at the top. Addons
added are installed by knapsack update, and those taken out are
uninstalled by knapsack update --prune, so edit says what the profile
should have, and update makes it so.`,
		Example: "  knapsack profiles edit retail\n  EDITOR='code --wait' knapsack profiles edit retail",
		Args:    exactArgs("profile name"),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			original, err := c.app.ProfileForEdit(cmd.Context(), name)
			if err != nil {
				return err
			}
			var res app.ProfileEditResult
			notes := []string{
				fmt.Sprintf("Editing profile %q. Save and quit to apply; quit without saving to cancel.", name),
				"Lines starting with #| are dropped.",
			}
			saved, err := c.edit(cmd, notes, original, func(edited []byte) error {
				var err error
				res, err = c.app.SaveProfileEdit(cmd.Context(), name, original, edited)
				return err
			})
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			if !saved || !res.Changed {
				out.printf("Profile %q unchanged.\n", name)
				return out.err
			}
			out.printf("Profile %q saved.\n", name)
			if out.err != nil {
				return out.err
			}
			return noteProfileEdit(cmd, name, res)
		},
	}
}

// noteProfileEdit says on stderr what to run for the addons an edit
// added or removed.
func noteProfileEdit(cmd *cobra.Command, name string, res app.ProfileEditResult) error {
	w := &printer{w: cmd.ErrOrStderr()}
	if n := len(res.Added); n > 0 {
		w.printf("%s added: install with `knapsack update -p %s`.\n", count(n, "addon"), name)
	}
	if n := len(res.Removed); n > 0 {
		w.printf("%s removed: uninstall with `knapsack update --prune -p %s`.\n", count(n, "addon"), name)
	}
	return w.err
}

// count is n things, as "1 addon" or "2 addons".
func count(n int, thing string) string {
	if n == 1 {
		return "1 " + thing
	}
	return fmt.Sprintf("%d %ss", n, thing)
}

func newConfigEditCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "edit",
		Short: "Edit config.toml in your editor",
		Long: `Edit config.toml in your editor: profiles, sources and settings for all
of Knapsack. The editor is $KNAPSACK_EDITOR, $VISUAL or $EDITOR, else vi
(Notepad on Windows).

Save and quit to apply; quit without saving to cancel. If what's saved
isn't valid, the editor opens again with the error at the top. The file
is saved as you wrote it, comments included, though commands that change
config later rewrite it in a fixed layout. API keys are shown as they're
stored: the copy being edited is readable only by you, and removed once
saved.`,
		Example: "  knapsack config edit",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			original, err := c.app.ConfigForEdit(cmd.Context())
			if err != nil {
				return err
			}
			changed := false
			notes := []string{
				"Editing config.toml. Save and quit to apply; quit without saving to cancel.",
				"Lines starting with #| are dropped; other comments are kept.",
			}
			saved, err := c.edit(cmd, notes, original, func(edited []byte) error {
				var err error
				changed, err = c.app.SaveConfigEdit(cmd.Context(), original, edited)
				return err
			})
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			if saved && changed {
				out.printf("Saved config.toml.\n")
			} else {
				out.printf("config.toml unchanged.\n")
			}
			return out.err
		},
	}
}
