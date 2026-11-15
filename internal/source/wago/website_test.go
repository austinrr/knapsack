package wago

import (
	"context"
	"errors"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/source"
)

// addonHTML is an addon's page as Wago's website draws it: the page's
// data, HTML-escaped, in the app element's data-page attribute.
func addonHTML(data string) string {
	return `<!DOCTYPE html><html><head><title>Plater</title></head>` +
		`<body><div id="app" data-page="` + html.EscapeString(data) + `"></div></body></html>`
}

// platerPage is Plater's page data, trimmed from Wago's, with fields
// Knapsack doesn't read left in.
const platerPage = `{
  "component": "Addon/AddonDetails",
  "props": {
    "errors": {},
    "user": null,
    "addon": {"id": "kRNLep6o", "slug": "plater-nameplates", "display_name": "Plater-Nameplates"},
    "metadata": {
      "owner": "Terciob",
      "recent_releases": {
        "retail": {
          "label": "Plater-v664",
          "changelog": "Fixes \"quoted\" & <tagged>",
          "download_link": "$SERVER/download/xdY5M2kd?expires=1794754813&link=retail",
          "supported_retail_patch": "12.1.7"
        },
        "forever": {
          "label": "Plater-v663",
          "download_link": "$SERVER/download/xdY5M2kd?expires=1794754813&link=forever"
        }
      }
    }
  },
  "url": "/addons/plater-nameplates",
  "version": "a2950e6c11d113780f98b8d2588dcfe7"
}`

// newWebsite returns a Website on a fake Wago site serving body, and the
// fake.
func newWebsite(t *testing.T, status int, body string) (*Website, *fakeWago) {
	t.Helper()
	fake := &fakeWago{status: status, body: body}
	srv := httptest.NewTLSServer(fake)
	t.Cleanup(srv.Close)
	return NewWebsite(srv.Client(), srv.URL+"/"), fake
}

func TestWebsiteResolve(t *testing.T) {
	t.Parallel()
	w, fake := newWebsite(t, 0, addonHTML(platerPage))
	got, err := w.Resolve(context.Background(), "plater-nameplates", flavor.Forever)
	if err != nil {
		t.Fatal(err)
	}
	req := fake.last.Load()
	if req.URL.Path != "/addons/plater-nameplates" {
		t.Errorf("path = %q", req.URL.Path)
	}
	if auth := req.Header.Get("Authorization"); auth != "" {
		t.Errorf("Authorization = %q, want none", auth)
	}
	// The link is signed, so it needs no key either.
	want := source.Release{
		Version:     "Plater-v663",
		DownloadURL: "https://" + req.Host + "/download/xdY5M2kd?expires=1794754813&link=forever",
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve (-want +got):\n%s", diff)
	}
	if w.Name() != "wago" {
		t.Errorf("Name = %q", w.Name())
	}
}

// Anything the website answers that isn't an addon page as Knapsack
// knows it is ErrUnexpectedWebsite, saying what was wrong, so the error
// can be reported.
func TestWebsiteResolveErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		status int
		body   string
		want   error
		msg    string
	}{
		{name: "not found", status: http.StatusNotFound, want: source.ErrNotFound, msg: "no such addon"},
		{name: "rate limited", status: http.StatusTooManyRequests, want: source.ErrRateLimited},
		{name: "blocked", status: http.StatusForbidden, want: source.ErrUnexpectedWebsite, msg: "answered 403 Forbidden"},
		{name: "server error", status: http.StatusBadGateway, want: source.ErrUnexpectedWebsite, msg: "502"},
		{name: "no data", body: "<html><body>Just a moment...</body></html>", want: source.ErrUnexpectedWebsite, msg: "no data-page attribute"},
		{name: "bad JSON", body: addonHTML(`{"component": `), want: source.ErrUnexpectedWebsite, msg: "decode"},
		{name: "another page", body: addonHTML(`{"component": "Auth/Login", "props": {}}`), want: source.ErrUnexpectedWebsite,
			msg: `a "Auth/Login" page, not "Addon/AddonDetails"`},
		{name: "releases changed shape", body: addonHTML(`{"component": "Addon/AddonDetails", "props": {"metadata": {"recent_releases": "soon"}}}`),
			want: source.ErrUnexpectedWebsite, msg: "decode"},
		{name: "null releases", body: addonHTML(`{"component": "Addon/AddonDetails", "props": {"metadata": {"recent_releases": null}}}`),
			want: source.ErrNoCompatibleRelease, msg: "no stable release for retail"},
		{name: "no releases", body: addonHTML(`{"component": "Addon/AddonDetails", "props": {"metadata": {"recent_releases": []}}}`),
			want: source.ErrNoCompatibleRelease, msg: "no stable release for retail"},
		{name: "not for the flavor", body: addonHTML(strings.Replace(platerPage, `"retail"`, `"mop"`, 1)),
			want: source.ErrNoCompatibleRelease, msg: "no stable release for retail"},
		{name: "no link", body: addonHTML(`{"component": "Addon/AddonDetails", "props": {"metadata": {"recent_releases": {"retail": {"label": "1"}}}}}`),
			want: source.ErrUnexpectedWebsite, msg: "no version or download link"},
		{name: "too large", body: addonHTML(`{"x": "` + strings.Repeat("x", maxResponse) + `"}`), want: source.ErrUnexpectedWebsite, msg: "over 4 MiB"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			w, _ := newWebsite(t, tt.status, tt.body)
			_, err := w.Resolve(context.Background(), "plater", flavor.Retail)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
			if !strings.Contains(err.Error(), tt.msg) || !strings.HasPrefix(err.Error(), "wago plater: ") {
				t.Errorf("err = %v, want it to name the addon and say %q", err, tt.msg)
			}
		})
	}
}

// An ID is one path element, whatever it holds.
func TestWebsiteEscapesID(t *testing.T) {
	t.Parallel()
	w, fake := newWebsite(t, 0, addonHTML(platerPage))
	if _, err := w.Resolve(context.Background(), "a/b?c", flavor.Retail); err != nil {
		t.Fatal(err)
	}
	if got := fake.last.Load().URL.EscapedPath(); got != "/addons/a%2Fb%3Fc" {
		t.Errorf("path = %q", got)
	}
}
