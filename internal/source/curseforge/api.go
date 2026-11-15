package curseforge

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// DefaultBaseURL is CurseForge's official API.
const DefaultBaseURL = "https://api.curseforge.com"

// Source resolves CurseForge releases through the official API, which
// needs an API key. It's safe for concurrent use.
type Source struct {
	client  *http.Client
	baseURL string
	key     string
}

// New returns a Source using client. baseURL is DefaultBaseURL except in
// tests. key is the user's CurseForge API key, which every request needs.
func New(client *http.Client, baseURL, key string) *Source {
	return &Source{client: client, baseURL: strings.TrimSuffix(baseURL, "/"), key: key}
}

// Name implements source.Source.
func (s *Source) Name() string { return ref.CurseForge }

// apiFile is the part of the API's File that Knapsack reads.
type apiFile struct {
	ID          int       `json:"id"`
	DisplayName string    `json:"displayName"`
	ReleaseType int       `json:"releaseType"`
	IsAvailable bool      `json:"isAvailable"`
	FileDate    time.Time `json:"fileDate"`
	// DownloadURL is null when the author doesn't allow other apps to
	// download the project.
	DownloadURL string `json:"downloadUrl"`
}

// Resolve implements source.Source. id is the project's numeric ID or its
// slug, which is looked up first.
func (s *Source) Resolve(ctx context.Context, id string, f flavor.Flavor) (source.Release, error) {
	gvt, ok := gameVersionTypes[f]
	if !ok {
		return source.Release{}, fmt.Errorf("curseforge %s: %w: CurseForge has no game version for %s", id, source.ErrNoCompatibleRelease, f)
	}
	if s.key == "" {
		return source.Release{}, fmt.Errorf("curseforge %s: no API key: %w", id, source.ErrSourceNotConfigured)
	}
	project := id
	if !isID(id) {
		var err error
		if project, err = s.projectID(ctx, id); err != nil {
			return source.Release{}, fmt.Errorf("curseforge %s: %w", id, err)
		}
	}
	// The API doesn't say how it orders files, so the newest release is
	// chosen by date. Pages are read until one has a release, as a project
	// can publish many alphas and betas between them.
	var newest *apiFile
	for page := 0; page < maxPages && newest == nil; page++ {
		var files struct {
			Data []apiFile `json:"data"`
		}
		q := url.Values{"gameVersionTypeId": {strconv.Itoa(gvt)}, "index": {strconv.Itoa(page * filesPerPage)}, "pageSize": {strconv.Itoa(filesPerPage)}}
		if err := s.get(ctx, "/v1/mods/"+project+"/files?"+q.Encode(), &files); err != nil {
			return source.Release{}, fmt.Errorf("curseforge %s: %w", id, err)
		}
		for i, file := range files.Data {
			if file.ReleaseType == releaseFile && file.IsAvailable && (newest == nil || file.FileDate.After(newest.FileDate)) {
				newest = &files.Data[i]
			}
		}
		if len(files.Data) < filesPerPage {
			break
		}
	}
	switch {
	case newest == nil:
		return source.Release{}, fmt.Errorf("curseforge %s: %w: no release file for %s", id, source.ErrNoCompatibleRelease, f)
	case newest.DownloadURL == "":
		return source.Release{}, fmt.Errorf("curseforge %s: %w", id, source.ErrNotDistributed)
	}
	// CurseForge's CDN needs no key, so none is sent with the download.
	return source.Release{Version: newest.DisplayName, DownloadURL: newest.DownloadURL}, nil
}

// ID implements source.Identifier: a slug's project ID, found by search,
// so config keeps the ID, which either way of reading CurseForge can use.
func (s *Source) ID(ctx context.Context, id string) (string, error) {
	if isID(id) {
		return id, nil
	}
	if s.key == "" {
		return "", fmt.Errorf("curseforge %s: no API key: %w", id, source.ErrSourceNotConfigured)
	}
	project, err := s.projectID(ctx, id)
	if err != nil {
		return "", fmt.Errorf("curseforge %s: %w", id, err)
	}
	return project, nil
}

// projectID looks up the numeric ID of the project with slug.
func (s *Source) projectID(ctx context.Context, slug string) (string, error) {
	var found struct {
		Data []struct {
			ID   int    `json:"id"`
			Slug string `json:"slug"`
		} `json:"data"`
	}
	q := url.Values{"gameId": {strconv.Itoa(wowGameID)}, "slug": {slug}}
	if err := s.get(ctx, "/v1/mods/search?"+q.Encode(), &found); err != nil {
		return "", err
	}
	for _, m := range found.Data {
		if strings.EqualFold(m.Slug, slug) {
			return strconv.Itoa(m.ID), nil
		}
	}
	return "", fmt.Errorf("no such project: %w", source.ErrNotFound)
}

// get fetches path from the API with the key and decodes its JSON into v.
func (s *Source) get(ctx context.Context, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", s.key)
	req.Header.Set("Accept", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // read-only; nothing to flush
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return fmt.Errorf("no such project: %w", source.ErrNotFound)
	case http.StatusUnauthorized, http.StatusForbidden:
		// The docs don't say which a bad key gets; both mean the key.
		return fmt.Errorf("the API key was rejected: %w", source.ErrUnauthorized)
	case http.StatusTooManyRequests:
		return fmt.Errorf("API rate limit reached: %w", source.ErrRateLimited)
	default:
		return fmt.Errorf("API answered %s", resp.Status)
	}
	if err := readJSON(resp, v); err != nil {
		return fmt.Errorf("decode API response: %w", err)
	}
	return nil
}
