# Knapsack — Design

Status: draft · Last updated: 2026-11-13

Knapsack is a small, cross-platform command-line addon manager for World of
Warcraft. It installs, updates and removes addons for one or more game clients
(Retail, Classic, Classic Era, Anniversary, Forever) from GitHub, CurseForge and Wago.

The guiding principle is **simplicity and refinement over features**. Every
feature has to justify the code, tests and docs it adds.

---

## 1. Goals and non-goals

### Goals

- **Profiles**: one profile per game client, each pointing at an
  `Interface/AddOns` directory.
- **Multiple sources** behind one abstraction: GitHub first, then CurseForge
  and Wago.
- **Bulk operations**: check and update every addon in a profile, or across
  all profiles, with one command.
- **Predictable installs**: an install either fully succeeds or leaves
  `AddOns/` untouched.
- **Clean removal**: Knapsack knows exactly which folders each addon owns.
- **Linux, Windows and macOS** from a single codebase, shipped as single
  static binaries.
- **CLI first**, built so a TUI can be added later without touching the core.
- **Readable state**: config and state are plain TOML files a person can
  inspect and fix by hand.

### Non-goals (for now)

- Managing addons Knapsack didn't install (no "adopt everything in AddOns"
  scan in v1).
- WeakAuras/Plater string syncing, SavedVariables backup, or settings sync.
- A search/browse UI for every source. You install by ID or URL.
- Running as a daemon or auto-updating on a schedule.
- Git clones as an install method. Installs come from release archives
  (zip) only. Cloning caused LemonUp's credential-prompt hang on private repos.

---

## 2. Vocabulary

| Term | Meaning |
|---|---|
| **Profile** | A named target: an AddOns directory plus a flavor. E.g. `forever`. |
| **Flavor** | The game client type, named after the client folder: `retail`, `classic`, `classic_era`, `anniversary`, `forever`. Decides which release files and TOCs are compatible. |
| **Game type** | The client's own name for what it runs, used in TOC suffixes and `AllowLoadGameType`: `standard`, `forever`, `mists`, `vanilla`, … Each flavor maps to one game type and one family (`mainline` or `classic`). |
| **Addon** | One thing the user asked for, identified by a **Ref**. May install several folders. |
| **Ref** | `source:id`, e.g. `github:austinrr/baganator-mirror`, `curseforge:12345`, `wago:VBNBxKx5`. |
| **Source** | A provider that can turn a Ref + flavor into a downloadable release. |
| **Release** | A resolved, downloadable version: version label, download URL, SHA-256. |
| **Owned folders** | The top-level `AddOns/` folders an installed addon created. |

---

## 3. Language and tooling

**Go.** The `go.mod` directive is `go 1.26`, so the two most recent Go
releases are supported; development uses the latest (1.27). Go gives single
static binaries, trivial cross-compilation and a strong standard library, and
it's the language the author is learning.

### Standard library (preferred whenever it's enough)

| Need | Package |
|---|---|
| HTTP | `net/http` |
| Zip extraction | `archive/zip` |
| Checksums | `crypto/sha256` |
| GitHub/CurseForge/Wago JSON | `encoding/json` |
| Config/cache dirs | `os.UserConfigDir`, `os.UserCacheDir` |
| Cancellation, timeouts, Ctrl-C | `context`, `os/signal` |
| Logging | `log/slog` (debug output behind `--verbose`) |
| Tests | `testing`, `net/http/httptest` |

### Third-party dependencies

Kept deliberately short. Each one must earn its place.

| Purpose | Module | Why |
|---|---|---|
| CLI | `github.com/spf13/cobra` | De facto standard for subcommand CLIs; shell completions for free |
| TOML | `github.com/BurntSushi/toml` | Small, stable, good error messages |
| Bounded concurrency | `golang.org/x/sync/errgroup` | Parallel resolve/download with a limit and error propagation |
| File locking | `github.com/gofrs/flock` | Cross-platform advisory locks; the standard library has none |
| Test diffs | `github.com/google/go-cmp` | Readable struct diffs in tests (test-only) |
| TUI (later) | `charmbracelet/bubbletea`, `bubbles`, `lipgloss` | Best Go TUI stack; Elm-style model fits a core/frontend split |

Explicitly **not** used: assertion/mocking frameworks (testify, gomock),
GitHub SDKs (go-github), git libraries, ORMs/SQLite.

### Development tools

| Tool | Use |
|---|---|
| `gofmt` / `goimports` | Formatting (enforced through golangci-lint formatters) |
| `golangci-lint` v2 | Linting (config in §10). Installed as a binary, as its docs recommend |
| `govulncheck` | Dependency vulnerability scan. Pinned as a `tool` dependency in `go.mod` |
| `goreleaser` | Cross-platform release builds. Pinned as a `tool` dependency |
| GitHub Actions | CI on Linux, Windows and macOS |
| `Makefile` | `make lint`, `make test`, `make build`. Thin wrappers only; CI runs the same commands |

---

## 4. Architecture

