package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
)

// sourceJSON is one source in `sources list -o json`.
type sourceJSON struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	// API is how it's read: "official" or "website".
	API string `json:"api"`
	// KeyFrom is where its key or token comes from: "config", or the
	// environment variable, or "gh" for the GitHub CLI. Omitted when none.
	KeyFrom string `json:"key_from,omitempty"`
}

// keyFrom is where a source's key comes from (app.KeyFrom). GitHub's
// token is looked for as commands do, which may run gh.
func keyFrom(ctx context.Context, e env, s app.SourceInfo) string {
	github := ""
	if !s.NeedsKey {
		_, github = findGitHubToken(ctx, e.getenv, e.ghAuthToken)
	}
	return app.KeyFrom(s, github)
}

// describeKey is app.DescribeKey, with "-" for a source with no key, as
// the table shows an empty cell.
func describeKey(s app.SourceInfo, from string) string {
	if d := app.DescribeKey(s, from); d != "" {
		return d
	}
	return "-"
}

func newSourcesCmd(c *cli) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sources",
		Short: "List addon sources and set how each is read",
		Long: `List the sources Knapsack installs addons from, choose how each is read,
and manage their API keys.

CurseForge and Wago are read through their official APIs by default, which
need an API key (` + "`knapsack sources set-key`" + `). They give keys out only to
some, so ` + "`knapsack sources set-api <source> website`" + ` reads the source's
website instead, as a browser does, which needs none. The website isn't an
API, so it can change under Knapsack: when it does, the error has a link to
report it.

Keys are shared by every profile. They're stored in config.toml, which is
then readable only by you, and an environment variable overrides each one
(KNAPSACK_CURSEFORGE_API_KEY, KNAPSACK_WAGO_API_KEY), so a config kept in a
dotfiles repo can leave them out. GitHub needs no key: Knapsack uses a token
from GITHUB_TOKEN, GH_TOKEN or the GitHub CLI when there is one, for a
higher rate limit, and never stores it.`,
	}
	return c.groupCmd(cmd, newSourcesListCmd(c), newSetAPICmd(c), newSetKeyCmd(c), newUnsetKeyCmd(c))
}

func newSourcesListCmd(c *cli) *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List sources, how each is read, and whether it's ready",
		Long: `List the sources Knapsack installs addons from.

STATUS is ready, or needs key for a source read through its official API
without one (set one with ` + "`knapsack sources set-key`" + `, or read its website
instead). API is how it's read: official or website
(` + "`knapsack sources set-api`" + `). KEY says where the key comes
from: config, or the environment variable that overrides it.
GitHub needs none, but uses a token from GITHUB_TOKEN, GH_TOKEN or the
GitHub CLI when there is one, for a higher rate limit.`,
		Example: `  knapsack sources list
  knapsack sources list -o json`,
		Args: noArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sources, err := c.app.Sources(cmd.Context())
			if err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if output == "json" {
				out := make([]sourceJSON, len(sources))
				for i, s := range sources {
					out[i] = sourceJSON{Name: s.Name, Status: s.Status(), API: string(s.API), KeyFrom: keyFrom(cmd.Context(), c.env, s)}
				}
				return writeJSON(w, out)
			}
			tw := newTable(w)
			out := &printer{w: tw}
			out.printf("NAME\tSTATUS\tAPI\tKEY\n")
			for _, s := range sources {
				out.printf("%s\t%s\t%s\t%s\n", s.Name, s.Status(), s.API, describeKey(s, keyFrom(cmd.Context(), c.env, s)))
			}
			if out.err != nil {
				return out.err
			}
			return tw.Flush()
		},
	}
	addOutputFlag(cmd, &output, "json")
	return cmd
}

func newSetAPICmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "set-api <source> <official|website>",
		Short: "Choose whether a source is read from its official API or its website",
		Long: `Choose how a source is read, for every profile.

official, the default, reads the source's official API, which needs an API
key (` + "`knapsack sources set-key`" + `); without one, commands fail until it's
set. website reads the source's website as a browser does, which needs no
key.`,
		Example: `  knapsack sources set-api wago website
  knapsack sources set-api wago official`,
		Args:              twoArgs("source", "api"),
		ValidArgsFunction: completeSetAPI,
		RunE: func(cmd *cobra.Command, args []string) error {
			res, err := c.app.SetSourceAPI(cmd.Context(), args[0], args[1])
			if err != nil {
				return err
			}
			api := res.API
			out := &printer{w: cmd.OutOrStdout()}
			now := "now"
			if !res.Changed {
				now = "already"
			}
			out.printf("%s is %s read from its %s.\n", args[0], now, apiNoun(api))
			if out.err != nil {
				return out.err
			}
			if res.NeedsKey {
				_, err = fmt.Fprintf(cmd.ErrOrStderr(), "The official API needs a key: set one with `knapsack sources set-key %s`.\n", args[0])
			}
			return err
		},
	}
}

// completeSetAPI completes set-api's arguments: a source with settings,
// then how to read it.
func completeSetAPI(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
	var out []string
	switch len(args) {
	case 0:
		for _, ks := range config.KeyedSources() {
			out = append(out, ks.Name)
		}
	case 1:
		for _, a := range config.APIs {
			out = append(out, string(a))
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

// apiNoun names api in a sentence: "website", or "official API".
func apiNoun(api config.API) string {
	if api == config.APIOfficial {
		return "official API"
	}
	return "website"
}

// maxKey bounds what set-key reads from stdin; API keys are short.
const maxKey = 4096

func newSetKeyCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:   "set-key <source>",
		Short: "Store a source's API key",
		Long: `Store a source's API key in config, for every profile.

On a terminal it asks for the key without showing it. Otherwise it reads
the key's first line from stdin, so it can come from a password manager
such as pass, which prints the secret first. It never takes the key as an
argument, which would leave it in your shell history.`,
		Example: `  knapsack sources set-key wago
  pass show wago | knapsack sources set-key wago`,
		Args: exactArgs("source"),
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
			return noteKey(cmd, ks.Name, res, true)
		},
	}
}

func newUnsetKeyCmd(c *cli) *cobra.Command {
	return &cobra.Command{
		Use:     "unset-key <source>",
		Short:   "Remove a source's API key from config",
		Example: `  knapsack sources unset-key wago`,
		Args:    exactArgs("source"),
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
			return noteKey(cmd, ks.Name, res, false)
		},
	}
}

// noteKey adds what affects the key just set or removed, on stderr: an
// environment variable that overrides config, and for a key just set, a
// source read from its website, which doesn't use it.
func noteKey(cmd *cobra.Command, name string, res app.SetSourceKeyResult, set bool) error {
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
	if set && res.Website {
		_, err := fmt.Fprintf(w, "%s is read from its website, which needs no key; `knapsack sources set-api %s official` uses the key.\n", name, name)
		return err
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
		b, err := e.readPassword(cmd.Context())
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
		// Only the first line, as password managers print it: `pass show`
		// puts the secret first, then fields like login: on later lines.
		key, _, _ = strings.Cut(string(b), "\n")
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
