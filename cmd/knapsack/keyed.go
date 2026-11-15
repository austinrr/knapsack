package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
	"github.com/austinrr/knapsack/internal/source/curseforge"
	"github.com/austinrr/knapsack/internal/source/wago"
)

// lazyKeyed is a source that takes an API key, CurseForge or Wago, built on
// first use as config says: reading the source's official API with the key
// from its environment variable or config, or its website. A command with
// none of its addons never reads them. Through the API without a key,
// resolving fails with ErrSourceNotConfigured. Config is read again for
// it, rather than passed from the App's session: sources are built before
// any command loads config.
type lazyKeyed struct {
	name   string
	config *config.Store
	getenv func(string) string
	// official builds the source on its API with key, website on its
	// website.
	official func(key string) source.Source
	website  func() source.Source

	once sync.Once
	src  source.Source
	err  error
}

func (l *lazyKeyed) source() (source.Source, error) {
	l.once.Do(func() {
		c, err := l.config.Load()
		if err != nil {
			l.err = fmt.Errorf("read %s's settings: %w", l.name, err)
			return
		}
		if c.API(l.name) == config.APIWebsite {
			l.src = l.website()
			return
		}
		// Trimmed as GitHub's token is: a key from a file ends in a newline.
		l.src = l.official(strings.TrimSpace(c.APIKey(l.name, l.getenv)))
	})
	return l.src, l.err
}

// Name implements source.Source.
func (l *lazyKeyed) Name() string { return l.name }

// Resolve implements source.Source.
func (l *lazyKeyed) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	src, err := l.source()
	if err != nil {
		return source.Release{}, err
	}
	return src.Resolve(ctx, id, f)
}

// ID implements source.Identifier, for a source that is one.
func (l *lazyKeyed) ID(ctx context.Context, id string) (string, error) {
	src, err := l.source()
	if err != nil {
		return "", err
	}
	if idr, ok := src.(source.Identifier); ok {
		return idr.ID(ctx, id)
	}
	return id, nil
}

// keyedSources are CurseForge and Wago, each read as config says.
func keyedSources(client *http.Client, store *config.Store, e env) []source.Source {
	return []source.Source{
		&lazyKeyed{
			name: ref.CurseForge, config: store, getenv: e.getenv,
			official: func(key string) source.Source { return curseforge.New(client, e.curseforgeURL, key) },
			website:  func() source.Source { return curseforge.NewWebsite(client, e.curseforgeSiteURL, e.cfwidgetURL) },
		},
		&lazyKeyed{
			name: ref.Wago, config: store, getenv: e.getenv,
			official: func(key string) source.Source { return wago.New(client, e.wagoURL, key) },
			website:  func() source.Source { return wago.NewWebsite(client, e.wagoSiteURL) },
		},
	}
}