```
     ┌────────────────┐        ┌────────────────┐
     │  cmd/knapsack  │        │  internal/tui  │  (later; `knapsack tui`)
     │  (cobra CLI)   │        │  (bubbletea)   │
     └───────┬────────┘        └───────┬────────┘
             └────────────┬────────────┘
                          ▼
     ┌──────────────────────────────────────────┐
     │               internal/app               │  use cases: Add, Remove,
     │     (orchestration; no terminal I/O)     │  Update, Check, List, Info
     └──┬────────┬────────┬────────┬────────┬───┘
        ▼        ▼        ▼        ▼        ▼
     ┌──────┐ ┌──────┐ ┌──────┐ ┌───────┐ ┌──────────┐
     │paths │ │config│ │state │ │install│ │  source  │
     └──────┘ └──────┘ └──────┘ └───┬───┘ └────┬─────┘
                                    ▼          ▼
                                 ┌──────┐ ┌────────────────────────┐
                                 │ toc  │ │ github·curseforge·wago │
                                 └──────┘ └───────────┬────────────┘
                                                      ▼
                                                  ┌───────┐
                                                  │ httpx │
                                                  └───────┘
        Leaf packages used throughout: flavor, ref
```

### Rules

1. **Frontends are thin.** `cmd/` and `tui/` parse input, call `app`, and
   render results. They contain no business logic.
2. **`app` never prints or prompts.** It returns values and errors and reports
   progress through a `Reporter` (§6.5). This is what makes the TUI a pure
   addition later.
3. **Import rules** (enforced in review; easy to check with `go list -deps`):

   | Package | May import (internal) |
   |---|---|
   | `flavor`, `ref`, `toc`, `paths` | nothing internal |
   | `httpx` | nothing internal |
   | `config`, `state` | `flavor`, `ref` |
   | `install` | `flavor`, `toc` |
   | `source` | `flavor`, `ref` |
   | `source/*` | `source`, `flavor`, `httpx` |
   | `app` | any of the above |
   | `cmd/knapsack`, `tui` | `app` (plus `flavor`, `ref` for parsing input) |

   Nothing imports `app` except the frontends, and `install` never touches
   the network.
4. **No global state.** No package-level clients, config singletons or
   `init()` side effects. Everything is constructed in `main` and passed down.
5. **Interfaces are defined where they're used**, with one deliberate
   exception: `source.Source`. It's a plugin interface with several
   implementations and a registry next to it, the same reasoning as
   `io.Reader` living in `io`.

### Package layout

```
knapsack/
  cmd/knapsack/          main.go, root.go, one file per command (add.go, update.go, …)
  internal/
    app/                 use cases; wires config, state, sources, installer
    paths/               config/state/cache locations; KNAPSACK_HOME
    config/              profiles + desired addons; Store with Load/Save
    state/               installed releases + owned folders; Store with Load/Save
    flavor/              Flavor type and the client data table (§6.4); reads .build.info
    ref/                 Ref parsing and normalization (refs and URLs)
    source/              Source interface, Release type, Registry, sentinel errors
      github/
      curseforge/
      wago/
    install/             verify → extract → validate → swap (filesystem only)
    toc/                 .toc parsing
    httpx/               shared HTTP client (timeouts, User-Agent) and Download
    tui/                 later
  testdata/              shared fixtures (sample zips, TOCs, API responses)
  docs/
```

Everything lives under `internal/` because Knapsack is an application, not a
library. Packages can be refactored freely without breaking any public API.
Package names are nouns describing what they provide. There are no `util`,
`common` or `helpers` packages.

---

## 5. Data model

Like Cargo.toml and Cargo.lock, **config is what you want, state is what you
have.** Keeping them apart means a corrupt or deleted state file never loses
the user's intent, and the config stays clean enough to edit by hand or keep
in dotfiles.

The layout follows the precedent of other addon managers. WowUp, Ajour,
instawow and LemonUp all keep their data in the per-user application
directory, and none writes a state file into the game's AddOns folder.
Knapsack does the same, so its files are where users already expect them.

### 5.1 Directories

| Purpose | Location | Linux | Windows | macOS |
|---|---|---|---|---|
| Config + state | `os.UserConfigDir()/knapsack` | `~/.config/knapsack` | `%AppData%\knapsack` | `~/Library/Application Support/knapsack` |
| Cache | `os.UserCacheDir()/knapsack` | `~/.cache/knapsack` | `%LocalAppData%\knapsack` | `~/Library/Caches/knapsack` |

```
<config>/knapsack/
  config.toml           # profiles, addon refs, preferences, API keys
  state/
    forever.toml        # what's installed in each profile
    forever.lock        # held while a command runs against this profile
    retail.toml
<cache>/knapsack/       # downloads in progress; later, HTTP ETags; always safe to delete
```

- Both locations come from the Go standard library, so there's no
  path-handling dependency and no per-OS code.
- State lives under the config directory, as WowUp, Ajour and instawow do. A
  separate XDG state directory would be more correct on Linux, but Go has no
  built-in for it and users would have to look in two places.
