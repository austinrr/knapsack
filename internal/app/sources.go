package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/ref"
)

// KeyOrigin is where a source's API key comes from.
type KeyOrigin string

// The places a key can come from. The environment wins over config.
const (
	KeyNone   KeyOrigin = config.FromNowhere
	KeyConfig KeyOrigin = config.FromConfig
	KeyEnv    KeyOrigin = config.FromEnv
)

// builtSources are the sources this build of Knapsack can install from.
// It's a fixed property of the build, not of how an App was set up, so
// Sources answers the same with or without network sources registered.
// CurseForge and Wago join it when they're built (DESIGN.md §7).
var builtSources = map[string]bool{ref.GitHub: true}

// SourceInfo describes one source, as Sources reports it.
type SourceInfo struct {
	Name string
	// Available is whether Knapsack can install from it yet; CurseForge
	// and Wago are planned (DESIGN.md §7).
	Available bool
	// NeedsKey is whether it takes an API key. GitHub doesn't: its token
	// is optional and never stored, so frontends find it themselves.
	NeedsKey bool
	// Key is where its key comes from, and Env the variable that overrides
	// config, for a source that takes one.
	Key KeyOrigin
	Env string
}

// Sources lists every source Knapsack knows, GitHub first, then those
// that take an API key in config order (DESIGN.md §8.4). Keys are shared
// by every profile.
func (a *App) Sources(ctx context.Context) ([]SourceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c, err := a.config.Load()
	if err != nil {
		return nil, err
	}
	out := []SourceInfo{{Name: ref.GitHub, Available: builtSources[ref.GitHub]}}
	for _, ks := range config.KeyedSources() {
		out = append(out, SourceInfo{
			Name: ks.Name, Available: builtSources[ks.Name], NeedsKey: true,
			Key: KeyOrigin(c.KeyOrigin(ks.Name, a.getenv)), Env: ks.Env,
		})
	}
	return out, nil
}

// SetSourceKeyResult says what SetSourceKey did.
type SetSourceKeyResult struct {
	// Changed is false when config already had that key, or had none to
	// remove.
	Changed bool
	// Overridden is true when the source's environment variable is set,
	// so it, not config, is what's used.
	Overridden bool
	Env        string
	// Available is whether Knapsack can install from the source yet.
	Available bool
}

// SetSourceKey stores the API key for a source in config, shared by every
// profile, or removes it when key is "". Config is written readable only
// by the user while it holds a key.
func (a *App) SetSourceKey(ctx context.Context, name, key string) (SetSourceKeyResult, error) {
	if err := ctx.Err(); err != nil {
		return SetSourceKeyResult{}, err
	}
	ks, err := KeyedSource(name)
	if err != nil {
		return SetSourceKeyResult{}, err
	}
	key = strings.TrimSpace(key)
	if err := checkKey(key); err != nil {
		return SetSourceKeyResult{}, err
	}
	res := SetSourceKeyResult{Env: ks.Env, Available: builtSources[ks.Name]}
	var origin string
	res.Changed, err = a.changeConfig(func(c *config.Config) error {
		if !c.SetAPIKey(ks.Name, key) {
			return fmt.Errorf("config has no key for %s", ks.Name) // KeyedSource found it, so never
		}
		origin = c.KeyOrigin(ks.Name, a.getenv)
		return nil
	})
	res.Overridden = origin == config.FromEnv
	return res, err
}

// errBadKey is the cause of a key checkKey rejects.
var errBadKey = errors.New("not an API key")

// checkKey rejects what can't be an API key, before it's saved: keys are
// one printable word, so whitespace or control characters mean something
// else was pasted or piped with it, and config must be valid UTF-8.
func checkKey(key string) error {
	hint := "an API key is one word with no spaces; check what was pasted or piped in"
	switch {
	case !utf8.ValidString(key):
		return &UserError{Msg: "the API key isn't valid text", Hint: hint, Err: errBadKey}
	case strings.ContainsFunc(key, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		return &UserError{Msg: "the API key has spaces or line breaks in it", Hint: hint, Err: errBadKey}
	}
	return nil
}

// KeyedSource finds the source called name among those that take a key,
// with an error that says why when it isn't one.
func KeyedSource(name string) (config.KeyedSource, error) {
	var names []string
	for _, ks := range config.KeyedSources() {
		if ks.Name == name {
			return ks, nil
		}
		names = append(names, ks.Name)
	}
	if name == ref.GitHub {
		return config.KeyedSource{}, &UserError{
			Msg:  "Knapsack doesn't store GitHub tokens",
			Hint: "set GITHUB_TOKEN or GH_TOKEN, or log in with `gh auth login`",
		}
	}
	return config.KeyedSource{}, &UserError{
		Msg:  fmt.Sprintf("unknown source %q", name),
		Hint: "sources that take an API key: " + strings.Join(names, ", "),
	}
}
