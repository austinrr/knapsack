package github

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// fakeGitHub serves testdata/<repo>.json for /repos/<owner>/<repo>/releases/latest,
// or calls h when it's set. It records the last request.
type fakeGitHub struct {
	h    http.HandlerFunc
	last atomic.Pointer[http.Request] // set on the server's goroutine
}

func (f *fakeGitHub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.last.Store(r)
	if f.h != nil {
		f.h(w, r)
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(parts) != 5 || parts[0] != "repos" || parts[3] != "releases" || parts[4] != "latest" {
		http.NotFound(w, r)
		return
	}
	data, err := os.ReadFile(filepath.Join("testdata", parts[2]+".json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}

func newSource(t *testing.T, token string, h http.HandlerFunc) (*Source, *fakeGitHub) {
	t.Helper()
	fake := &fakeGitHub{h: h}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return New(srv.Client(), srv.URL, token), fake
}

func TestResolveRealReleases(t *testing.T) {
	t.Parallel()
	s, _ := newSource(t, "", nil)
	tests := []struct {
		id     string
		flavor flavor.Flavor
		want   source.Release
	}{
		{
			// One unmarked zip, plus the packager's release.json.
			id:     "austinrr/baganator-mirror",
			flavor: flavor.Forever,
			want: source.Release{
				Version:     "854",
				DownloadURL: "https://github.com/austinrr/baganator-mirror/releases/download/854/Baganator-854.zip",
				SHA256:      "29b37eae631a7ad40c74693312e60716732fe2136306b2d3a85c343368433c9b",
			},
		},
		{
			// A zip marked -forever.
			id:     "austinrr/leatrix-maps-forever-mirror",
			flavor: flavor.Forever,
			want: source.Release{
				Version:     "1.60.31",
				DownloadURL: "https://github.com/austinrr/leatrix-maps-forever-mirror/releases/download/1.60.31/Leatrix_Maps-1.60.31-forever.zip",
				SHA256:      "3f4a7e5d1519a2b99b8baa9dcddc559a2fb1e29f806621451cc1bb7255ac935a",
			},
		},
	}
	for _, tt := range tests {
		got, err := s.Resolve(context.Background(), tt.id, tt.flavor)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		if diff := cmp.Diff(tt.want, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.id, diff)
		}
	}
}

// One case per asset-selection rule, on a packager-style release with a
// zip per client plus -nolib variants.
func TestResolvePackaged(t *testing.T) {
	t.Parallel()
	s, _ := newSource(t, "", nil)
	tests := []struct {
		flavor flavor.Flavor
		want   string
	}{
		{flavor.Forever, "Addon-v1-forever.zip"},
		{flavor.ClassicEra, "Addon-v1-classic.zip"}, // "classic" is Era, not the _classic_ client
		{flavor.Anniversary, "Addon-v1-bcc.zip"},
		{flavor.Classic, "Addon-v1-mists.zip"},
		{flavor.Retail, "Addon-v1.zip"}, // no -mainline zip, so the unmarked one
	}
	for _, tt := range tests {
		got, err := s.Resolve(context.Background(), "o/packaged", tt.flavor)
		if err != nil {
			t.Fatalf("%s: %v", tt.flavor, err)
		}
		if name := filepath.Base(got.DownloadURL); name != tt.want {
			t.Errorf("%s: chose %s, want %s", tt.flavor, name, tt.want)
		}
	}
}

func TestChooseAsset(t *testing.T) {
	t.Parallel()
	zips := func(names ...string) []asset {
		out := make([]asset, len(names))
		for i, n := range names {
			out[i] = asset{Name: n}
		}
		return out
	}
	tests := []struct {
		name    string
		assets  []asset
		flavor  flavor.Flavor
		want    string // "" for ErrNoCompatibleRelease
		wantMsg string
	}{
		{"marker beats unmarked", zips("A-1.zip", "A-1-forever.zip"), flavor.Forever, "A-1-forever.zip", ""},
		{"underscore marker", zips("A_forever.zip"), flavor.Forever, "A_forever.zip", ""},
		{"marker case", zips("A-1-Forever.ZIP"), flavor.Forever, "A-1-Forever.ZIP", ""},
		{"mainline marker for retail", zips("A-1-mainline.zip", "A-1-classic.zip"), flavor.Retail, "A-1-mainline.zip", ""},
		{"marker only at the end", zips("Classic_Castbars-1.0.zip"), flavor.Forever, "Classic_Castbars-1.0.zip", ""},
		{"nolib skipped", zips("A-1-nolib.zip", "A-1.zip"), flavor.Forever, "A-1.zip", ""},
		{"non-zips ignored", zips("release.json", "A-1.tar.gz", "A-1.zip"), flavor.Forever, "A-1.zip", ""},
		{"only other clients", zips("A-1-classic.zip", "A-1-wrath.zip"), flavor.Forever, "", "no .zip asset for forever among A-1-classic.zip, A-1-wrath.zip"},
		{"several unmarked", zips("A-1.zip", "B-1.zip"), flavor.Forever, "", "several assets could be for forever: A-1.zip, B-1.zip"},
		{"several marked", zips("A-forever.zip", "B-forever.zip"), flavor.Forever, "", "several assets"},
		{"only nolib", zips("A-1-nolib.zip"), flavor.Forever, "", "no .zip asset"},
		{"no assets", nil, flavor.Forever, "", "the release has no assets"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := chooseAsset(tt.assets, tt.flavor)
			if tt.want == "" {
				if !errors.Is(err, source.ErrNoCompatibleRelease) || !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("error = %v, want ErrNoCompatibleRelease containing %q", err, tt.wantMsg)
				}
				return
			}
			if err != nil || got.Name != tt.want {
				t.Errorf("chose %q, %v; want %q", got.Name, err, tt.want)
			}
		})
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	respond := func(status int, headers map[string]string, body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			for k, v := range headers {
				w.Header().Set(k, v)
			}
			w.WriteHeader(status)
			_, _ = w.Write([]byte(body))
		}
	}
	tests := []struct {
		name    string
		token   string
		h       http.HandlerFunc
		wantErr error
		wantMsg string
	}{
		{"not found", "", respond(404, nil, `{"message":"Not Found"}`), source.ErrNotFound, "github o/r: no such repository, or it has no releases"},
		{"bad token", "t", respond(401, nil, `{"message":"Bad credentials"}`), source.ErrUnauthorized, "rejected the token"},
		{"rate limited", "", respond(403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1763057045"}, ""), source.ErrRateLimited, "a token raises the limit"},
		{"rate limited with token", "t", respond(403, map[string]string{"X-RateLimit-Remaining": "0"}, ""), source.ErrRateLimited, "rate limit reached: rate limited"},
		{"secondary rate limit", "", respond(403, map[string]string{"Retry-After": "60"}, ""), source.ErrRateLimited, ""},
		{"429", "", respond(429, nil, ""), source.ErrRateLimited, ""},
		{"forbidden", "", respond(403, map[string]string{"X-RateLimit-Remaining": "42"}, ""), nil, "403 Forbidden"},
		{"bad json", "", respond(200, nil, "<html>"), nil, "decode release"},
		{"bad digest", "", respond(200, nil, `{"tag_name":"v1","assets":[{"name":"A.zip","digest":"md5:abc"}]}`), nil, `unsupported digest "md5:abc"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newSource(t, tt.token, tt.h)
			_, err := s.Resolve(context.Background(), "o/r", flavor.Forever)
			if err == nil {
				t.Fatal("Resolve succeeded")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("error = %v, want %v", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}

// Assets uploaded before GitHub recorded digests have none; the release is
// still usable, with an empty SHA256.
func TestResolveMissingDigest(t *testing.T) {
	t.Parallel()
	s, _ := newSource(t, "", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(release{TagName: "v1", Assets: []asset{{Name: "A-1.zip", BrowserDownloadURL: "https://example.invalid/A-1.zip"}}})
	})
	got, err := s.Resolve(context.Background(), "o/r", flavor.Forever)
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != "" || got.DownloadURL != "https://example.invalid/A-1.zip" {
		t.Errorf("Resolve = %+v, want the asset with no SHA256", got)
	}
}

// The request uses the API's versioned JSON media type, and sends the
// token only when there is one.
func TestResolveRequest(t *testing.T) {
	t.Parallel()
	for _, token := range []string{"", "secret"} {
		s, fake := newSource(t, token, nil)
		if _, err := s.Resolve(context.Background(), "austinrr/baganator-mirror", flavor.Forever); err != nil {
			t.Fatal(err)
		}
		r := fake.last.Load()
		if r.URL.Path != "/repos/austinrr/baganator-mirror/releases/latest" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q", got)
		}
		want := ""
		if token != "" {
			want = "Bearer " + token
		}
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("token %q: Authorization = %q, want %q", token, got, want)
		}
	}
}

func TestResolveBadID(t *testing.T) {
	t.Parallel()
	s, _ := newSource(t, "", nil)
	for _, id := range []string{"noslash", "/repo", "owner/", "a/b/c"} {
		if _, err := s.Resolve(context.Background(), id, flavor.Forever); err == nil {
			t.Errorf("Resolve(%q) succeeded", id)
		}
	}
}

func TestName(t *testing.T) {
	t.Parallel()
	if got := New(http.DefaultClient, DefaultBaseURL, "").Name(); got != ref.GitHub {
		t.Errorf("Name = %q, want %q, the source name in refs", got, ref.GitHub)
	}
}