- **`KNAPSACK_HOME`** overrides both locations (`$KNAPSACK_HOME/config`,
  `$KNAPSACK_HOME/cache`). Copied from instawow's `INSTAWOW_HOME`, it lets you
  run an isolated second setup, and tests point it at `t.TempDir()`.
- `--config <file>` overrides only the config file path.
- All path resolution lives in `internal/paths`, and nothing else calls
  `os.UserConfigDir` directly.

### 5.2 Config — `config.toml`

Written by the user or by `knapsack` commands. Never contains derived data.

```toml
current_profile = "forever"

[profiles.forever]
path   = "/games/wow/_forever_/Interface/AddOns"
flavor = "forever"
addons = [
  "github:austinrr/baganator-mirror",
  "github:austinrr/syndicator-mirror",
  "wago:VBNBxKx5",
]

[profiles.retail]
path   = "C:/Program Files (x86)/World of Warcraft/_retail_/Interface/AddOns"
flavor = "retail"
addons = ["curseforge:12345"]

[sources.curseforge]
api_key = ""

[sources.wago]
api_key = ""
```

What belongs here:

- **Profiles**: name, AddOns path, flavor.
- **Addons per profile**, stored as normalized **refs**, never raw URLs. The
  CLI accepts URLs and converts them, so each addon has exactly one spelling.
  GitHub `owner/repo` is lower-cased, because GitHub treats it
  case-insensitively.
- **Preferences**: only `current_profile` (named after kubectl's
  `current-context`). Each new preference must justify the code path it adds.
- **API keys**: in config, which is what LemonUp, WowUp and Ajour do.
  Environment variables take precedence (`KNAPSACK_CURSEFORGE_API_KEY`,
  `KNAPSACK_WAGO_API_KEY`), so a config kept in a dotfiles repo can leave
  them empty. When a config contains a key, Knapsack writes it with `0600`
  permissions.
- **GitHub tokens are never stored.** They come from `GITHUB_TOKEN`,
  `GH_TOKEN` or `gh auth token`, as in the LemonUp fork.

**Rewrites drop comments.** Commands that change config (`add`, `remove`,
`config set-profile`, `config use-profile`, `config delete-profile`) rewrite
the whole file in a stable key order, so diffs stay small under version
control. No Go TOML library preserves comments, so comments in `config.toml`
are lost when Knapsack writes it. This is documented in `--help` and the
README rather than worked around.

### 5.3 State — `state/<profile>.toml`

Machine-written facts about what is installed in one profile. This is the
only data Knapsack can't rebuild: which folders an addon owns can't be
reliably worked out from what's on disk.

```toml
version = 1   # schema version, for future migrations

[addons."github:austinrr/baganator-mirror"]
version      = "851"                # display label only; never compared
sha256       = "d3775a6bd651…"      # identity of the installed release
installed_at = 2026-11-13T18:04:05Z
folders      = ["Baganator"]

[addons."wago:VBNBxKx5"]
version      = "11.2.0"
sha256       = "1ab4…"
installed_at = 2026-11-13T18:04:09Z
folders      = ["Details", "Details_DataStorage", "Details_EncounterDetails"]
```

- **"Is an update available?" compares identity, not versions.** An addon is
  outdated when the resolved release's SHA-256 differs from the installed
  one. Version labels (`851`, `v1.2-beta`, `11.2.0`) are shown to the user
  but never sorted or compared, because they don't follow one scheme.
- Folders are names relative to the profile's AddOns path, never absolute
  paths. Renaming or deleting a profile renames or deletes its state file.
- Writes are atomic: write a temp file in the same directory, `fsync`, then
  rename.
- Not stored, because it would only go stale: addon titles and metadata (read
  from TOCs on demand), the latest available release (always fetched live),
  and download URLs (resolved each time).

### 5.4 Write order and consistency

Every change is written in a fixed order: **files on disk, then state, then
config.** If Knapsack is interrupted partway:

| Interrupted after | Result | How it's resolved |
|---|---|---|
| Swap, before state | Folders on disk that state doesn't record | `install` sees an unowned folder; `update --force` replaces it |
| State, before config | Addon in state but not in config | `list` shows it as `not in config`; `update --prune` removes it, `add` adopts it |

So config can never claim an addon whose files are half-installed.

### 5.5 Locking

Every command that reads or writes a profile holds `state/<profile>.lock`
(via `gofrs/flock`) for its whole duration. A second Knapsack process, such as
a scheduled run or the TUI, fails fast with
`error: profile "forever" is in use by another knapsack process` rather than
corrupting state. Profile-agnostic config commands lock `config.lock` instead.

### 5.6 Cache

In v1 the cache only holds downloads in progress. Planned after v0.1: HTTP
ETags for source API responses. GitHub doesn't count conditional requests that
return `304 Not Modified` against its rate limit, so ETags keep update checks
cheap without a token. It's deferred because a token already solves the rate
limit. Nothing may depend on the cache existing.

