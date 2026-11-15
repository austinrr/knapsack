package curseforge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// DefaultSiteURL is CurseForge's website.
const DefaultSiteURL = "https://www.curseforge.com"

// DefaultWidgetURL is CFWidget's API, which a slug's project ID is looked
// up on.
const DefaultWidgetURL = "https://api.cfwidget.com"

// Website resolves CurseForge releases from the JSON API CurseForge's
// website lists and downloads files with, which needs no key, by project
// ID (DESIGN.md §7). It isn't an official API: when it changes, Resolve
// fails with source.ErrUnexpectedWebsite. It's safe for concurrent use.
type Website struct {
	client    *http.Client
	siteURL   string
	widgetURL string
}

// NewWebsite returns a Website using client. siteURL and widgetURL are
// DefaultSiteURL and DefaultWidgetURL except in tests.
func NewWebsite(client *http.Client, siteURL, widgetURL string) *Website {
	return &Website{client: client, siteURL: strings.TrimSuffix(siteURL, "/"), widgetURL: strings.TrimSuffix(widgetURL, "/")}
}

// Name implements source.Source.
func (w *Website) Name() string { return ref.CurseForge }

// siteFile is the part of a file in the website's listing Knapsack reads.
type siteFile struct {
	ID                 int    `json:"id"`
	DisplayName        string `json:"displayName"`
	ReleaseType        int    `json:"releaseType"`
	GameVersionTypeIDs []int  `json:"gameVersionTypeIds"`
}

// Files are listed newest first, a page at a time. A project's release
// for a flavor is normally among its newest few files, but one that
// publishes alphas often, or a file per flavor, can have many above it.
const (
	filesPerPage = 50
	maxPages     = 4
)

// Resolve implements source.Source. id is the project's numeric ID, as
// ID stores it, or a slug written in config by hand, which is looked up
// each time.
func (w *Website) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	gvt, ok := gameVersionTypes[f]
	if !ok {
		return source.Release{}, fmt.Errorf("curseforge %s: %w: CurseForge has no game version for %s", id, source.ErrNoCompatibleRelease, f)
	}
	project := id
	if !isID(id) {
		var err error
		if project, err = w.projectID(ctx, id); err != nil {
			return source.Release{}, fmt.Errorf("curseforge %s: %w", id, err)
		}
	}
	for page := range maxPages {
		files, err := w.files(ctx, project, page)
		if err != nil {
			return source.Release{}, fmt.Errorf("curseforge %s: %w", id, err)
		}
		if page == 0 && len(files) == 0 {
			// The website answers an unknown project with no files.
			return source.Release{}, fmt.Errorf("curseforge %s: no such project, or it has no files: %w", id, source.ErrNotFound)
		}
		for _, file := range files {
			if file.ReleaseType == releaseFile && slices.Contains(file.GameVersionTypeIDs, gvt) {
				if file.ID == 0 || file.DisplayName == "" {
					return source.Release{}, fmt.Errorf("curseforge %s: %w: a file has no id or name", id, source.ErrUnexpectedWebsite)
				}
				return source.Release{
					Version:     file.DisplayName,
					DownloadURL: fmt.Sprintf("%s/api/v1/mods/%s/files/%d/download", w.siteURL, project, file.ID),
				}, nil
			}
		}
		if len(files) < filesPerPage {
			break
		}
	}
	return source.Release{}, fmt.Errorf("curseforge %s: %w: no release file for %s among the newest %d", id, source.ErrNoCompatibleRelease, f, maxPages*filesPerPage)
}

// files fetches a page of the project's files, newest first.
func (w *Website) files(ctx context.Context, project string, page int) ([]siteFile, error) {
	q := url.Values{
		"pageIndex": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(filesPerPage)},
		"sort": {"dateCreated"}, "sortDescending": {"true"},
	}
	endpoint := w.siteURL + "/api/v1/mods/" + project + "/files?" + q.Encode()
	resp, err := w.get(ctx, endpoint, "application/json")
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	var listing struct {
		Data *[]siteFile `json:"data"`
	}
	if err := readJSON(resp, &listing); err != nil {
		return nil, fmt.Errorf("%w: decode GET %s: %w", source.ErrUnexpectedWebsite, endpoint, err)
	}
	if listing.Data == nil {
		return nil, fmt.Errorf("%w: GET %s has no data", source.ErrUnexpectedWebsite, endpoint)
	}
	return *listing.Data, nil
}

// ID implements source.Identifier: a slug's project ID, looked up on
// CFWidget, since CurseForge's website can't be asked for it.
func (w *Website) ID(ctx context.Context, id string) (string, error) {
	if isID(id) {
		return id, nil
	}
	project, err := w.projectID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("curseforge %s: %w", id, err)
	}
	return project, nil
}

// projectID looks up slug's project ID on CFWidget
// (https://www.cfwidget.com), a service that mirrors CurseForge's project
// details. CurseForge's own pages and search, which would say, sit behind
// a challenge only a browser can pass, so a slug is looked up there once,
// when it's added, and its ID kept (DESIGN.md §7).
func (w *Website) projectID(ctx context.Context, slug string) (string, error) {
	endpoint := w.widgetURL + "/wow/addons/" + url.PathEscape(slug)
	resp, err := w.get(ctx, endpoint, "application/json")
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	if resp.StatusCode == http.StatusAccepted {
		// CFWidget answers a project it hasn't seen yet once it has it.
		return "", fmt.Errorf("CFWidget is still looking the project up; retry in a minute")
	}
	var project struct {
		ID int `json:"id"`
	}
	if err := readJSON(resp, &project); err != nil {
		return "", fmt.Errorf("%w: decode GET %s: %w", source.ErrUnexpectedWebsite, endpoint, err)
	}
	if project.ID <= 0 {
		return "", fmt.Errorf("%w: GET %s has no project ID", source.ErrUnexpectedWebsite, endpoint)
	}
	return strconv.Itoa(project.ID), nil
}

// get fetches url from the website, turning an unsuccessful answer into
// an error.
func (w *Website) get(ctx context.Context, url, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted {
		return resp, nil // the caller decides
	}
	if err := source.WebsiteStatus(resp, url); err != nil {
		_ = resp.Body.Close()
		return nil, err
	}
	return resp, nil
}
