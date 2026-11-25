package curseforge

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/source"
)

// fake answers each path, without its query, with routes' status and
// body; other paths are 404. It records every request.
type fake struct {
	routes map[string]route
	mu     sync.Mutex
	reqs   []*http.Request
}

type route struct {
	status int
	body   string
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r)
	f.mu.Unlock()
	rt, ok := f.routes[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if rt.status != 0 {
		w.WriteHeader(rt.status)
	}
	_, _ = w.Write([]byte(rt.body))
}

func (f *fake) requests() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reqs
}

func serve(t *testing.T, routes map[string]route) (*httptest.Server, *fake) {
	t.Helper()
	f := &fake{routes: routes}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return srv, f
}

// widgetSyndicator is CFWidget's answer for Syndicator, trimmed.
const widgetSyndicator = `{"id":988370,"game":"wow","type":"Addons","title":"Syndicator",
 "urls":{"curseforge":"https://www.curseforge.com/wow/addons/syndicator"},"download":{"id":9511150,"name":"Syndicator-290.zip"}}`

// siteFiles is the website's listing of Syndicator's files, newest first:
// an alpha, a release for retail only, then one for every flavor.
const siteFiles = `{"data":[
 {"id":9511152,"displayName":"291-alpha","fileName":"Syndicator-291-alpha.zip","releaseType":3,"gameVersionTypeIds":[517,88568]},
 {"id":9511151,"displayName":"290-retail","fileName":"Syndicator-290-retail.zip","releaseType":1,"gameVersionTypeIds":[517]},
 {"id":9511150,"displayName":"290","fileName":"Syndicator-290.zip","releaseType":1,"status":4,
  "gameVersions":["1.60.1","12.1.5"],"gameVersionTypeIds":[81212,79434,517,73246,67408,88568],"isCompatibleWithClient":true}
],"pagination":{"index":0,"pageSize":50,"totalCount":3}}`

func TestWebsiteResolve(t *testing.T) {
	t.Parallel()
	srv, f := serve(t, map[string]route{
		"/widget/wow/addons/syndicator": {body: widgetSyndicator},
		"/api/v1/mods/988370/files":     {body: siteFiles},
	})
	w := NewWebsite(srv.Client(), srv.URL+"/", srv.URL+"/widget/")
	for _, tt := range []struct {
		id      string
		f       flavor.Flavor
		version string
		file    string
	}{
		{"syndicator", flavor.Forever, "290", "9511150"},
		{"988370", flavor.Retail, "290-retail", "9511151"}, // nothing to look up
	} {
		got, err := w.Resolve(context.Background(), tt.id, tt.f)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		want := source.Release{Version: tt.version, DownloadURL: srv.URL + "/api/v1/mods/988370/files/" + tt.file + "/download"}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("%s (-want +got):\n%s", tt.id, diff)
		}
	}
	reqs := f.requests()
	if len(reqs) != 3 {
		t.Fatalf("made %d requests, want a lookup and files, then files alone", len(reqs))
	}
	q := reqs[1].URL.Query()
	if q.Get("sort") != "dateCreated" || q.Get("sortDescending") != "true" || q.Get("pageIndex") != "0" {
		t.Errorf("files query = %v, want the newest first", q)
	}
	for _, r := range reqs {
		if r.Header.Get("x-api-key") != "" {
			t.Errorf("%s sent a key", r.URL)
		}
	}
}

// Files are paged until a release for the flavor turns up, or a short page
// ends them.
func TestWebsitePages(t *testing.T) {
	t.Parallel()
	full := `{"data":[` + strings.Repeat(`{"id":1,"displayName":"a","releaseType":3,"gameVersionTypeIds":[517]},`, filesPerPage-1) +
		`{"id":1,"displayName":"a","releaseType":3,"gameVersionTypeIds":[517]}]}`
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("pageIndex") == "1" {
			_, _ = w.Write([]byte(`{"data":[{"id":7,"displayName":"1.0","releaseType":1,"gameVersionTypeIds":[517]}]}`))
			return
		}
		_, _ = w.Write([]byte(full))
	}))
	defer srv.Close()
	got, err := NewWebsite(srv.Client(), srv.URL, "").Resolve(context.Background(), "1", flavor.Retail)
	if err != nil || got.Version != "1.0" || calls != 2 {
		t.Errorf("Resolve = %+v, %v after %d pages; want 1.0 from the second", got, err, calls)
	}
}