### 5.7 Core types (sketch)

```go
package flavor
// Flavor names match the client folder: _retail_ → retail, and so on.
type Flavor string
const (
    Retail      Flavor = "retail"      // _retail_
    Classic     Flavor = "classic"     // _classic_ (currently Mists)
    ClassicEra  Flavor = "classic_era" // _classic_era_
    Anniversary Flavor = "anniversary" // _anniversary_ (TBC Anniversary)
    Forever     Flavor = "forever"     // _forever_ (and _classic_beta_)
)
func Parse(s string) (Flavor, error)          // unknown values are rejected
func FromPath(addonsDir string) (Flavor, bool) // …/_forever_/Interface/AddOns → Forever
func (f Flavor) Client() Client               // the row of the client data table (§6.4)

// ClientInterface reads the installed client's version from .build.info in
// the WoW folder and returns its Interface number (1.60.1 → 16001).
func ClientInterface(addonsDir string, f Flavor) (int, bool)

package ref
type Ref struct {
    Source string // "github", "curseforge", "wago"
    ID     string // "owner/repo", "12345", "VBNBxKx5"; normalized
}
func Parse(s string) (Ref, error) // accepts `source:id` and known URLs
func (r Ref) String() string
func (r Ref) ShortName() string   // "baganator-mirror"

package source
type Release struct {
    Version     string // display label only
    DownloadURL string
    SHA256      string // from the source when it provides one (GitHub asset digests)
}
```

---

## 6. Key abstractions

### 6.1 Source

```go
// Source resolves an addon ID to the newest release compatible with a flavor.
type Source interface {
    Name() string
    Resolve(ctx context.Context, id string, fl flavor.Flavor) (Release, error)
}
```

- One small interface. Downloading and installing are shared code, **not**
  per-source logic, so a new source is roughly 100–200 lines plus tests.
- Each implementation takes its dependencies in a constructor:
  `github.New(client *http.Client, baseURL, token string)`. Passing `baseURL`
  is what lets tests point at an `httptest.Server`.
- `source.Registry` maps names to Sources and exposes
  `Resolve(ctx, ref.Ref, flavor.Flavor)`. Sources without their required API
  key aren't registered, and resolving one returns `ErrSourceNotConfigured`.

Sentinel errors in `source`: `ErrNotFound`, `ErrNoCompatibleRelease`,
`ErrRateLimited`, `ErrUnauthorized`, `ErrSourceNotConfigured`. Implementations
wrap them (`fmt.Errorf("github %s: %w", id, source.ErrNotFound)`) so callers
can use `errors.Is` without knowing which source failed.

### 6.2 Download (`httpx`)

```go
// Download streams url into a new file in dir, verifying wantSHA256 when it's
// non-empty, and returns the file's path and actual SHA-256.
func Download(ctx context.Context, c *http.Client, url, dir, wantSHA256 string) (path, sum string, err error)
```

`httpx.NewClient(version string)` returns an `*http.Client` with sensible
timeouts and a `User-Agent: knapsack/<version>` header containing no user
information. It retries idempotent GETs at most twice, only on 5xx responses
or connection resets.

### 6.3 Installer (filesystem only)

```go
type Options struct {
    Flavor   flavor.Flavor
    Previous []string // folders this addon owned before; may be replaced
    Force    bool     // also replace folders owned by no one
}

// Install installs the zip at archivePath into addonsDir and returns the
// folders it now owns. Either every folder is replaced or AddOns is unchanged.
func Install(ctx context.Context, archivePath, addonsDir string, opts Options) (folders []string, warnings []string, err error)

// Remove deletes the given folders from addonsDir.
func Remove(addonsDir string, folders []string) error

// Cleanup removes staging and backup folders left by an interrupted run.
func Cleanup(addonsDir string) error
```

The installer has no struct and no HTTP. It's a set of functions over the
filesystem, which keeps its tests plain: build a zip, call `Install`, inspect
the temp directory.

Pipeline:

1. **Cleanup** leftovers from an interrupted run (`.knapsack-staging-*`,
   `.knapsack-backup-*`). The app calls this before every operation on a
   profile.
2. **Extract** into `addonsDir/.knapsack-staging-<rand>`, so the final move is
   a same-filesystem rename. Reject zip-slip paths, absolute paths and
   symlinks. WoW ignores these dot-folders because they contain no matching
   `.toc`.
3. **Normalize**: if the archive has a single wrapper directory without a
   `.toc` (common with GitHub source zips), descend into it.
4. **Validate** each top-level folder against the flavor (§6.4).
5. **Conflict check**: a target folder that exists and isn't in `Previous` is
   refused unless `Force` is set.
6. **Swap**: rename each existing folder to `.knapsack-backup-<rand>/…`,
   rename the new folders into place, then delete the backups. On any failure,
   roll back.
7. Return the owned folders and any validation warnings.

Atomicity is per-folder rename. That isn't a true transaction, but with
rollback and startup cleanup it's the most you can get portably, and it's far
better than extracting directly into `AddOns/`.

