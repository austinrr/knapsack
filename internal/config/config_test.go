package config_test

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"

	"github.com/austinrr/knapsack/internal/config"
	"github.com/austinrr/knapsack/internal/flavor"
	"github.com/austinrr/knapsack/internal/ref"
)

var update = flag.Bool("update", false, "rewrite golden files")

// root is an absolute path on the OS running the tests. Golden files spell
// it <root> so they're the same everywhere.
func root() string {
	if runtime.GOOS == "windows" {
		return "C:/games"
	}
	return "/games"
}

func mustRef(t *testing.T, s string) ref.Ref {
	t.Helper()
	r, err := ref.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// sample is a config using every feature.
func sample(t *testing.T) *config.Config {
	t.Helper()
	c := config.New()
	c.CurrentProfile = "forever"
	c.Profiles["forever"] = config.Profile{
		Path:   root() + "/wow/_forever_/Interface/AddOns",
		Flavor: flavor.Forever,
		Addons: []ref.Ref{
			mustRef(t, "github:austinrr/baganator-mirror"),
			mustRef(t, "github:austinrr/syndicator-mirror"),
			mustRef(t, "wago:VBNBxKx5"),
		},
	}
	c.Profiles["retail"] = config.Profile{
		Path:   root() + "/World of Warcraft/_retail_/Interface/AddOns",
		Flavor: flavor.Retail,
		Addons: []ref.Ref{mustRef(t, "curseforge:12345")},
	}
	c.Profiles["classic"] = config.Profile{
		Path:   root() + "/wow/_classic_/Interface/AddOns",
		Flavor: flavor.Classic,
	}
	c.Sources.CurseForge.APIKey = `key-with-"quotes"\and\backslashes`
	return c
}

func writeFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	contents = strings.ReplaceAll(contents, "<root>", root())
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The written file matches testdata/config.golden byte for byte.
func TestSaveGolden(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := config.NewStore(path).Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(string(data), root(), "<root>")

	golden := filepath.Join("testdata", "config.golden")
	if *update {
		//nolint:gosec // G703: golden is the constant testdata/config.golden
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden file (run with -update to create it): %v", err)
	}
	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("saved config differs from %s (-want +got):\n%s", golden, diff)
	}
}