// CFWidget's answer lists every file and game version a project has had,
// megabytes for a long-lived addon: the ID is read from its start, and the
// rest left unread, however big it is.
func TestWebsiteBigLookup(t *testing.T) {
	t.Parallel()
	files := `[` + strings.Repeat(`{"id":1,"name":"Syndicator-1.zip"},`, maxWidgetResponse/30) + `{}]`
	for _, tt := range []struct {
		name, body string
		want       error
	}{
		{"id first", `{"id":988370,"files":` + files + `}`, nil},
		{"id later", `{"title":"Syndicator","files":[{"id":1}],"id":988370}`, nil},
		{"id after too much", `{"files":` + files + `,"id":988370}`, source.ErrTooLarge},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, map[string]route{"/widget/wow/addons/syndicator": {body: tt.body}})
			got, err := NewWebsite(srv.Client(), srv.URL, srv.URL+"/widget").ID(context.Background(), "syndicator")
			if tt.want != nil {
				if !errors.Is(err, tt.want) || !errors.Is(err, source.ErrUnexpectedWebsite) {
					t.Errorf("ID = %q, %v; want %v", got, err, tt.want)
				}
				return
			}
			if err != nil || got != "988370" {
				t.Errorf("ID = %q, %v; want 988370", got, err)
			}
		})
	}
}

func TestWebsiteResolveErrors(t *testing.T) {
	t.Parallel()
	lookup := func(r route) map[string]route {
		return map[string]route{"/widget/wow/addons/syndicator": r, "/api/v1/mods/988370/files": {body: siteFiles}}
	}
	files := func(r route) map[string]route {
		return map[string]route{"/widget/wow/addons/syndicator": {body: widgetSyndicator}, "/api/v1/mods/988370/files": r}
	}
	tests := []struct {
		name   string
		routes map[string]route
		want   error
		msg    string
	}{
		{"no such slug", lookup(route{status: http.StatusNotFound}), source.ErrNotFound, "no such addon"},
		{"lookup pending", lookup(route{status: http.StatusAccepted, body: `{"error":"in_queue"}`}), nil, "retry in a minute"},
		{"lookup changed shape", lookup(route{body: `{"project":{"id":988370}}`}), source.ErrUnexpectedWebsite, "has no project ID"},
		{"lookup not JSON", lookup(route{body: `<html>`}), source.ErrUnexpectedWebsite, "decode"},
		{"lookup not an object", lookup(route{body: `[988370]`}), source.ErrUnexpectedWebsite, "not a JSON object"},
		{"lookup bad id", lookup(route{body: `{"id":"988370"}`}), source.ErrUnexpectedWebsite, "decode"},
		{"blocked", files(route{status: http.StatusForbidden, body: "_cf-lb_"}), source.ErrUnexpectedWebsite, "answered 403 Forbidden"},
		{"outage", files(route{status: http.StatusServiceUnavailable}), nil, "answered 503"},
		{"rate limited", files(route{status: http.StatusTooManyRequests}), source.ErrRateLimited, "rate limit"},
		{"no such project", files(route{body: `{"data":[],"pagination":{}}`}), source.ErrNotFound, "no such project, or it has no files"},
		{"files changed shape", files(route{body: `{"files":[]}`}), source.ErrUnexpectedWebsite, "has no data"},
		{"files not JSON", files(route{body: `<html>`}), source.ErrUnexpectedWebsite, "decode"},
		{"file without an id", files(route{body: `{"data":[{"releaseType":1,"gameVersionTypeIds":[517]}]}`}), source.ErrUnexpectedWebsite, "no id or name"},
		{"only betas", files(route{body: `{"data":[{"id":1,"displayName":"b","releaseType":2,"gameVersionTypeIds":[517]}]}`}),
			source.ErrNoCompatibleRelease, "no release file for retail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, tt.routes)
			_, err := NewWebsite(srv.Client(), srv.URL, srv.URL+"/widget").Resolve(context.Background(), "syndicator", flavor.Retail)
			switch {
			case err == nil:
				t.Fatal("no error")
			case tt.want != nil && !errors.Is(err, tt.want):
				t.Errorf("err = %v, want %v", err, tt.want)
			case tt.want == nil && errors.Is(err, source.ErrUnexpectedWebsite):
				t.Errorf("err = %v, want it not to ask for a report", err)
			}
			if !strings.HasPrefix(err.Error(), "curseforge syndicator: ") || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to name the addon and say %q", err, tt.msg)
			}
		})
	}
}