**Windows:** renames can fail briefly while antivirus scans freshly extracted
files. Retry a failed rename a few times over about two seconds before
treating it as a real error. If it still fails with a sharing violation,
report `close World of Warcraft and retry`.

### 6.4 TOC parsing and flavor validation

`toc.Parse(r io.Reader) (File, error)` reads `## Key: Value` headers. It
handles `## Interface` lists (`## Interface: 120105, 16001, 50504, 11509`),
repeated `AllowLoadGameType`/`ExcludeLoadGameType` lines (later lines append),
a UTF-8 BOM and CRLF line endings.

Validation **mirrors what the game client does**, as documented on
[warcraft.wiki.gg/wiki/TOC_format](https://warcraft.wiki.gg/wiki/TOC_format)
and implemented in WowUp. Knapsack doesn't invent its own rules.

**Step 1: pick the TOC the client would load.** For each addon folder, the
client uses the first match of:

1. `<Folder>_<GameType>.toc` for its game type or an alias (Forever:
   `_Forever` or `_Camelot`)
2. `<Folder>_<Family>.toc` for its family (`_Mainline` or `_Classic`)
3. `<Folder>.toc`

**Step 2: check that TOC.**

| Condition | Result |
|---|---|
| No `.toc` at all | **Error.** Not an addon folder |
| `ExcludeLoadGameType` names this game type or its family, or `AllowLoadGameType` names only other recognized types | **Error.** The client will never load it |
| No `## Interface` value is in this game type's range | **Error.** Built for a different game (e.g. only `120105` on Forever's `16001`). This is the OpenMap case |
| A value is in range but none equals the client's Interface number | **Warning.** Out of date; WoW loads it only with "Load out of date AddOns" enabled |
| A value equals the client's Interface number | OK |

`AllowLoadGameType` follows the client's quirk: if none of the listed types
are recognized, the condition passes.

**The client data table** lives in `flavor` as plain data, the only place
that knows about specific game versions:

| Flavor | Client folders | Game type (aliases) | Family | Interface range | `.build.info` product |
|---|---|---|---|---|---|
| `retail` | `_retail_` | `standard` | `mainline` | 100000 and up | `wow` |
| `classic` | `_classic_` | `mists` | `classic` | 50000–59999 | `wow_classic` |
| `classic_era` | `_classic_era_` | `vanilla` | `classic` | 11000–11999 | `wow_classic_era` |
| `anniversary` | `_anniversary_` | `tbc` | `classic` | 20000–29999 | see §13 |
| `forever` | `_forever_`, `_classic_beta_` | `forever` (`camelot`) | `mainline` | 16000–16999 | `wowf` |

When Blizzard changes what a folder runs (`_classic_` has been Cataclysm and
is now Mists), only this table changes.

**The exact Interface number comes from the installed client, not the
table.** The game records its version in `.build.info` in the WoW folder
(`1.60.1.70938` for Forever), and the Interface number follows from it:
major·10000 + minor·100 + patch, giving `16001`. The same formula gives
retail's `120105` and Mists' `50504`. Reading it means nothing goes stale when
a patch ships. If `.build.info` is missing or unreadable, Knapsack skips the
out-of-date warning and still applies every error check.

### 6.5 App layer

```go
type Options struct {
    Paths    paths.Paths
    Config   *config.Store
    State    *state.Store
    Sources  *source.Registry
    HTTP     *http.Client
    Reporter Reporter
}

func New(opts Options) *App

func (a *App) Add(ctx context.Context, profile string, refs []ref.Ref, force bool) ([]Result, error)
func (a *App) Remove(ctx context.Context, profile string, names []string) ([]Result, error)
func (a *App) Update(ctx context.Context, sel Selector, opts UpdateOptions) ([]Result, error)
func (a *App) Check(ctx context.Context, sel Selector) ([]Plan, error)
func (a *App) List(ctx context.Context, sel Selector) ([]Installed, error)
func (a *App) Info(ctx context.Context, profile, name string) (Details, error)

// Selector picks what a bulk operation covers.
type Selector struct {
    Profiles []string // one profile, or every profile for --all-profiles
    Addons   []string // empty = all addons in each profile
}
```

- **Stores, not loaded structs.** `config.Store` and `state.Store` load fresh
  data at the start of each operation, under the profile lock. Nothing goes
  stale in a long-running TUI, and nothing is cached between commands.
- **Every method takes a `ctx`**, including local-only ones, since they all
  take locks and may be cancelled.
- **Concurrency**: resolve and download run in parallel (`errgroup`, limit
  about 6). **Installs run one at a time** per profile, so folder-conflict
  checks never race. With several profiles, each profile is processed in turn.
- **Partial failure**: one addon failing doesn't stop the rest. Each `Result`
  carries its own error, and the method's own `error` is reserved for failures
  that stop everything (config unreadable, lock unavailable).
