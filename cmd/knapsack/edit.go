package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/signal"
	"runtime"
	"slices"
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
// editor is run by the shell (editorCmd), as kubectl and git run it, so it
// can carry arguments ("code --wait") or be a quoted path with spaces.
//
// Ctrl-C in the editor reaches Knapsack too, which shares its terminal, as
// it does vi's insert mode, constantly. So while the editor runs, Knapsack
// ignores it, as git does, and ctx is never what stops the editor: only
// the editor decides when it's done.
func runEditor(ctx context.Context, editor, path string) error {
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, os.Interrupt) // caught, so not fatal, and dropped
	defer signal.Stop(interrupts)
	cmd := editorCmd(context.WithoutCancel(ctx), editor, path)
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
// what's being edited, and passes what's saved to save, with a context
// that a Ctrl-C typed in the editor didn't cancel. When save finds
// it invalid (app.ErrInvalidEdit), the editor opens again with the error
// above the text, as kubectl edit does, until it's saved as valid, or
// left unchanged, which cancels. On any other failure, the text is kept
// in a file whose path the error gives, so nothing typed is lost.
func (c *cli) edit(ctx context.Context, notes []string, text []byte, save func(ctx context.Context, edited []byte) error) (saved bool, err error) {
	ctx = context.WithoutCancel(ctx)
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
		if err := c.env.runEditor(ctx, editor, path); err != nil {
			if lastErr == nil {
				return false, err // nothing typed but the original
			}
			keep = true
			return false, fmt.Errorf("%w (your edit is kept in %s)", err, path)
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
		err = save(ctx, text)
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
			saved, res, err := c.editProfile(cmd.Context(), name)
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

// editProfile opens a profile in the user's editor, as profiles edit
// does, and saves it. saved is false when the edit was cancelled.
func (c *cli) editProfile(ctx context.Context, name string) (saved bool, res app.ProfileEditResult, err error) {
	original, err := c.app.ProfileForEdit(ctx, name)
	if err != nil {
		return false, res, err
	}
	notes := []string{
		fmt.Sprintf("Editing profile %q. Save and quit to apply; quit without saving to cancel.", name),
		"Lines starting with #| are dropped.",
	}
	saved, err = c.edit(ctx, notes, original, func(ctx context.Context, edited []byte) error {
		var err error
		res, err = c.app.SaveProfileEdit(ctx, name, original, edited)
		return err
	})
	return saved, res, err
}

// noteProfileEdit says on stderr what to run for the addons an edit
// added or removed.
func noteProfileEdit(cmd *cobra.Command, name string, res app.ProfileEditResult) error {
	w := &printer{w: cmd.ErrOrStderr()}
	if n := len(res.Added); n > 0 {
		w.printf("%s added: install with `knapsack update -p %s`.\n", app.Count(n, "addon"), name)
	}
	if n := len(res.Removed); n > 0 {
		w.printf("%s removed: uninstall with `knapsack update --prune -p %s`.\n", app.Count(n, "addon"), name)
	}
	return w.err
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
config later rewrite it in a fixed layout. A profile taken out is deleted
as knapsack profiles delete deletes it, and one renamed keeps its record
of what's installed. API keys are shown as they're stored: the copy being
edited, next to config.toml, is readable only by you, and removed once
saved.`,
		Example: "  knapsack config edit",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			original, err := c.app.ConfigForEdit(cmd.Context())
			if err != nil {
				return err
			}
			var res app.ConfigEditResult
			notes := []string{
				"Editing config.toml. Save and quit to apply; quit without saving to cancel.",
				"Lines starting with #| are dropped; other comments are kept.",
			}
			saved, err := c.edit(cmd.Context(), notes, original, func(ctx context.Context, edited []byte) error {
				var err error
				res, err = c.app.SaveConfigEdit(ctx, original, edited)
				return err
			})
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			if !saved || !res.Changed {
				out.printf("config.toml unchanged.\n")
				return out.err
			}
			out.printf("Saved config.toml.\n")
			for _, old := range slices.Sorted(maps.Keys(res.Renamed)) {
				out.printf("Renamed profile %q to %q, with its record of what's installed.\n", old, res.Renamed[old])
			}
			for _, name := range res.Deleted {
				out.printf("Deleted profile %q, and its record of what's installed; addon files are untouched.\n", name)
			}
			return out.err
		},
	}
}