// apiFiles is the official API's list of a project's files for a game
// version type, in no promised order.
const apiFiles = `{"data":[
 {"id":3,"displayName":"291-beta","releaseType":2,"isAvailable":true,"fileDate":"2026-11-10T00:00:00Z","downloadUrl":"https://edge.forgecdn.net/files/3/s-291.zip"},
 {"id":1,"displayName":"289","releaseType":1,"isAvailable":true,"fileDate":"2026-10-01T00:00:00Z","downloadUrl":"https://edge.forgecdn.net/files/1/s-289.zip"},
 {"id":2,"displayName":"290","releaseType":1,"isAvailable":true,"fileDate":"2026-11-08T00:00:00Z","downloadUrl":"https://edge.forgecdn.net/files/2/s-290.zip",
  "hashes":[{"value":"abc","algo":1}],"gameVersions":["1.60.1"]},
 {"id":4,"displayName":"292","releaseType":1,"isAvailable":false,"fileDate":"2026-11-12T00:00:00Z","downloadUrl":"https://edge.forgecdn.net/files/4/s-292.zip"}
],"pagination":{"index":0,"pageSize":50,"resultCount":4,"totalCount":4}}`

func TestAPIResolve(t *testing.T) {
	t.Parallel()
	srv, f := serve(t, map[string]route{
		"/v1/mods/search":       {body: `{"data":[{"id":5,"slug":"syndicator-classic"},{"id":988370,"slug":"syndicator"}]}`},
		"/v1/mods/988370/files": {body: apiFiles},
	})
	got, err := New(srv.Client(), srv.URL+"/", "secret").Resolve(context.Background(), "syndicator", flavor.Forever)
	if err != nil {
		t.Fatal(err)
	}
	// The newest available release, wherever it's listed.
	want := source.Release{Version: "290", DownloadURL: "https://edge.forgecdn.net/files/2/s-290.zip"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Resolve (-want +got):\n%s", diff)
	}
	reqs := f.requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want a search, then files", len(reqs))
	}
	if q := reqs[0].URL.Query(); q.Get("gameId") != "1" || q.Get("slug") != "syndicator" {
		t.Errorf("search query = %v", q)
	}
	if q := reqs[1].URL.Query(); q.Get("gameVersionTypeId") != "88568" {
		t.Errorf("files query = %v, want Forever's game version type", q)
	}
	for _, r := range reqs {
		if r.Header.Get("x-api-key") != "secret" {
			t.Errorf("%s: x-api-key = %q", r.URL, r.Header.Get("x-api-key"))
		}
	}
}