- **Reporter**: `Event(e Event)` receives `Resolving`, `Downloading{Bytes,
  Total}`, `Installed`, `Skipped`, `Warned`, `Failed`. Implementations **must
  be safe for concurrent use**. The CLI prints lines or a progress bar on a
  TTY, the TUI sends Bubble Tea messages, and tests use `NopReporter` or a
  recording fake.

### 6.6 Errors with hints

```go
// UserError is an error with a message and an optional hint meant for the user.
type UserError struct {
    Msg  string
    Hint string
    Err  error // underlying cause; shown with --verbose
}
```

`app` returns a `UserError` wherever it knows how the user can fix the
problem, for example a missing API key, a profile in use, or a folder owned by
nothing. Frontends are the only place that renders errors, using `errors.As`
to find the hint. Packages below `app` return plain wrapped errors and never
write user-facing text.

---

## 7. Sources

| Source | Access | v1 status |
|---|---|---|
| **GitHub** | Public REST API. 60 req/h anonymous, 5000 with a token. Asset metadata includes a `sha256:` digest | **v1** |
| **CurseForge** | Official API needs an API key (console.curseforge.com). Authors can opt out of third-party distribution | Later |
| **Wago** | Addons API needs a per-user API key | Later |

### GitHub resolution

1. `GET /repos/{owner}/{repo}/releases/latest`.
2. Choose an asset, in order:
   1. A `.zip` whose name matches the flavor (`-forever`, `-classic`,
      `-mainline`, …).
   2. Exactly one `.zip` with no flavor marker.
   3. Otherwise, fail with `ErrNoCompatibleRelease` and list the assets seen.
3. Use the asset's `digest` as `Release.SHA256`. It's verified on download
   and compared with state to detect updates.
4. Never fall back to source tarballs automatically. Mixed-TOC source trees
   are what caused the OpenMap problem.

CurseForge projects that block third-party access are covered by the existing
`wow-addon-mirror-action`, which mirrors them to GitHub releases, so GitHub
support alone already covers most of the author's addons.

---

## 8. CLI

### 8.1 Grammar

The structure and conventions follow kubectl: profiles are managed the way
kubectl manages contexts (`config use-profile`, like `config use-context`), and
`-p`, `-A`, `-o`, `--prune` and the `check` exit codes match kubectl's. The
verbs come from package managers (`add`, `remove`, `update`), because they
describe addons better than kubectl's `create`/`delete`/`apply`. Addons are
the only everyday resource, so commands don't repeat a noun (`add`, not
`add addon`).

Two patterns from LemonUp are deliberately avoided: one install command per
source (`install-github`, `install-wago`, …) and separate `update`/`update-all`
commands. The source is part of the ref, and "all" is the default.

```
# Addons
knapsack add <ref|url>...          add to config and install
knapsack remove <addon>...         uninstall owned folders and remove from config   (alias: rm)
knapsack update [addon...]         install missing addons, upgrade outdated ones
knapsack check [addon...]          show what update would do; changes nothing
knapsack list                      installed addons; offline                       (alias: ls)
knapsack info <addon>              ref, version, owned folders, TOC flavors, install time

# Profiles
knapsack config set-profile <name> --path <AddOns> [--flavor <flavor>]
knapsack config get-profiles
knapsack config use-profile <name>
knapsack config current-profile
knapsack config delete-profile <name>
knapsack config view

# Other
knapsack tui                       (later) interactive interface over the same core
knapsack version
knapsack completion bash|zsh|fish|powershell
```

### 8.2 Bulk operations

`check` and `update` cover **every addon in the profile** when no addons are
named, so bulk is the default rather than a separate command.

| Command | Scope |
|---|---|
| `knapsack check` | All addons in the current profile |
| `knapsack check -p retail` | All addons in `retail` |
| `knapsack check -A` | All addons in every profile |
| `knapsack update` | Update everything in the current profile |
| `knapsack update -A` | Update everything in every profile |
| `knapsack update baganator-mirror` | Just that addon |

`-A, --all-profiles` mirrors kubectl's `-A, --all-namespaces`. It applies to
`check`, `update` and `list`, and can't be combined with `-p` or with named
addons. Output is grouped by profile, with a summary line per profile:

```
$ knapsack check -A
PROFILE  ADDON               INSTALLED  AVAILABLE  ACTION
forever  baganator-mirror    851        853        upgrade
forever  syndicator-mirror   212        212        -
retail   curseforge:12345    -          4.2.0      install

forever: 1 upgrade · retail: 1 install
```

### 8.3 Global flags

| Flag | Env | Meaning |
|---|---|---|
| `-p, --profile <name>` | `KNAPSACK_PROFILE` | Profile to act on (like kubectl `--context`). Defaults to `current_profile` |
| `-A, --all-profiles` | | Act on every profile (`check`, `update`, `list`) |
| `-o, --output <fmt>` | | `wide` (extra columns) or `json`. Default: a human-readable table |
| `-v, --verbose` | | Debug logging and underlying errors to stderr via `slog` |
| `--config <file>` | | Use this config file (like `--kubeconfig`) |

