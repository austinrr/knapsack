// Package wago resolves addons published on Wago Addons (DESIGN.md §7): the
// newest stable release for a flavor, through Wago's external API, which
// needs a per-user API key.
package wago

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/httpx"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// DefaultBaseURL is Wago Addons' external API.
const DefaultBaseURL = "https://addons.wago.io/api/external"

// Source resolves Wago Addons releases. It's safe for concurrent use.
type Source struct {
	client  *http.Client
	baseURL string
	// origin is baseURL's, as httpx.Origin has it: the only place the key
	// is sent.
	origin string
	key    string
}

// New returns a Source using client. baseURL is DefaultBaseURL except in
// tests. key is the user's Wago API key, which every request needs.
func New(client *http.Client, baseURL, key string) *Source {
	baseURL = strings.TrimSuffix(baseURL, "/")
	s := &Source{client: client, baseURL: baseURL, key: key}
	if u, err := url.Parse(baseURL); err == nil {
		s.origin = httpx.Origin(u)
	}
	return s
}

// Name implements source.Source.
func (s *Source) Name() string { return ref.Wago }

// gameVersions are Wago's names for the game each flavor runs. Classic is
// the _classic_ client's current line, Mists of Pandaria; Wago's "classic"
// is Classic Era.
var gameVersions = map[flavor.Flavor]string{
	flavor.Retail:      "retail",
	flavor.Classic:     "mop",
	flavor.ClassicEra:  "classic",
	flavor.Anniversary: "bc",
	flavor.Forever:     "forever",
}

// addon is the part of Wago's addon details Knapsack reads.
type addon struct {
	// RecentRelease maps a stability (stable, beta, alpha) to the newest
	// release of it for the game version asked for. Wago sends [] rather
	// than {} when there are none, so it's decoded by releases.
	RecentRelease json.RawMessage `json:"recent_release"`
}

type release struct {
	Label        string `json:"label"`
	DownloadLink string `json:"download_link"`
}

// stable is the only stability Knapsack installs, as it installs only
// GitHub's latest release, never a pre-release.
const stable = "stable"

// maxResponse bounds an API response. Details include the description and
// changelogs, but are still well under this.
const maxResponse = 4 << 20

// Resolve implements source.Source. id is the addon's Wago ID or the slug
// in its addons.wago.io link.
func (s *Source) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	gv, ok := gameVersions[f]
	if !ok {
		return source.Release{}, fmt.Errorf("wago %s: %w: Wago has no game version for %s", id, source.ErrNoCompatibleRelease, f)
	}
	a, err := s.details(ctx, id, gv)
	if err != nil {
		return source.Release{}, err
	}
	rels, err := releases(a.RecentRelease)
	if err != nil {
		return source.Release{}, fmt.Errorf("wago %s: decode releases: %w", id, err)
	}
	rel, ok := rels[stable]
	switch {
	case !ok:
		return source.Release{}, fmt.Errorf("wago %s: %w: %s", id, source.ErrNoCompatibleRelease, noStable(rels, f))
	case rel.DownloadLink == "":
		return source.Release{}, fmt.Errorf("wago %s %s: %w: the stable release for %s has no download link", id, rel.Label, source.ErrNoCompatibleRelease, f)
	}
	out := source.Release{Version: rel.Label, DownloadURL: rel.DownloadLink}
	// The download needs the key too, but only Wago's own API may see it,
	// should a link ever point somewhere else. httpx.Download drops it on
	// a redirect anywhere else, too.
	if u, err := url.Parse(rel.DownloadLink); err == nil && s.origin != "" && httpx.Origin(u) == s.origin {
		out.Header = s.authHeader()
	}
	return out, nil
}

// noStable says why there's no stable release: there are only others, or
// none at all.
func noStable(rels map[string]release, f flavor.Flavor) string {
	others := slices.Sorted(maps.Keys(rels))
	if len(others) == 0 {
		return fmt.Sprintf("no release for %s", f)
	}
	return fmt.Sprintf("no stable release for %s, only %s", f, strings.Join(others, ", "))
}

// releases decodes recent_release: an object of releases by stability, or
// an empty array or null for none.
func releases(raw json.RawMessage) (map[string]release, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || bytes.Equal(trimmed, []byte("[]")) {
		return nil, nil
	}
	var rels map[string]release
	err := json.Unmarshal(trimmed, &rels)
	return rels, err
}

// details fetches the addon id's details for the game version gv.
func (s *Source) details(ctx context.Context, id, gv string) (addon, error) {
	if s.key == "" {
		return addon{}, fmt.Errorf("wago %s: no API key: %w", id, source.ErrSourceNotConfigured)
	}
	endpoint := fmt.Sprintf("%s/addons/%s?%s", s.baseURL, url.PathEscape(id), url.Values{"game_version": {gv}}.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return addon{}, fmt.Errorf("wago %s: %w", id, err)
	}
	req.Header = s.authHeader()
	req.Header.Set("Accept", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return addon{}, fmt.Errorf("wago %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	if err := checkStatus(resp); err != nil {
		return addon{}, fmt.Errorf("wago %s: %w", id, err)
	}
	body, err := source.ReadBody(resp.Body, maxResponse)
	if err != nil {
		return addon{}, fmt.Errorf("wago %s: API response: %w", id, err)
	}
	var a addon
	if err := json.Unmarshal(body, &a); err != nil {
		return addon{}, fmt.Errorf("wago %s: decode addon: %w", id, err)
	}
	return a, nil
}

func (s *Source) authHeader() http.Header {
	return http.Header{"Authorization": {"Bearer " + s.key}}
}

// checkStatus turns an unsuccessful response into a source error.
func checkStatus(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("no such addon: %w", source.ErrNotFound)
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("the API key was rejected: %w", source.ErrUnauthorized)
	case http.StatusTooManyRequests:
		return fmt.Errorf("API rate limit reached: %w", source.ErrRateLimited)
	}
	return fmt.Errorf("API answered %s", resp.Status)
}
