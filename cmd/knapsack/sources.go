package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
)

// sourceJSON is one source in `sources -o json`.
type sourceJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// KeyFrom is where its key or token comes from: "config", or the
	// environment variable, or "gh" for the GitHub CLI. Omitted when none.
	KeyFrom string `json:"key_from,omitempty"`
}

// sourceStatus is how ready a source is: "ready", "needs key", or "not
// yet" for one Knapsack can't install from yet.
func sourceStatus(s app.SourceInfo) string {
	switch {
	case !s.Available:
		return "not yet"
	case s.NeedsKey && s.Key == app.KeyNone:
		return "needs key"
	}
	return "ready"
}

// keyFrom is where a source's key comes from, as sourceJSON.KeyFrom has it.
// GitHub's token is looked for as commands do, which may run gh.
func keyFrom(ctx context.Context, e env, s app.SourceInfo) string {
	switch {
	case !s.NeedsKey:
		return githubTokenOrigin(ctx, e.getenv, e.ghAuthToken)
	case s.Key == app.KeyEnv:
		return s.Env
	case s.Key == app.KeyConfig:
		return "config"
	}
	return ""
}

// describeKey says where the key comes from, for the table.
func describeKey(s app.SourceInfo, from string) string {
	switch {
	case from == "config":
		return "set in config"
	case from == "gh":
		return "from gh auth token"
	case from != "":
		return "from $" + from
	case !s.NeedsKey:
		return "none (60 requests an hour)"
	}
	return "-"
}

func newSourcesCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "List addon sources and manage their API keys",
		Long: `List the sources Knapsack installs addons from, whether each is ready,
and where its API key comes from.

Keys are shared by every profile. They're stored in config.toml, which is
then readable only by you, and an environment variable overrides each one
(KNAPSACK_CURSEFORGE_API_KEY, KNAPSACK_WAGO_API_KEY), so a config kept in a
dotfiles repo can leave them out. GitHub needs no key: Knapsack uses a token
from GITHUB_TOKEN, GH_TOKEN or the GitHub CLI when there is one, for a
higher rate limit, and never stores it.`,
		Example: `  knapsack sources
  knapsack sources set-key curseforge
  pass show curseforge | knapsack sources set-key curseforge`,
		Args:    subcommandArgs,
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sources, err := c.app.Sources(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				out := make([]sourceJSON, len(sources))
				for i, s := range sources {
					out[i] = sourceJSON{Name: s.Name, Status: sourceStatus(s), KeyFrom: keyFrom(cmd.Context(), c.env, s)}
				}
				return writeJSON(w, out)
			}
			tw := newTable(w)
			out := &printer{w: tw}
			out.printf("NAME\tSTATUS\tKEY\n")
			for _, s := range sources {
				out.printf("%s\t%s\t%s\n", s.Name, sourceStatus(s), describeKey(s, keyFrom(cmd.Context(), c.env, s)))
			}
			if out.err != nil {
				return out.err
			}
			return tw.Flush()
		},
	}
	addOutputFlag(cmd, &output, "json")
	cmd.AddCommand(newSetKeyCmd(c), newUnsetKeyCmd(c))
	return cmd
}

// maxKey bounds what set-key reads from stdin; API keys are short.
const maxKey = 4096

func newSetKeyCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "set-key <source>",
		Short: "Store a source's API key",
		Long: `Store a source's API key in config, for every profile.

On a terminal it asks for the key without showing it. Otherwise it reads
the key from stdin, so it can come from a password manager. It never takes
the key as an argument, which would leave it in your shell history.`,
		Example: `  knapsack sources set-key curseforge
  pass show curseforge | knapsack sources set-key curseforge`,
		Args:    exactArgs("source"),
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Checked first, so nobody types a key for a source that
			// can't take one.
			ks, err := app.KeyedSource(args[0])
			if err != nil {
				return err
			}
			key, err := readKey(cmd, c.env, ks.Name)
			if err != nil {
				return err
			}
			res, err := c.app.SetSourceKey(cmd.Context(), ks.Name, key)
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			if res.Changed {
				out.printf("Saved the %s API key to config.\n", ks.Name)
			} else {
				out.printf("The %s API key is unchanged.\n", ks.Name)
			}
			if out.err != nil {
				return out.err
			}
			return noteKey(cmd, c, ks.Name, res, true)
		},
	}
}

func newUnsetKeyCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "unset-key <source>",
		Short:   "Remove a source's API key from config",
		Example: `  knapsack sources unset-key curseforge`,
		Args:    exactArgs("source"),
		PreRunE: c.buildNetApp,
		RunE: func(cmd *cobra.Command, args []string) error {
			ks, err := app.KeyedSource(args[0])
			if err != nil {
				return err
			}
			res, err := c.app.SetSourceKey(cmd.Context(), ks.Name, "")
			if err != nil {
				return err
			}
			out := &printer{w: cmd.OutOrStdout()}
			if res.Changed {
				out.printf("Removed the %s API key from config.\n", ks.Name)
			} else {
				out.printf("There's no %s API key in config.\n", ks.Name)
			}
			if out.err != nil {
				return out.err
			}
			return noteKey(cmd, c, ks.Name, res, false)
		},
	}
}

// noteKey adds what affects the key just set or removed, on stderr: an
// environment variable that overrides config, and for a key just set, a
// source Knapsack can't install from yet.
func noteKey(cmd *cobra.Command, c *cli, name string, res app.SetSourceKeyResult, set bool) error {
	w := cmd.ErrOrStderr()
	if res.Overridden {
		msg := "%s is set, so Knapsack uses it instead of config.\n"
		if !set {
			msg = "%s is still set, so Knapsack still has a key.\n"
		}
		if _, err := fmt.Fprintf(w, msg, res.Env); err != nil {
			return err
		}
	}
	if !set {
		return nil
	}
	sources, err := c.app.Sources(cmd.Context())
	if err != nil {
		return err
	}
	for _, s := range sources {
		if s.Name == name && !s.Available {
			_, err := fmt.Fprintf(w, "Knapsack can't install from %s yet; it will use the key once it can.\n", name)
			return err
		}
	}
	return nil
}

// readKey gets the key for source: typed at a prompt that doesn't echo it
// when stdin is a terminal, else read from stdin.
func readKey(cmd *cobra.Command, e env, source string) (string, error) {
	var key string
	if e.stdinTerminal && e.readPassword != nil {
		if _, err := fmt.Fprintf(cmd.ErrOrStderr(), "API key for %s: ", source); err != nil {
			return "", err
		}
		b, err := e.readPassword()
		_, _ = fmt.Fprintln(cmd.ErrOrStderr()) // the prompt's line; nothing else to report a failure on
		if err != nil {
			return "", fmt.Errorf("read the key: %w", err)
		}
		key = string(b)
	} else {
		if e.stdin == nil {
			return "", errors.New("no stdin to read the key from")
		}
		b, err := io.ReadAll(io.LimitReader(e.stdin, maxKey+1))
		if err != nil {
			return "", fmt.Errorf("read the key from stdin: %w", err)
		}
		if len(b) > maxKey {
			return "", &app.UserError{Msg: fmt.Sprintf("stdin has more than %d bytes, which is too long for an API key", maxKey)}
		}
		key = string(b)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", &app.UserError{
			Msg:  "no API key given",
			Hint: fmt.Sprintf("type it when asked, or pipe it in: `pass show %s | knapsack sources set-key %s`", source, source),
		}
	}
	return key, nil
}
