package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/ref"
)

// KeyOrigin is where a source's API key comes from.
type KeyOrigin string

// The places a key can come from. The environment wins over config.
const (
	KeyNone   KeyOrigin = ""
	KeyConfig KeyOrigin = "config"
	KeyEnv    KeyOrigin = "env"
)

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
	out := []SourceInfo{{Name: ref.GitHub, Available: a.sources.Has(ref.GitHub)}}
	stored := c.Sources.Keys()
	for _, ks := range config.KeyedSources() {
		info := SourceInfo{Name: ks.Name, Available: a.sources.Has(ks.Name), NeedsKey: true, Env: ks.Env}
		switch {
		case a.getenv(ks.Env) != "":
			info.Key = KeyEnv
		case stored[ks.Name] != "":
			info.Key = KeyConfig
		}
		out = append(out, info)
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
	changed, err := a.changeConfig(func(c *config.Config) error {
		c.SetAPIKey(ks.Name, key)
		return nil
	})
	return SetSourceKeyResult{Changed: changed, Overridden: a.getenv(ks.Env) != "", Env: ks.Env}, err
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