### 8.4 Command behavior

- **`add`** resolves each ref, installs it, then records it in state and
  config, in that order (§5.4). A failed `add` leaves config unchanged.
  Adding an addon that's already in config is a no-op that says so. Adding
  one that's in state but not in config adopts it.
- **`update`** makes the profile match its config: it installs addons listed
  in config but not installed, and upgrades installed addons whose resolved
  release differs from the installed one (§5.3). This makes `update` the
  everyday command and the one-step setup for a new machine or a hand-edited
  config. `--prune` also uninstalls addons in state but not in config.
  Without `--prune`, `update` never removes anything. `--force` replaces
  folders owned by no addon.
- **`check`** runs the same resolution as `update` and prints the planned
  changes (install, upgrade, prune candidates) without making any.
- **`remove`** deletes only the folders recorded as owned in state, then
  removes the addon from state and config.
- **`list`** reads config and state only. It never touches the network, so
  it's instant. Each addon's status is `installed`, `missing` (in config, not
  installed) or `not in config` (in state only). Use `check` to see what's
  outdated.
- **`config set-profile`** creates or updates a profile. If `--flavor` is
  omitted, it's inferred from the client folder in the path (`_forever_` →
  `forever`), and it's an error if it can't be. The first profile created
  becomes current.
- **`config delete-profile`** removes the profile and its state file. It never
  touches addon files on disk.

### 8.5 Naming addons

- **Refs** take the form `<source>:<id>`: `github:owner/repo`,
  `curseforge:<id|slug>`, `wago:<id>`.
- **URLs** for each source are accepted anywhere a ref is, and are normalized
  to a ref before being stored.
- **Short names**: commands that act on installed addons (`remove`, `update`,
  `check`, `info`) also accept `Ref.ShortName()` (`baganator-mirror`) when
  exactly one addon in the profile matches. If several match, the error lists
  them.
- There's no bare `owner/repo` shorthand. The explicit form is only a few
  characters longer and never ambiguous.

### 8.6 Output, errors and exit codes

- Tables go to stdout; progress, warnings and logs go to stderr, so
  `-o json` output can always be piped.
- Errors are one line plus an optional hint (§6.6):
  ```
  error: curseforge is not configured
  hint: set sources.curseforge.api_key or KNAPSACK_CURSEFORGE_API_KEY
  ```
- When several addons are processed, one failure doesn't stop the rest. Each
  failure is reported, and the command exits non-zero at the end.
- Exit codes follow the `diff`/`grep` and `kubectl diff` convention:

  | Code | Meaning |
  |---|---|
  | `0` | Success (for `check`: everything up to date) |
  | `1` | `check` only: changes are pending |
  | `2` | Any error, including usage errors and partial failures |

- No interactive prompts. Anything destructive needs an explicit flag:
  `--force` to overwrite folders Knapsack doesn't own, `--prune` to remove
  addons that are no longer in config.
- Ctrl-C cancels the context, and in-flight installs roll back.
- Only `rm` and `ls` have aliases.

### 8.7 Cobra conventions

Cobra's own examples use package-level command variables and `init()`
registration. Knapsack doesn't:

- `newRootCmd(deps *deps) *cobra.Command` builds the whole command tree, and
  each subcommand has its own `newXxxCmd(deps)` constructor. `deps` is built
  lazily from global flags in `PersistentPreRunE`.
- Commands write only through `cmd.OutOrStdout()` and `cmd.ErrOrStderr()`, so
  tests can capture output.
- Every command uses `RunE` and returns errors. `SilenceUsage` and
  `SilenceErrors` are set on the root, and `main` is the single place that
  renders errors and picks the exit code.
- `main` is about 10 lines: build a context with `signal.NotifyContext` for
  Ctrl-C, call `newRootCmd(...).ExecuteContext(ctx)`, map the error to an exit
  code.

---

## 9. Testing strategy

Aim for tests that are fast, deterministic, offline and use real code paths
wherever that's cheap.

| Layer | Approach |
|---|---|
| `ref`, `flavor`, `toc`, `config`, `state` | **Table-driven unit tests.** Pure functions, many cases, including malformed input. Fuzz tests (`go test -fuzz`) for `ref.Parse` and `toc.Parse`. |
| `source/github` etc. | **`httptest.Server`** serving recorded JSON fixtures from `testdata/`. Covers asset selection, digests, 404, 403 rate-limit and 401. No live network in unit tests. |
| `httpx.Download` | `httptest.Server`: checksum match and mismatch, truncated bodies, retries, cancellation. |
| `install` | **Real filesystem in `t.TempDir()`.** Build zips in-test with `archive/zip`. Covers wrapper-dir normalization, zip-slip and symlink rejection, every row of the validation table, conflict detection, rollback on an injected failure, cleanup of leftovers and multi-folder addons. |
| `app` | **Fakes, not mocks**: a hand-written `fakeSource`, real stores and installer on a temp `KNAPSACK_HOME`. Verifies use cases end to end, including partial failures, `--prune`, `-A`, interrupted-write recovery (§5.4) and lock contention. |
| `cmd/knapsack` | **Golden-file** tests: run commands against a temp `KNAPSACK_HOME` and a fake source, then compare stdout, stderr and the exit code with `testdata/*.golden` (`go test ./cmd/... -update` regenerates them). |
| Live APIs | Optional `//go:build integration` tests hitting real GitHub, run manually or in a scheduled CI job. Never block PRs. |

