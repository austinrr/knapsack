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
	"github.com/austinrr/knapsack/internal/source/wago"
)

// lazyWago is the Wago source, built on first use as config says: reading
// Wago's website, or its official API with the key from
// KNAPSACK_WAGO_API_KEY or config, so a command with no Wago addons never
// reads them. Through the API without a key, resolving fails with
// ErrSourceNotConfigured. Config is read again for it, rather than passed
// from the App's session: sources are built before any command loads
// config.
type lazyWago struct {
	client  *http.Client
	baseURL string // the API's
	siteURL string // the website's
	config  *config.Store
	getenv  func(string) string

	once sync.Once
	src  source.Source
	err  error
}

func (l *lazyWago) source() (source.Source, error) {
	l.once.Do(func() {
		c, err := l.config.Load()
		if err != nil {
			l.err = fmt.Errorf("read Wago's settings: %w", err)
			return
		}
		if c.API(ref.Wago) == config.APIWebsite {
			l.src = wago.NewWebsite(l.client, l.siteURL)
			return
		}
		// Trimmed as GitHub's token is: a key from a file ends in a newline.
		l.src = wago.New(l.client, l.baseURL, strings.TrimSpace(c.APIKey(ref.Wago, l.getenv)))
	})
	return l.src, l.err
}

// Name implements source.Source.
func (l *lazyWago) Name() string { return ref.Wago }

// Resolve implements source.Source.
func (l *lazyWago) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	src, err := l.source()
	if err != nil {
		return source.Release{}, err
	}
	return src.Resolve(ctx, id, f)
}
