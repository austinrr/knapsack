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
