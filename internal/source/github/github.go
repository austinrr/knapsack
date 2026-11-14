// Package github resolves addons released on GitHub (DESIGN.md §7): the
// newest release's .zip asset for a flavor, with the digest GitHub
// publishes for it.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/source"
)

// DefaultBaseURL is GitHub's REST API.
const DefaultBaseURL = "https://api.github.com"

// Source resolves GitHub releases. It's safe for concurrent use.
type Source struct {
	client  *http.Client
	baseURL string
	token   string
}

// New returns a Source using client. baseURL is DefaultBaseURL except in
// tests. token is optional: without one GitHub allows 60 requests an hour,
// with one 5,000.
func New(client *http.Client, baseURL, token string) *Source {
	return &Source{client: client, baseURL: strings.TrimSuffix(baseURL, "/"), token: token}
}

// Name implements source.Source.
func (s *Source) Name() string { return "github" }

// release is the part of GitHub's release object Knapsack reads.
type release struct {
	TagName string  `json:"tag_name"`
	Assets  []asset `json:"assets"`
}

type asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	// Digest is "sha256:<hex>". Assets uploaded before GitHub started
	// recording digests in 2025 have none.
	Digest string `json:"digest"`
}

// maxResponse bounds an API response; a release with many assets is still
// well under this.
const maxResponse = 4 << 20

// Resolve implements source.Source. id is "owner/repo".
func (s *Source) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	owner, repo, ok := strings.Cut(id, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return source.Release{}, fmt.Errorf("github %s: want owner/repo", id)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/%s/releases/latest", s.baseURL, url.PathEscape(owner), url.PathEscape(repo))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return source.Release{}, fmt.Errorf("github %s: %w", id, err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return source.Release{}, fmt.Errorf("github %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	if err := checkStatus(resp, s.token != ""); err != nil {
		return source.Release{}, fmt.Errorf("github %s: %w", id, err)
	}

	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponse)).Decode(&rel); err != nil {
		return source.Release{}, fmt.Errorf("github %s: decode release: %w", id, err)
	}
	a, err := chooseAsset(rel.Assets, f)
	if err != nil {
		return source.Release{}, fmt.Errorf("github %s %s: %w", id, rel.TagName, err)
	}
	sum, err := digest(a.Digest)
	if err != nil {
		return source.Release{}, fmt.Errorf("github %s %s: asset %s: %w", id, rel.TagName, a.Name, err)
	}
	return source.Release{Version: rel.TagName, DownloadURL: a.BrowserDownloadURL, SHA256: sum}, nil
}

// checkStatus turns an unsuccessful response into a source error.
func checkStatus(resp *http.Response, withToken bool) error {
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusNotFound:
		// GitHub also answers 404 for a repo with no releases, and for a
		// private repo the token can't see.
		return fmt.Errorf("no such repository, or it has no releases: %w", source.ErrNotFound)
	case resp.StatusCode == http.StatusUnauthorized:
		return fmt.Errorf("GitHub rejected the token: %w", source.ErrUnauthorized)
	case isRateLimited(resp):
		msg := "GitHub API rate limit reached"
		if reset := rateLimitReset(resp); !reset.IsZero() {
			msg += ", resets at " + reset.Format(time.Kitchen)
		}
		if !withToken {
			msg += " (a token raises the limit)"
		}
		return fmt.Errorf("%s: %w", msg, source.ErrRateLimited)
	default:
		return fmt.Errorf("GitHub API: %s", resp.Status)
	}
}

// isRateLimited reports whether a response is GitHub's rate limit: 429, or
// 403 with no requests left or a Retry-After for its secondary limits.
func isRateLimited(resp *http.Response) bool {
	switch resp.StatusCode {
	case http.StatusTooManyRequests:
		return true
	case http.StatusForbidden:
		return resp.Header.Get("X-RateLimit-Remaining") == "0" || resp.Header.Get("Retry-After") != ""
	}
	return false
}

// rateLimitReset returns when the rate limit resets, or the zero time if
// GitHub didn't say.
func rateLimitReset(resp *http.Response) time.Time {
	secs, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(secs, 0)
}

// chooseAsset picks the release asset for f (DESIGN.md §7):
//
//  1. the .zip whose name ends with one of f's markers (-forever, …);
//  2. otherwise the only .zip with no marker for any client.
//
// -nolib zips, which need libraries installed separately, are skipped.
// Anything else, including several candidates, is ErrNoCompatibleRelease,
// listing the assets seen. Source tarballs are never used: GitHub doesn't
// list them as assets, and mixed-TOC source trees are what broke OpenMap.
func chooseAsset(assets []asset, f flavor.Flavor) (asset, error) {
	var matching, unmarked []asset
	for _, a := range assets {
		base, ok := zipBase(a.Name)
		if !ok || isNolib(base) {
			continue
		}
		switch {
		case slices.ContainsFunc(f.AssetMarkers(), func(m string) bool { return hasMarker(base, m) }):
			matching = append(matching, a)
		case !slices.ContainsFunc(flavor.AllAssetMarkers(), func(m string) bool { return hasMarker(base, m) }):
			unmarked = append(unmarked, a)
		}
	}
	for _, candidates := range [][]asset{matching, unmarked} {
		switch len(candidates) {
		case 0:
			continue
		case 1:
			return candidates[0], nil
		default:
			return asset{}, fmt.Errorf("%w: several assets could be for %s: %s",
				source.ErrNoCompatibleRelease, f, names(candidates))
		}
	}
	if len(assets) == 0 {
		return asset{}, fmt.Errorf("%w: the release has no assets", source.ErrNoCompatibleRelease)
	}
	return asset{}, fmt.Errorf("%w: no .zip asset for %s among %s", source.ErrNoCompatibleRelease, f, names(assets))
}

// zipBase returns a .zip asset's name without the extension, lower-cased.
func zipBase(name string) (string, bool) {
	lower := strings.ToLower(name)
	base, ok := strings.CutSuffix(lower, ".zip")
	return base, ok && base != ""
}

// hasMarker reports whether an asset's base name ends with marker after a
// - or _ separator: Addon-1.0-forever, Addon_forever. Only the end counts,
// so an addon named Classic_Castbars isn't mistaken for a classic build.
func hasMarker(base, marker string) bool {
	return strings.HasSuffix(base, "-"+marker) || strings.HasSuffix(base, "_"+marker)
}

// isNolib reports whether an asset is a -nolib build, which needs its
// libraries installed separately. The packager puts the marker before any
// client marker (Addon-1.0-nolib-classic), so it can be anywhere.
func isNolib(base string) bool {
	return strings.Contains(base, "-nolib") || strings.Contains(base, "_nolib")
}

func names(assets []asset) string {
	n := make([]string, len(assets))
	for i, a := range assets {
		n[i] = a.Name
	}
	return strings.Join(n, ", ")
}

// digest turns GitHub's "sha256:<hex>" into lowercase hex, or "" when the
// asset has no digest. Any other algorithm is an error, since nothing else
// could be verified.
func digest(d string) (string, error) {
	if d == "" {
		return "", nil
	}
	hex, ok := strings.CutPrefix(d, "sha256:")
	if !ok || len(hex) != 64 || strings.Trim(strings.ToLower(hex), "0123456789abcdef") != "" {
		return "", fmt.Errorf("unsupported digest %q", d)
	}
	return strings.ToLower(hex), nil
}