func TestRoundTrip(t *testing.T) {
	t.Parallel()
	tests := map[string]*config.Config{
		"sample": sample(t),
		"empty":  config.New(),
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			store := config.NewStore(filepath.Join(t.TempDir(), "config.toml"))
			if err := store.Save(want); err != nil {
				t.Fatal(err)
			}
			got, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if diff := cmp.Diff(want, got, cmpopts.EquateEmpty()); diff != "" {
				t.Errorf("round trip mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// Saving what was loaded gives the same bytes, so a no-op command never
// produces a diff.
func TestSaveIsStable(t *testing.T) {
	t.Parallel()
	store := config.NewStore(filepath.Join(t.TempDir(), "config.toml"))
	if err := store.Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(c); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(string(first), string(second)); diff != "" {
		t.Errorf("second save differs (-first +second):\n%s", diff)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Parallel()
	c, err := config.NewStore(filepath.Join(t.TempDir(), "config.toml")).Load()
	if err != nil {
		t.Fatal(err)
	}
	if diff := cmp.Diff(config.New(), c); diff != "" {
		t.Errorf("Load of a missing file (-want +got):\n%s", diff)
	}
}

// Hand-written refs in any accepted form are normalized on load.
func TestLoadNormalizesRefs(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `
[profiles.forever]
path = "<root>/AddOns"
flavor = "forever"
addons = ["GitHub:AustinRR/Baganator-Mirror", "https://github.com/owner/repo.git"]
`)
	c, err := config.NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	want := []ref.Ref{mustRef(t, "github:austinrr/baganator-mirror"), mustRef(t, "github:owner/repo")}
	if diff := cmp.Diff(want, c.Profiles["forever"].Addons); diff != "" {
		t.Errorf("addons (-want +got):\n%s", diff)
	}
}

// Every way Load can reject a file, each with the text that explains it.
func TestLoadErrors(t *testing.T) {
	t.Parallel()
	const ok = "path = \"<root>/AddOns\"\nflavor = \"forever\"\n"
	tests := []struct {
		name    string
		file    string
		wantErr []string // substrings, all of which must appear
	}{
		{
			name:    "syntax error",
			file:    "current_profile = \n",
			wantErr: []string{"config.toml", "line 1"},
		},
		{
			name:    "unknown keys",
			file:    "[profiles.forever]\n" + ok + "flavour = \"retail\"\n[sources.wago]\napi-key = \"x\"\n",
			wantErr: []string{"unknown keys: profiles.forever.flavour, sources.wago.api-key"},
		},
		{
			// A mistyped table is named once, not with every key inside it.
			name:    "unknown table",
			file:    "[profile.forever]\n" + ok,
			wantErr: []string{"unknown keys: profile.forever\n"},
		},
		{
			name:    "unknown source table",
			file:    "[sources.github]\napi_key = \"x\"\n",
			wantErr: []string{"unknown source table [sources.github] (sources with settings: curseforge, wago)"},
		},
		{
			name: "same path twice",
			file: "[profiles.a]\npath = \"<root>/wow/AddOns\"\nflavor = \"forever\"\n" +
				"[profiles.b]\npath = \"<root>/WoW/AddOns/\"\nflavor = \"forever\"\n",
			wantErr: []string{`profiles "a" and "b" have the same path`},
		},
		{
			name:    "wrong type",
			file:    "[profiles.forever]\n" + ok + "addons = \"github:a/b\"\n",
			wantErr: []string{"config.toml", "addons"},
		},
		{
			name:    "uppercase profile name",
			file:    "[profiles.Forever]\n" + ok,
			wantErr: []string{`invalid profile name "Forever"`},
		},
		{
			name:    "profile name with a dot",
			file:    "[profiles.\"for.ever\"]\n" + ok,
			wantErr: []string{`invalid profile name "for.ever"`},
		},
		{
			name:    "empty path",
			file:    "[profiles.forever]\nflavor = \"forever\"\n",
			wantErr: []string{`profile "forever": path is empty`},
		},
		{
			name:    "relative path",
			file:    "[profiles.forever]\npath = \"wow/AddOns\"\nflavor = \"forever\"\n",
			wantErr: []string{`profile "forever": path "wow/AddOns" is not absolute`},
		},
		{
			name:    "empty flavor",
			file:    "[profiles.forever]\npath = \"<root>/AddOns\"\n",
			wantErr: []string{`profile "forever": flavor is empty`},
		},
		{
			name:    "unknown flavor",
			file:    "[profiles.forever]\npath = \"<root>/AddOns\"\nflavor = \"classic_beta\"\n",
			wantErr: []string{`profile "forever": unknown flavor "classic_beta"`},
		},
		{
			name:    "invalid ref",
			file:    "[profiles.forever]\n" + ok + "addons = [\"owner/repo\"]\n",
			wantErr: []string{`profile "forever": invalid addon ref "owner/repo"`},
		},
		{
			name:    "duplicate addon after normalization",
			file:    "[profiles.forever]\n" + ok + "addons = [\"github:a/b\", \"https://github.com/A/B\"]\n",
			wantErr: []string{`profile "forever": addon "github:a/b" is listed twice`},
		},
		{
			name:    "missing current profile",
			file:    "current_profile = \"retail\"\n[profiles.forever]\n" + ok,
			wantErr: []string{`current_profile "retail" is not a profile`},
		},
		{
			// A bad ref doesn't hide the rest.
			name: "every problem is reported",
			file: "current_profile = \"nope\"\n" +
				"[profiles.a]\npath = \"rel\"\nflavor = \"x\"\naddons = [\"bad\"]\n" +
				"[profiles.b]\nflavor = \"forever\"\n",
			wantErr: []string{
				`profile "a": invalid addon ref "bad"`,
				`profile "a": path "rel" is not absolute`,
				`profile "a": unknown flavor "x"`,
				`profile "b": path is empty`,
				`current_profile "nope" is not a profile`,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.NewStore(writeFile(t, tt.file)).Load()
			if err == nil {
				t.Fatal("Load succeeded, want error")
			}
			for _, want := range tt.wantErr {
				if !strings.Contains(err.Error()+"\n", want) {
					t.Errorf("error %q\ndoesn't contain %q", err, want)
				}
			}
		})
	}
}

// Save refuses an invalid config and leaves the existing file alone.
func TestSaveRejectsInvalid(t *testing.T) {
	t.Parallel()
	store := config.NewStore(filepath.Join(t.TempDir(), "config.toml"))
	if err := store.Save(sample(t)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}

	bad := sample(t)
	bad.CurrentProfile = "missing"
	if err := store.Save(bad); err == nil {
		t.Fatal("Save of an invalid config succeeded")
	}
	after, err := os.ReadFile(store.Path())
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Error("a rejected Save changed the file")
	}
}

func TestValidateProfileName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"forever", "retail-2", "my_classic", "a", strings.Repeat("a", 64)} {
		if err := config.ValidateProfileName(name); err != nil {
			t.Errorf("ValidateProfileName(%q): %v", name, err)
		}
	}
	for _, name := range []string{"", "Forever", "for ever", "for.ever", "../x", `a\b`, strings.Repeat("a", 65)} {
		if err := config.ValidateProfileName(name); err == nil {
			t.Errorf("ValidateProfileName(%q) succeeded, want error", name)
		}
	}
}

// A config with an API key is readable only by the user.
func TestSavePerm(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("no Unix permissions on Windows")
	}
	noKey := sample(t)
	noKey.Sources = config.Sources{}
	tests := []struct {
		name string
		c    *config.Config
		want os.FileMode
	}{
		{"with key", sample(t), 0o600},
		{"without key", noKey, 0o644},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "sub", "config.toml") // Save creates sub/
			if err := config.NewStore(path).Save(tt.c); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode().Perm(); got != tt.want {
				t.Errorf("mode = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestAPIKey(t *testing.T) {
	t.Parallel()
	c := config.New()
	c.Sources.CurseForge.APIKey = "from-config"
	c.Sources.Wago.APIKey = "wago-config"
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}

	tests := []struct {
		name   string
		source string
		env    map[string]string
		want   string
	}{
		{"config only", ref.CurseForge, nil, "from-config"},
		{"env wins", ref.CurseForge, map[string]string{config.CurseForgeKeyEnv: "from-env"}, "from-env"},
		{"empty env ignored", ref.CurseForge, map[string]string{config.CurseForgeKeyEnv: ""}, "from-config"},
		{"wago env", ref.Wago, map[string]string{config.WagoKeyEnv: "wago-env"}, "wago-env"},
		{"wago config", ref.Wago, nil, "wago-config"},
		{"other source's env", ref.Wago, map[string]string{config.CurseForgeKeyEnv: "x"}, "wago-config"},
		{"github has none", ref.GitHub, nil, ""},
	}
	for _, tt := range tests {
		if got := c.APIKey(tt.source, env(tt.env)); got != tt.want {
			t.Errorf("%s: APIKey(%q) = %q, want %q", tt.name, tt.source, got, tt.want)
		}
	}
}

// Profiles from both OSes can share one config, as in a dotfiles repo, and
// it loads on either.
func TestLoadMixedOSPaths(t *testing.T) {
	t.Parallel()
	path := writeFile(t, `
[profiles.linux]
path = "/games/wow/_forever_/Interface/AddOns"
flavor = "forever"

[profiles.windows]
path = "C:/Program Files (x86)/World of Warcraft/_retail_/Interface/AddOns"
flavor = "retail"

[profiles.backslashes]
path = 'D:\Games\WoW\_classic_\Interface\AddOns'
flavor = "classic"

[profiles.unc]
path = '\\nas\wow\_classic_era_\Interface\AddOns'
flavor = "classic_era"
`)
	c, err := config.NewStore(path).Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Profiles) != 4 {
		t.Errorf("loaded %d profiles, want 4", len(c.Profiles))
	}
}

