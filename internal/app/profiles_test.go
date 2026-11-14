package app_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/austinrr/knapsack/internal/app"
	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/paths"
	"github.com/austinrr/knapsack/internal/ref"
	"github.com/austinrr/knapsack/internal/state"
)

// newApp returns an App on a temp KNAPSACK_HOME, its config store, and the
// path of a _forever_ AddOns folder inside it.
func newApp(t *testing.T) (*app.App, *config.Store, string) {
	t.Helper()
	home := t.TempDir()
	p, err := paths.Resolve(func(string) string { return home })
	if err != nil {
		t.Fatal(err)
	}
	addons := filepath.Join(home, "wow", "_forever_", "Interface", "AddOns")
	if err := os.MkdirAll(addons, 0o700); err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(p.ConfigFile)
	return app.New(app.Options{Paths: p, Config: store, State: state.NewStore(p.StateFile)}), store, addons
}

// Changing a profile's path keeps its addons and flavor.
func TestSetProfileModifyKeepsAddons(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	ctx := context.Background()
	if _, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons}); err != nil {
		t.Fatal(err)
	}

	// Record an addon the way `add` will.
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	r, err := ref.Parse("github:austinrr/baganator-mirror")
	if err != nil {
		t.Fatal(err)
	}
	p := c.Profiles["forever"]
	p.Addons = []ref.Ref{r}
	c.Profiles["forever"] = p
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}

	moved := filepath.Join(filepath.Dir(addons), "AddOns-moved")
	if err := os.Mkdir(moved, 0o700); err != nil {
		t.Fatal(err)
	}
	res, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: moved})
	if err != nil {
		t.Fatal(err)
	}
	want := app.ProfileInfo{Name: "forever", Path: moved, Flavor: flavor.Forever, Addons: []ref.Ref{r}, Current: true}
	if diff := cmp.Diff(want, res.Profile); diff != "" {
		t.Errorf("profile after modify (-want +got):\n%s", diff)
	}
	if res.Created || res.MadeCurrent {
		t.Errorf("modify reported Created=%v MadeCurrent=%v, want both false", res.Created, res.MadeCurrent)
	}
}

// Only the first profile becomes current; deleting the current one leaves
// none current.
func TestCurrentProfileLifecycle(t *testing.T) {
	t.Parallel()
	a, _, addons := newApp(t)
	ctx := context.Background()

	first, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "one", Path: addons})
	if err != nil {
		t.Fatal(err)
	}
	other := t.TempDir()
	second, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "two", Path: other, Flavor: "retail"})
	if err != nil {
		t.Fatal(err)
	}
	if !first.MadeCurrent || second.MadeCurrent {
		t.Errorf("MadeCurrent = %v, %v; want true, false", first.MadeCurrent, second.MadeCurrent)
	}

	res, err := a.DeleteProfile(ctx, "one")
	if err != nil {
		t.Fatal(err)
	}
	if !res.WasCurrent {
		t.Error("deleting the current profile: WasCurrent = false")
	}
	var ue *app.UserError
	if _, err := a.CurrentProfile(ctx); !errors.As(err, &ue) || ue.Msg != "no current profile" {
		t.Errorf("CurrentProfile after delete = %v, want the no-current-profile UserError", err)
	}
}

// A cancelled context stops a use case before it does anything.
func TestCancelledContext(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons}); !errors.Is(err, context.Canceled) {
		t.Errorf("SetProfile error = %v, want context.Canceled", err)
	}
	if _, err := os.Stat(store.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config was written despite the cancelled context: %v", err)
	}
}

// ViewConfig redacts keys and never changes the file.
func TestViewConfigRedacts(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	c := config.New()
	c.Profiles["forever"] = config.Profile{Path: addons, Flavor: flavor.Forever}
	c.Sources.Wago.APIKey = "secret"
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	view, err := a.ViewConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := view.Sources.Wago.APIKey; got != "REDACTED" {
		t.Errorf("viewed key = %q, want REDACTED", got)
	}
	again, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Sources.Wago.APIKey; got != "secret" {
		t.Errorf("stored key = %q after view, want it unchanged", got)
	}
}

// A change that changes nothing doesn't rewrite config, so comments the
// user wrote survive.
func TestNoOpKeepsComments(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	ctx := context.Background()
	if _, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	withComment := append([]byte("# my notes\n"), data...)
	if err := os.WriteFile(store.Path(), withComment, 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons})
	if err != nil {
		t.Fatal(err)
	}
	if res.Changed {
		t.Error("SetProfile with the same settings reported Changed")
	}
	if err := a.UseProfile(ctx, "forever"); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(withComment) {
		t.Errorf("no-op commands rewrote config:\n%s", after)
	}
}

// Deleting a profile deletes its state file too.
func TestDeleteProfileRemovesState(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	ctx := context.Background()
	if _, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons}); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(filepath.Dir(store.Path()), "state", "forever.toml")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("version = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteProfile(ctx, "forever"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(statePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state file still exists after DeleteProfile: %v", err)
	}
}

// If the state file can't be deleted, the profile stays in config, so the
// command can be retried and no stale state is left for a new profile of
// the same name.
func TestDeleteProfileStateFailure(t *testing.T) {
	t.Parallel()
	a, store, addons := newApp(t)
	ctx := context.Background()
	if _, err := a.SetProfile(ctx, app.SetProfileRequest{Name: "forever", Path: addons}); err != nil {
		t.Fatal(err)
	}
	// A non-empty directory where the state file goes can't be removed.
	statePath := filepath.Join(filepath.Dir(store.Path()), "state", "forever.toml")
	if err := os.MkdirAll(filepath.Join(statePath, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := a.DeleteProfile(ctx, "forever"); err == nil {
		t.Fatal("DeleteProfile succeeded although the state file couldn't be deleted")
	}
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Profiles["forever"]; !ok {
		t.Error("profile was removed from config although its state wasn't")
	}
}
