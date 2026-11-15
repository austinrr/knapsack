package source

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

// Errors every source wraps, so callers can match them with errors.Is
// without knowing which source failed:
//
//	fmt.Errorf("github %s: %w", id, source.ErrNotFound)
var (
	// ErrNotFound means the addon doesn't exist or has no releases.
	ErrNotFound = errors.New("not found")
	// ErrNoCompatibleRelease means the newest release has nothing for the
	// flavor, or Knapsack can't tell which of its files to use.
	ErrNoCompatibleRelease = errors.New("no compatible release")
	// ErrRateLimited means the source refused because of too many requests.
	ErrRateLimited = errors.New("rate limited")
	// ErrUnauthorized means the source rejected the credentials sent.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrSourceNotConfigured means the source isn't registered, usually
	// because it needs an API key that isn't set.
	ErrSourceNotConfigured = errors.New("source not configured")
	// ErrUnexpectedWebsite means a source read from its website, rather
	// than an official API, answered in a way Knapsack doesn't understand:
	// the website has likely changed since this release of Knapsack.
	ErrUnexpectedWebsite = errors.New("unexpected answer from the website")
)

// Source resolves an addon ID to the newest release compatible with a
// flavor.
type Source interface {
	// Name is the source's name in refs, such as "github".
	Name() string
	// Resolve finds the newest release of the addon id for f.
	Resolve(ctx context.Context, id string, f flavor.Flavor) (Release, error)
}

// Release is one downloadable release of an addon.
type Release struct {
	// Version is the release's label, for display only.
	Version string
	// DownloadURL is the archive to download.
	DownloadURL string
	// SHA256 is the archive's digest in lowercase hex, when the source
	// publishes one. It's verified on download and is the release's
	// identity. When it's empty, the release can only be identified by
	// downloading it and hashing the file.
	SHA256 string
	// Header is sent with the download, for a source whose downloads need
	// its API key. Nil means none.
	Header http.Header
}

// Asset is one file attached to a published release, as Knapsack's own
// releases are: an archive per platform.
type Asset struct {
	Name string
	URL  string
	// SHA256 is the digest in lowercase hex, or "" when the source
	// publishes none.
	SHA256 string
}

// Registry holds the configured sources by name.
type Registry struct {
	sources map[string]Source
}

// NewRegistry returns a Registry of sources. Leave out a source that isn't
// configured; resolving its refs then returns ErrSourceNotConfigured.
func NewRegistry(sources ...Source) *Registry {
	r := &Registry{sources: map[string]Source{}}
	for _, s := range sources {
		r.sources[s.Name()] = s
	}
	return r
}

// Resolve finds the newest release of the addon r for f, through the
// source r names.
func (reg *Registry) Resolve(ctx context.Context, r ref.Ref, f flavor.Flavor) (Release, error) {
	s, ok := reg.sources[r.Source]
	if !ok {
		return Release{}, fmt.Errorf("%s %s: %w", r.Source, r.ID, ErrSourceNotConfigured)
	}
	return s.Resolve(ctx, r.ID, f)
}