// Save refuses what Load couldn't read back the same way.
func TestSaveValidation(t *testing.T) {
	t.Parallel()
	base := func() *config.Config {
		c := config.New()
		c.Profiles["forever"] = config.Profile{Path: root() + "/AddOns", Flavor: flavor.Forever}
		return c
	}
	tests := []struct {
		name    string
		change  func(*config.Config)
		wantErr string
	}{
		{"unnormalized ref", func(c *config.Config) {
			p := c.Profiles["forever"]
			p.Addons = []ref.Ref{{Source: ref.GitHub, ID: "Owner/Repo"}}
			c.Profiles["forever"] = p
		}, `addon "github:Owner/Repo" is not a valid, normalized ref`},
		{"zero ref", func(c *config.Config) {
			p := c.Profiles["forever"]
			p.Addons = []ref.Ref{{}}
			c.Profiles["forever"] = p
		}, `addon ":" is not a valid, normalized ref`},
		{"non-UTF-8 path", func(c *config.Config) {
			p := c.Profiles["forever"]
			p.Path = root() + "/caf\xe9/AddOns"
			c.Profiles["forever"] = p
		}, "is not valid UTF-8"},
		{"non-UTF-8 key", func(c *config.Config) {
			c.Sources.Wago.APIKey = "\xff"
		}, "sources.wago.api_key is not valid UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := base()
			tt.change(c)
			err := config.NewStore(filepath.Join(t.TempDir(), "config.toml")).Save(c)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("Save error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