func TestAPIResolveErrors(t *testing.T) {
	t.Parallel()
	files := func(r route) map[string]route { return map[string]route{"/v1/mods/42/files": r} }
	tests := []struct {
		name   string
		id     string
		key    string
		routes map[string]route
		want   error
		msg    string
	}{
		{"no key", "42", "", nil, source.ErrSourceNotConfigured, "no API key"},
		{"no such slug", "nope", "k", map[string]route{"/v1/mods/search": {body: `{"data":[{"id":1,"slug":"nope-classic"}]}`}}, source.ErrNotFound, "no such project"},
		{"no such ID", "42", "k", nil, source.ErrNotFound, "no such project"},
		{"bad key", "42", "k", files(route{status: http.StatusForbidden}), source.ErrUnauthorized, "rejected"},
		{"rate limited", "42", "k", files(route{status: http.StatusTooManyRequests}), source.ErrRateLimited, "rate limit"},
		{"server error", "42", "k", files(route{status: http.StatusBadGateway}), nil, "502"},
		{"bad JSON", "42", "k", files(route{body: `{"data": "soon"}`}), nil, "decode"},
		{"only betas", "42", "k", files(route{body: `{"data":[{"id":1,"releaseType":2,"isAvailable":true,"downloadUrl":"x"}]}`}),
			source.ErrNoCompatibleRelease, "no release file for retail"},
		{"not distributed", "42", "k", files(route{body: `{"data":[{"id":1,"displayName":"1","releaseType":1,"isAvailable":true,"downloadUrl":null}]}`}),
			source.ErrNotDistributed, "doesn't allow other apps"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := serve(t, tt.routes)
			_, err := New(srv.Client(), srv.URL, tt.key).Resolve(context.Background(), tt.id, flavor.Retail)
			if err == nil {
				t.Fatal("no error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
			if !strings.HasPrefix(err.Error(), "curseforge "+tt.id+": ") || !strings.Contains(err.Error(), tt.msg) {
				t.Errorf("err = %v, want it to name the addon and say %q", err, tt.msg)
			}
		})
	}
}

// ID looks a slug's project ID up once, through either way of reading
// CurseForge, and leaves an ID alone.
func TestID(t *testing.T) {
	t.Parallel()
	srv, f := serve(t, map[string]route{
		"/widget/wow/addons/syndicator": {body: widgetSyndicator},
		"/v1/mods/search":               {body: `{"data":[{"id":988370,"slug":"syndicator"}]}`},
	})
	for name, idr := range map[string]source.Identifier{
		"website": NewWebsite(srv.Client(), srv.URL, srv.URL+"/widget"),
		"api":     New(srv.Client(), srv.URL, "k"),
	} {
		if got, err := idr.ID(context.Background(), "syndicator"); err != nil || got != "988370" {
			t.Errorf("%s: ID(syndicator) = %q, %v", name, got, err)
		}
		if got, err := idr.ID(context.Background(), "42"); err != nil || got != "42" {
			t.Errorf("%s: ID(42) = %q, %v", name, got, err)
		}
		if _, err := idr.ID(context.Background(), "nope"); !errors.Is(err, source.ErrNotFound) {
			t.Errorf("%s: ID(nope) = %v, want not found", name, err)
		}
	}
	if n := len(f.requests()); n != 4 {
		t.Errorf("made %d requests, want a lookup per slug, none for an ID", n)
	}
	if _, err := New(srv.Client(), srv.URL, "").ID(context.Background(), "syndicator"); !errors.Is(err, source.ErrSourceNotConfigured) {
		t.Errorf("ID without a key = %v", err)
	}
}

// The official API is paged too, until a page has a release, with each
// page's index where the last ended.
func TestAPIPages(t *testing.T) {
	t.Parallel()
	alpha := `{"id":1,"displayName":"a","releaseType":3,"isAvailable":true,"downloadUrl":"x"}`
	full := `{"data":[` + strings.TrimSuffix(strings.Repeat(alpha+",", filesPerPage), ",") + `]}`
	var indexes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		indexes = append(indexes, r.URL.Query().Get("index"))
		if r.URL.Query().Get("index") == "50" {
			_, _ = w.Write([]byte(`{"data":[{"id":7,"displayName":"1.0","releaseType":1,"isAvailable":true,"downloadUrl":"https://cdn/x.zip"}]}`))
			return
		}
		_, _ = w.Write([]byte(full))
	}))
	defer srv.Close()
	got, err := New(srv.Client(), srv.URL, "k").Resolve(context.Background(), "1", flavor.Retail)
	if err != nil || got.Version != "1.0" || !cmp.Equal(indexes, []string{"0", "50"}) {
		t.Errorf("Resolve = %+v, %v after pages %v; want 1.0 from index 50", got, err, indexes)
	}
}

// Only CFWidget's lookup takes a 202, for a project it's still fetching;
// from CurseForge it's unexpected.
func TestWebsiteAccepted(t *testing.T) {
	t.Parallel()
	srv, _ := serve(t, map[string]route{"/api/v1/mods/1/files": {status: http.StatusAccepted, body: siteFiles}})
	if _, err := NewWebsite(srv.Client(), srv.URL, srv.URL).Resolve(context.Background(), "1", flavor.Retail); !errors.Is(err, source.ErrUnexpectedWebsite) {
		t.Errorf("err = %v, want a 202 from CurseForge to be unexpected", err)
	}
}

// Every flavor has a game version type, and each is its own.
func TestGameVersionTypes(t *testing.T) {
	t.Parallel()
	seen := map[int]bool{}
	for _, f := range flavor.All() {
		id, ok := gameVersionTypes[f]
		if !ok || seen[id] {
			t.Errorf("%s: game version type %d, ok %v", f, id, ok)
		}
		seen[id] = true
	}
}
