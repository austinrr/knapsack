package wago

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// DefaultSiteURL is Wago Addons' website.
const DefaultSiteURL = "https://addons.wago.io"

// Website resolves Wago Addons releases from the addon's page on Wago's
// website, as a browser sees it, so it needs no API key (DESIGN.md §7).
// The page isn't an API: when it changes, Resolve fails with
// source.ErrUnexpectedWebsite. It's safe for concurrent use.
type Website struct {
	client  *http.Client
	siteURL string
}

// NewWebsite returns a Website using client. siteURL is DefaultSiteURL
// except in tests.
func NewWebsite(client *http.Client, siteURL string) *Website {
	return &Website{client: client, siteURL: strings.TrimSuffix(siteURL, "/")}
}

// Name implements source.Source.
func (w *Website) Name() string { return ref.Wago }

// page is the part of an addon page's data Knapsack reads. Wago's pages
// are drawn by Inertia, which puts the page's data, as JSON, in the
// data-page attribute of the app's element.
type page struct {
	Component string `json:"component"`
	Props     struct {
		Metadata struct {
			// RecentReleases maps a game version to its newest stable
			// release. As in the API, it's [] or null for none, so it's
			// decoded by releases.
			RecentReleases json.RawMessage `json:"recent_releases"`
		} `json:"metadata"`
	} `json:"props"`
}

// addonPage is the component that draws an addon's page.
const addonPage = "Addon/AddonDetails"

// dataPage finds Inertia's data-page attribute. Its value is HTML-escaped
// JSON, which can't hold a bare double quote.
var dataPage = regexp.MustCompile(`\sdata-page="([^"]*)"`)

// Resolve implements source.Source. id is the addon's Wago ID or the slug
// in its addons.wago.io link, which the website takes alike.
func (w *Website) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	gv, ok := gameVersions[f]
	if !ok {
		return source.Release{}, fmt.Errorf("wago %s: %w: Wago has no game version for %s", id, source.ErrNoCompatibleRelease, f)
	}
	p, err := w.page(ctx, id)
	if err != nil {
		return source.Release{}, fmt.Errorf("wago %s: %w", id, err)
	}
	rels, err := releases(p.Props.Metadata.RecentReleases)
	if err != nil {
		return source.Release{}, fmt.Errorf("wago %s: %w: decode recent_releases: %w", id, source.ErrUnexpectedWebsite, err)
	}
	rel, ok := rels[gv]
	switch {
	case !ok:
		return source.Release{}, fmt.Errorf("wago %s: %w: no stable release for %s", id, source.ErrNoCompatibleRelease, f)
	case rel.Label == "" || rel.DownloadLink == "":
		return source.Release{}, fmt.Errorf("wago %s: %w: the release for %s has no version or download link", id, source.ErrUnexpectedWebsite, f)
	}
	// The link is signed and works without a key.
	return source.Release{Version: rel.Label, DownloadURL: rel.DownloadLink}, nil
}

// page fetches and decodes the addon's page.
func (w *Website) page(ctx context.Context, id string) (page, error) {
	endpoint := w.siteURL + "/addons/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return page{}, err
	}
	req.Header.Set("Accept", "text/html")
	resp, err := w.client.Do(req)
	if err != nil {
		return page{}, err
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	if err := source.WebsiteStatus(resp, endpoint); err != nil {
		return page{}, err
	}
	body, err := source.ReadBody(resp.Body, maxResponse)
	if errors.Is(err, source.ErrTooLarge) {
		return page{}, fmt.Errorf("%w: the page %w", source.ErrUnexpectedWebsite, err)
	}
	if err != nil {
		return page{}, err
	}
	m := dataPage.FindSubmatch(body)
	if m == nil {
		return page{}, fmt.Errorf("%w: GET %s has no data-page attribute", source.ErrUnexpectedWebsite, endpoint)
	}
	var p page
	if err := json.Unmarshal([]byte(html.UnescapeString(string(m[1]))), &p); err != nil {
		return page{}, fmt.Errorf("%w: decode GET %s's data-page: %w", source.ErrUnexpectedWebsite, endpoint, err)
	}
	if p.Component != addonPage {
		return page{}, fmt.Errorf("%w: GET %s is a %q page, not %q", source.ErrUnexpectedWebsite, endpoint, p.Component, addonPage)
	}
	return p, nil
}
