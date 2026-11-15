package source_test

import (
	"context"
	"errors"
	"testing"

	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/source"
)

// fakeSource resolves every ID to a release named after it.
type fakeSource struct{ name string }

func (f fakeSource) Name() string { return f.name }

func (f fakeSource) Resolve(_ context.Context, id string, fl flavor.Flavor) (source.Release, error) {
	return source.Release{Version: f.name + ":" + id + "@" + string(fl)}, nil
}

// The registry routes refs by source name, and a source that isn't
// registered is ErrSourceNotConfigured.
func TestRegistry(t *testing.T) {
	t.Parallel()
	reg := source.NewRegistry(fakeSource{ref.GitHub})
	gh, err := ref.Parse("github:austinrr/baganator-mirror")
	if err != nil {
		t.Fatal(err)
	}
	rel, err := reg.Resolve(context.Background(), gh, flavor.Forever)
	if err != nil || rel.Version != "github:austinrr/baganator-mirror@forever" {
		t.Errorf("Resolve(%s) = %+v, %v", gh, rel, err)
	}
	wago, err := ref.Parse("wago:VBNBxKx5")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Resolve(context.Background(), wago, flavor.Forever); !errors.Is(err, source.ErrSourceNotConfigured) {
		t.Errorf("Resolve(%s) error = %v, want ErrSourceNotConfigured", wago, err)
	}
}

// identifying is a source with its own IDs for addons: "slug" is 42.
type identifying struct{ fakeSource }

func (identifying) ID(_ context.Context, id string) (string, error) {
	switch id {
	case "slug":
		return "42", nil
	case "bad":
		return "not an id!", nil
	}
	return "", errors.New("lookup failed")
}

// The registry keeps an Identifier's ID for an addon, and other sources'
// refs as they are.
func TestRegistryID(t *testing.T) {
	t.Parallel()
	reg := source.NewRegistry(fakeSource{name: ref.GitHub}, identifying{fakeSource{name: ref.CurseForge}})
	for _, tt := range []struct{ in, want string }{
		{"github:o/r", "github:o/r"},
		{"curseforge:slug", "curseforge:42"},
	} {
		r, err := ref.Parse(tt.in)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := reg.ID(context.Background(), r); err != nil || got.String() != tt.want {
			t.Errorf("ID(%s) = %s, %v; want %s", tt.in, got, err, tt.want)
		}
	}
	for _, id := range []string{"curseforge:other", "curseforge:bad"} {
		r, _ := ref.Parse(id)
		if _, err := reg.ID(context.Background(), r); err == nil {
			t.Errorf("ID(%s) succeeded, want the lookup's error or an invalid ID", id)
		}
	}
}
