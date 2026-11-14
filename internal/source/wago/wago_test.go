package wago

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/source"
)

// fakeWago answers /addons/<id> with body, or with status when it isn't
// 200. It records the last request.
type fakeWago struct {
	status int
	body   string
	last   atomic.Pointer[http.Request] // set on the server's goroutine
}

func (f *fakeWago) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.last.Store(r)
	if f.status != 0 && f.status != http.StatusOK {
		w.WriteHeader(f.status)
		return
	}
	// The download link is on the server itself, as Wago's are on its API.
	_, _ = w.Write([]byte(strings.ReplaceAll(f.body, "$SERVER", "https://"+r.Host)))
}

// newSource returns a Source on a fake Wago serving body, and the fake.
func newSource(t *testing.T, key string, status int, body string) (*Source, *fakeWago) {
	t.Helper()
	fake := &fakeWago{status: status, body: body}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	return New(srv.Client(), srv.URL+"/api/external/", key), fake
}

// details is an addon with a stable and a beta release, shaped like Wago's
// answer: other fields Knapsack doesn't read are left in.
const details = `{
  "id": "VBNBxKx5",
  "slug": "details",
  "display_name": "Details! Damage Meter",
  "recent_release": {
    "stable": {
      "label": "Details.20261101.13800.160",
      "supported_retail_patch": "11.2.5",
      "stability": "stable",
      "changelog": "Fixes",
      "created_at": "2026-11-01T12:00:00Z",
      "download_link": "$SERVER/api/external/addons/VBNBxKx5/versions/abc/download"
    },
    "beta": {
      "label": "Details.20261110.13801.160",
      "stability": "beta",
      "download_link": "$SERVER/api/external/addons/VBNBxKx5/versions/def/download"
    }
  }
}`

func TestResolve(t *testing.T) {
	t.Parallel()
	s, fake := newSource(t, "secret", 0, details)
	got, err := s.Resolve(context.Background(), "VBNBxKx5", flavor.Forever)
	if err != nil {
		t.Fatal(err)
	}
	req := fake.last.Load()
	if req.URL.Path != "/api/external/addons/VBNBxKx5" {
		t.Errorf("path = %q", req.URL.Path)
	}
	if gv := req.URL.Query().Get("game_version"); gv != "forever" {
		t.Errorf("game_version = %q, want forever", gv)
	}
	if auth := req.Header.Get("Authorization"); auth != "Bearer secret" {
		t.Errorf("Authorization = %q", auth)
	}
	want := source.Release{
		Version:     "Details.20261101.13800.160",
		DownloadURL: "https://" + req.Host + "/api/external/addons/VBNBxKx5/versions/abc/download",
		Header:      http.Header{"Authorization": {"Bearer secret"}},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve (-want +got):\n%s", diff)
	}
}

func TestGameVersions(t *testing.T) {
	t.Parallel()
	want := map[flavor.Flavor]string{
		flavor.Retail: "retail", flavor.Classic: "mop", flavor.ClassicEra: "classic",
		flavor.Anniversary: "bc", flavor.Forever: "forever",
	}
	for _, f := range flavor.All() {
		s, fake := newSource(t, "k", 0, details)
		if _, err := s.Resolve(context.Background(), "details", f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if gv := fake.last.Load().URL.Query().Get("game_version"); gv != want[f] {
			t.Errorf("%s: game_version = %q, want %q", f, gv, want[f])
		}
	}
}

// The key goes only to Wago's own API: a download link anywhere else gets
// no header, even on a host whose name merely starts the same.
func TestKeyOnlyToWago(t *testing.T) {
	t.Parallel()
	for _, link := range []string{
		"https://cdn.example.com/Details.zip",
		"http://$HOST/api/external/addons/VBNBxKx5/download",
		"https://$HOST.evil.example/Details.zip",
	} {
		body := `{"recent_release": {"stable": {"label": "1", "download_link": "` + link + `"}}}`
		fake := &fakeWago{}
		srv := httptest.NewTLSServer(fake)
		host := strings.TrimPrefix(srv.URL, "https://")
		fake.body = strings.ReplaceAll(body, "$HOST", host)
		s := New(srv.Client(), srv.URL, "secret")
		got, err := s.Resolve(context.Background(), "x", flavor.Retail)
		srv.Close()
		if err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if got.Header != nil {
			t.Errorf("%s: sent the key with the download: %v", link, got.Header)
		}
	}
}

// The same host spelled another way is still Wago's API.
func TestKeyToWagoHowEverSpelled(t *testing.T) {
	t.Parallel()
	fake := &fakeWago{}
	srv := httptest.NewTLSServer(fake)
	defer srv.Close()
	fake.body = `{"recent_release": {"stable": {"label": "1", "download_link": "` +
		strings.ToUpper(srv.URL) + `/download"}}}`
	got, err := New(srv.Client(), srv.URL, "secret").Resolve(context.Background(), "x", flavor.Retail)
	if err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Authorization") != "Bearer secret" {
		t.Errorf("no key for %s: %v", got.DownloadURL, got.Header)
	}
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		key    string
		status int
		body   string
		want   error
		msg    string
	}{
		{name: "no key", want: source.ErrSourceNotConfigured, msg: "no API key"},
		{name: "not found", key: "k", status: http.StatusNotFound, want: source.ErrNotFound},
		{name: "bad key", key: "k", status: http.StatusUnauthorized, want: source.ErrUnauthorized},
		{name: "forbidden", key: "k", status: http.StatusForbidden, want: source.ErrUnauthorized},
		{name: "rate limited", key: "k", status: http.StatusTooManyRequests, want: source.ErrRateLimited},
		{name: "server error", key: "k", status: http.StatusBadGateway, msg: "502"},
		{name: "no releases", key: "k", body: `{"recent_release": []}`, want: source.ErrNoCompatibleRelease, msg: "no release for retail"},
		{name: "null releases", key: "k", body: `{"recent_release": null}`, want: source.ErrNoCompatibleRelease, msg: "no release for retail"},
		{
			name: "only pre-releases", key: "k",
			body: `{"recent_release": {"beta": {"label": "2", "download_link": "x"}, "alpha": {"label": "3", "download_link": "y"}}}`,
			want: source.ErrNoCompatibleRelease, msg: "no stable release for retail, only alpha, beta",
		},
		{name: "no link", key: "k", body: `{"recent_release": {"stable": {"label": "1"}}}`, want: source.ErrNoCompatibleRelease,
			msg: "the stable release for retail has no download link"},
		{name: "too large", key: "k", body: `{"description": "` + strings.Repeat("x", maxResponse) + `"}`, msg: "over 4 MiB"},
		{name: "bad JSON", key: "k", body: `{"recent_release": "soon"}`, msg: "decode releases"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			s, _ := newSource(t, tt.key, tt.status, tt.body)
			_, err := s.Resolve(context.Background(), "details", flavor.Retail)
			if err == nil {
				t.Fatal("no error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to say %q", err, tt.msg)
			}
			if !strings.HasPrefix(err.Error(), "wago details") {
				t.Errorf("err = %v, want it to name the addon", err)
			}
		})
	}
}

// An ID is one path element, whatever it holds.
func TestResolveEscapesID(t *testing.T) {
	t.Parallel()
	s, fake := newSource(t, "k", 0, details)
	if _, err := s.Resolve(context.Background(), "a/b?c", flavor.Retail); err != nil {
		t.Fatal(err)
	}
	if got := fake.last.Load().URL.EscapedPath(); got != "/api/external/addons/a%2Fb%3Fc" {
		t.Errorf("path = %q", got)
	}
}