Conventions:

- `t.Parallel()` wherever there's no shared state. Tests never touch the
  real config directory; they set `KNAPSACK_HOME` with `t.Setenv` or pass
  paths directly.
- `t.Helper()` in helpers. `cmp.Diff` for struct comparisons.
- Test behavior through exported functions. No reaching into unexported
  fields.
- Race detector in CI: `go test -race ./...`.
- Coverage is reported but not gated. Aim for thorough tests on `install`,
  `toc`, `app` and sources, which carry the real risk.

---

## 10. Linting and code quality

`golangci-lint` v2 with a deliberate, not exhaustive, set of linters.

```yaml
# .golangci.yml
version: "2"
linters:
  default: standard        # errcheck, govet, ineffassign, staticcheck, unused
  enable:
    - bodyclose            # unclosed HTTP response bodies
    - errorlint            # errors.Is/As and %w used correctly
    - exhaustive           # switches over Flavor, Event kinds, etc. cover every case
    - gocritic
    - gosec                # file permissions, zip-slip, etc.
    - misspell
    - nilerr
    - noctx                # HTTP requests without a context
    - revive
    - unconvert
    - unparam
    - usestdlibvars
  settings:
    gosec:
      excludes:
        - G304             # file paths from variables; a file manager does this by design
formatters:
  enable:
    - gofmt
    - goimports
  settings:
    goimports:
      local-prefixes:
        - github.com/austinrr/knapsack
```

`wrapcheck` and `contextcheck` are left out on purpose. Both are known for
false positives, and code review covers error wrapping.

Code conventions:

- Errors are wrapped with context at each boundary (`%w`). Sentinel errors
  are used for conditions callers branch on. User-facing text exists only in
  `app.UserError` and the frontends (§6.6).
- `context.Context` is the first parameter of anything that does I/O or takes
  a lock.
- Interfaces are small and defined in the package that uses them, except
  `source.Source` (§4).
- Constructors take dependencies explicitly. There's no global HTTP client and
  no `init()`.
- No `panic` outside truly impossible states.
- Exported identifiers get doc comments. Package docs go in `doc.go`.
- Files are kept focused. Split when a file stops having one clear job.

---

## 11. CI and releases

**CI** (GitHub Actions, on push and PR), on Linux, Windows and macOS:

1. `go mod tidy` diff check
2. `golangci-lint run`
3. `go test -race ./...`
4. `go tool govulncheck ./...` (Linux only)
5. `go build ./...`

Running the test matrix on all three OSes matters: path handling, renames and
file locking differ, especially on Windows.

**Releases**: tagging `vX.Y.Z` triggers `go tool goreleaser`, which builds
linux/windows/darwin × amd64/arm64, generates checksums and a changelog, and
publishes a GitHub release. The version is injected with `-ldflags` and shown
by `knapsack version`. Package-manager publishing (AUR, Homebrew, Scoop) comes
later.

---

## 12. Milestones

| # | Scope | Done when |
|---|---|---|
| **M0** | Skeleton: module, Cobra root per §8.7, `version`, Makefile, CI, lint config | CI green on all three OSes |
| **M1** | `paths`, `config`, `flavor`, `ref`; `config` profile commands | Profiles round-trip through TOML with tests |
| **M2** | `toc`, `install`, `state`, locking | Installs a local zip atomically, with rollback and cleanup tests |
| **M3** | `httpx`, GitHub source; `add`, `remove`, `list`, `info` | Installs the author's mirror repos into Forever |
| **M4** | `update` (incl. `--prune`), `check`, `-A`; concurrency | Daily-driver replacement for the LemonUp fork |
| **M5** | goreleaser, first `v0.1.0` release | Binaries downloadable for all three OSes |
| **M6** | CurseForge and Wago sources; ETag cache | Behind API-key config |
| **M7** | `knapsack tui` on the same `app` layer | Feature parity with the CLI |

---

## 13. Open questions

1. **`.build.info` product codes**: `wowf` (Forever) is confirmed from a
   real install. Confirm the codes for the other clients, Anniversary in
   particular, when implementing `flavor.ClientInterface`.
2. **Importing existing installs**: should there be a one-time
   `knapsack import` from LemonUp's state, or does `add` for each addon cover
   it?
3. **Pinning**: is `github:owner/repo@v1.2.3` worth supporting in v1, or
   should it wait until someone needs it?
4. **Dependencies**: TOCs declare `## Dependencies`. Should Knapsack warn
   about missing ones? (Leaning: warn only, never auto-install.)
