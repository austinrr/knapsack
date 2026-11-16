# Knapsack — Design

Status: draft · Last updated: 2026-11-15

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
- **CLI first**, built so a TUI could be added without touching the core,
  as it was (§9).
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
| **Game type** | The client's own name for what it runs, used in TOC suffixes and `AllowLoadGameType`: `standard`, `forever`, `mists`, `vanilla`, … Each flavor maps to one game type, which may have aliases (Forever's `camelot`), and one family (`mainline` or `classic`). |
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
| TUI (§9) | `charmbracelet/bubbletea`, `bubbles`, `lipgloss` | Best Go TUI stack; Elm-style model fits a core/frontend split |

Explicitly **not** used: assertion/mocking frameworks (testify, gomock),
GitHub SDKs (go-github), git libraries, ORMs/SQLite.

### Development tools

| Tool | Use |
|---|---|
| `gofmt` / `goimports` | Formatting (enforced through golangci-lint formatters) |
| `golangci-lint` v2 | Linting (config in §11). Pinned as a `tool` in its own module, `tools/lint/go.mod`, so its dependencies are the ones its release was built with. `make lint` runs it, locally and in CI |
| `govulncheck` | Dependency vulnerability scan. Pinned as a `tool` dependency in `go.mod` |
| `goreleaser` | Cross-platform release builds. Pinned as a `tool` in its own module, `tools/go.mod`, so its hundreds of dependencies and newer Go requirement stay out of Knapsack's `go.mod` (and off `go install` users). Run with `go -C tools tool -n goreleaser` |
| `git-cliff` | Changelog and next-version calculation from conventional-commit PR titles. Runs in CI only |
| GitHub Actions | CI on Linux, Windows and macOS |
| `Makefile` | `make lint`, `make test`, `make build`. Thin wrappers only; CI runs the same commands |

---

## 4. Architecture

```
     ┌────────────────┐        ┌────────────────┐
     │  cmd/knapsack  │        │  internal/tui  │  (`knapsack tui`, §9)
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
   | `flavor`, `ref`, `toc`, `paths`, `atomicfile`, `lock`, `tomlenc` | nothing internal |
   | `httpx` | nothing internal |
   | `config`, `state` | `flavor`, `ref`, `atomicfile`, `tomlenc` |
   | `install` | `flavor`, `toc` |
   | `selfupdate` | nothing internal |
   | `discover` | `flavor` |
   | `source` | `flavor`, `ref` |
   | `source/*` | `source`, `flavor`, `ref`, `httpx` |
   | `app` | any of the above |
   | `cmd/knapsack`, `tui` | `app`, plus `flavor` and `ref` for parsing input, and the packages `main` constructs for `app.Options` (`paths`, `config`, `state`, `httpx`, `source`, `source/github`) |

   Nothing imports `app` except the frontends, and `install` never touches
   the network. `cmd/knapsack` also imports `tui`, to run it as
   `knapsack tui`.
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
    selfupdate/          replace the knapsack binary from a release archive (filesystem only)
    discover/            find World of Warcraft installs for profiles create --auto (filesystem only)
    toc/                 .toc parsing
    atomicfile/          atomic file writes (temp file, fsync, rename)
    tomlenc/             TOML value formatting for the config and state encoders
    lock/                per-profile and config locks (wraps gofrs/flock)
    httpx/               shared HTTP client (timeouts, User-Agent) and Download
    tui/                 knapsack tui (§9)
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
  config.lock           # held by every command that writes config.toml
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
path = "/games/wow/_forever_/Interface/AddOns"
flavor = "forever"
addons = [
  "github:austinrr/baganator-mirror",
  "github:austinrr/syndicator-mirror",
  "wago:VBNBxKx5",
]

[profiles.retail]
path = "C:/Program Files (x86)/World of Warcraft/_retail_/Interface/AddOns"
flavor = "retail"
addons = [
  "curseforge:12345",
]

[sources.curseforge]
api_key = "…"

[sources.wago]
api = "website"
```

What belongs here:

- **Profiles**: name, AddOns path, flavor. Names are lowercase letters,
  digits, `-` and `_`, because each one becomes a state and lock file name. On
  the case-insensitive filesystems of Windows and macOS, `Forever` and
  `forever` would otherwise share one state file.
- **Paths** are absolute, in Unix or Windows form. Either form loads on any OS,
  so one config in a dotfiles repo can hold profiles for both. Two profiles
  can't share an AddOns folder, since each would track the same folders in
  its own state.
- **Addons per profile**, stored as normalized **refs**, never raw URLs. The
  CLI accepts URLs and converts them, so each addon has exactly one spelling.
  GitHub `owner/repo` is lower-cased, because GitHub treats it
  case-insensitively.
- **Preferences**: only `current_profile` so far. Settings for all of
  Knapsack, such as how often the TUI checks for updates, or how many
  backups to keep, will go here too, shown by `knapsack config`. Each new
  one must justify the code path it adds.
- **API keys**: in config, which is what LemonUp, WowUp and Ajour do.
  Environment variables take precedence (`KNAPSACK_CURSEFORGE_API_KEY`,
  `KNAPSACK_WAGO_API_KEY`), so a config kept in a dotfiles repo can leave
  them empty. When a config contains a key, Knapsack writes it with `0600`
  permissions. Keys are global, shared by every profile, and managed with
  `knapsack sources` (§8.4) rather than by editing the file.
- **How each source is read**: `api = "official"`, the default, or
  `"website"`, per source (§7). Set with `knapsack sources set-api`; the
  default isn't written.
- **GitHub tokens are never stored.** They come from `GITHUB_TOKEN`,
  `GH_TOKEN` or `gh auth token`, as in the LemonUp fork.

**Rewrites drop comments.** Commands that change config (`add`, `remove`,
`profiles create`, `set`, `use` and `delete`, `sources set-api` and
`set-key`) rewrite
the whole file in a stable key order, so diffs stay small under version
control. No Go TOML library preserves comments, so comments in `config.toml`
are lost when Knapsack writes it. This is documented in `--help` and the
README rather than worked around.

The layout is fixed: profiles in name order, one addon per line so adding
or removing one is a one-line diff, and `[sources.*]` tables only when they
hold a setting. Saving what was loaded gives identical bytes. Unknown keys are
an error on load, because they're almost always typos (`flavour`) that
would otherwise be silently ignored.

### 5.3 State — `state/<profile>.toml`

Machine-written facts about what is installed in one profile. This is the
only data Knapsack can't rebuild: which folders an addon owns can't be
reliably worked out from what's on disk.

```toml
version = 1

[addons."github:austinrr/baganator-mirror"]
version = "851"
sha256 = "d3775a6bd651…"
installed_at = 2026-11-13T18:04:05Z
folders = ["Baganator"]

[addons."wago:VBNBxKx5"]
version = "11.2.0"
sha256 = "1ab4…"
installed_at = 2026-11-13T18:04:09Z
folders = ["Details", "Details_DataStorage", "Details_EncounterDetails"]

[addons."curseforge:988370"]
version = "290"
sha256 = "7e3f…"
installed_at = 2026-11-15T09:00:00Z
folders = ["Syndicator"]
added_as = "curseforge:syndicator"
```

`version` is the schema version, for future migrations. A file with a newer
one is refused with a hint to upgrade Knapsack. An addon's `version` is its
release label, shown but never compared, and `sha256` is the identity of the
installed release. Like config, the file is written in a fixed layout by a
small hand-written encoder, so it stays easy to read when debugging.

- **"Is an update available?" compares identity, not versions.** An addon is
  outdated when the resolved release's SHA-256 differs from the installed
  one. Version labels (`851`, `v1.2-beta`, `11.2.0`) are shown to the user
  but never sorted or compared, because they don't follow one scheme.
- Folders are names relative to the profile's AddOns path, never absolute
  paths. Deleting a profile deletes its state file.
- **`added_as`** is the ref an addon was added as, when its source keeps
  another ID for it (§7, CurseForge): the slug of a pasted link, say. So
  `remove`, `update` and `info` find the addon by the link, ref or short
  name it was added with, offline, without asking the source again. It's
  written only when it differs, so a Knapsack older than CurseForge
  support, which would refuse the unknown key, never meets it in a
  profile it could otherwise read. Upgrades keep it.
- **Loading is strict**, because `remove` deletes the folders state lists:
  folder names must be single path elements (no separators, `.` or `..`), no
  folder can belong to two addons, refs must be in normal form, and unknown
  keys are an error.
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
corrupting state.

Every command that **writes `config.toml`** also holds `config.lock`, in
Knapsack's config directory, beside `state/` so no profile name can collide
with it. That includes profile commands such as `add`, which record refs in
config. It's taken after the profile lock, and config is re-read under it
before the change is applied, so two commands on different profiles can't
each save a stale copy and lose the other's change. The lock stays in
Knapsack's directory with `--config`, so nothing is created next to a config
file kept elsewhere.

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
    Forever     Flavor = "forever"     // _forever_
)
func Parse(s string) (Flavor, error)          // unknown values are rejected
func FromPath(addonsDir string) (Flavor, bool) // …/_forever_/Interface/AddOns → Forever
func (f Flavor) Client() (Client, bool)       // the row of the client data table (§6.4)

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
  `Resolve(ctx, ref.Ref, flavor.Flavor)`. A source that isn't built isn't
  registered, and resolving one returns `ErrSourceNotConfigured`. So does a
  built one whose API key isn't set; the key is read on first use, so a
  command with no addons from that source never reads it.
- `Release.Header` carries headers the download needs, such as Wago's API
  key. It's never stored.

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
or connection resets, with a short backoff. A 429 isn't retried: rate limits
need a far longer wait, which the source reports instead.

There's no overall request timeout, which would cut off a large download on a
slow link. Connecting (10 s), the TLS handshake (10 s) and waiting for
response headers (30 s) time out, and `Download` fails with `ErrStalled` when
the body stops arriving for 60 s. That timer starts once headers are in and
resets after every read and write, so retries and a slow disk don't count.

`Download` writes to a temp file in `dir` (the cache directory), hashing as
it writes. It fails with `*StatusError` for any status but 200, `ErrChecksum`
when the SHA-256 differs, `io.ErrUnexpectedEOF` for a body shorter than its
`Content-Length`, and `ErrTooLarge` past 1 GiB. An expected hash that isn't
64 hex digits is a separate error, before any request: it's the source's data
that's wrong, not the file. No file is left behind on error.

### 6.3 Installer (filesystem only)

```go
type Options struct {
    Flavor          flavor.Flavor
    ClientInterface int      // from flavor.ClientInterface; 0 if unknown
    Previous        []string // folders this addon owned before; may be replaced
    Others          []string // folders other addons own; never replaced (ErrOwned)
    Force           bool     // also replace folders owned by no one
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

1. **Cleanup** leftovers from an interrupted run. The app calls this before
   every operation on a profile. Staging and trash folders are deleted, on a
   best-effort basis, since leftovers there are harmless. A backup folder
   without a trash mark means a swap was interrupted: every folder in it is moved
   back, replacing any new folder already moved into its place, so AddOns
   holds the release state still records rather than a mix. New folders
   that had no old counterpart stay, unowned, as in the first row of the
   table in §5.4.
2. **Extract** into `addonsDir/.knapsack-staging-<rand>`, so the final move is
   a same-filesystem rename. Reject zip-slip paths, absolute paths (including
   drive letters), symlinks and other special files, and paths (folders
   included) that collide when case is ignored or are both a file and a
   folder. Backslashes count as separators, since some Windows tools write
   them. Extraction stops at 1 GiB actually written, whatever sizes the
   archive claims, or 100,000 entries. Corrupt data is a bad archive; failing
   to read the file is an I/O error. Archive permissions are ignored: files are
   written `0600` and folders `0700`. WoW ignores these dot-folders because
   they contain no matching `.toc`.
3. **Normalize**: while the archive has a single wrapper directory without a
   `.toc` (common with GitHub source zips), descend into it. Top-level files
   such as a README, hidden folders such as `.github`, and Finder's
   `__MACOSX` are ignored, but a `.toc` at the top level is an error,
   since an addon's files must be in a folder named after it.
4. **Validate** each top-level folder against the flavor (§6.4).
5. **Conflict check**: a target folder that exists and isn't in `Previous` is
   refused (`ErrConflict`) unless `Force` is set. Names match without regard
   to case, but an exact match wins, so on a case-sensitive filesystem
   holding both `Details` and `details` the right one is used. The installer
   only knows this addon's folders; the app refuses a folder another addon
   owns in state before calling it, even with `--force`, which only covers
   folders no addon owns (§8.4).
6. **Swap**: rename each folder being replaced, and each folder in `Previous`
   the new release no longer has, into `.knapsack-backup-<rand>/`, then
   rename the new folders into place. On any failure, undo every rename in
   reverse. On success, mark the swap finished by renaming the backup to
   `.knapsack-trash-<rand>`, which is atomic, then delete it. If that rename
   fails, an empty `.knapsack-trash-<rand>` file is the mark instead, kept
   until the backup is gone. Either way `Cleanup` never restores folders
   from a swap that finished. If rollback itself fails, the backup is kept
   and the error names it.
7. Return the owned folders and any validation warnings.

`Remove` uses the same swap with nothing moved in, so either every folder is
removed or none is.

Atomicity is per-folder rename. That isn't a true transaction, but with
rollback and startup cleanup it's the most you can get portably, and it's far
better than extracting directly into `AddOns/`.

**Windows:** renames can fail briefly while antivirus scans freshly extracted
files. Retry a failed rename a few times over about two seconds before
treating it as a real error. If it still fails with a sharing or lock
violation, the error wraps `ErrInUse`, which the app reports as `close World of Warcraft and retry`.

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

1. `<Folder>_<GameType>.toc` for its game type (`_Forever`, `_Mists`, …),
   then for each alias (Forever: `_Camelot`, as Details ships)
2. `<Folder>_<Family>.toc` for its family (`_Mainline` or `_Classic`)
3. `<Folder>.toc`

File names are matched without regard to case, as on Windows and macOS.

**Step 2: check that TOC.**

| Condition | Result |
|---|---|
| No `.toc` at all | **Error.** Not an addon folder |
| `ExcludeLoadGameType` names this game type, an alias or its family, or `AllowLoadGameType` names only other recognized types | **Error.** The client will never load it |
| No `## Interface` line, or no value in this game type's range | **Error.** Built for a different game (e.g. only `120105` on Forever's `16001`). This is the OpenMap case |
| A value is in range but none equals the client's Interface number | **Warning.** Out of date; WoW loads it only with "Load out of date AddOns" enabled |
| A value equals the client's Interface number | OK |

`AllowLoadGameType` follows the client's quirk: if none of the listed types
are recognized, the condition passes. "Recognized" means the client's full
list from the wiki (`standard`, `forever`, `plunderstorm`, `wowhack`,
`vanilla`, `tbc`, `wrath`, `cata`, `mists`, and the families `mainline` and
`classic`), which includes game types Knapsack has no flavor for.

**The client data table** lives in `flavor` as plain data, the only place
that knows about specific game versions:

| Flavor | Client folders | Game type (aliases) | Family | Interface range | `.build.info` product |
|---|---|---|---|---|---|
| `retail` | `_retail_`; tests `_ptr_`, `_xptr_`, `_beta_` | `standard` | `mainline` | 100000 and up | `wow`; `wowt`, `wowxptr`, `wow_beta` |
| `classic` | `_classic_`; tests `_classic_ptr_`, `_classic_beta_` | `mists` | `classic` | 50000–59999 | `wow_classic`; `wow_classic_ptr`, `wow_classic_beta` |
| `classic_era` | `_classic_era_`; test `_classic_era_ptr_` | `vanilla` | `classic` | 11000–11999 | `wow_classic_era`; `wow_classic_era_ptr` |
| `anniversary` | `_anniversary_` | `tbc` | `classic` | 20000–29999 | see §14 |
| `forever` | `_forever_` | `forever` (`camelot`) | `mainline` | 16000–16999 | `wowf` |

Each row also lists the **asset markers** that name release files built for
that client: `mainline`/`retail`, `mists`/`mop`,
`vanilla`/`classic`/`era`/`classic_era`, `tbc`/`bcc` and `forever`. `classic`
means Classic Era, as in the BigWigs packager's file names, not the
`_classic_` client. `wrath`, `wotlk`, `wotlkc`, `cata`, `cataclysm`, `sod`
and `hardcore` are known markers for clients or realms with no flavor, so
such files are never mistaken for unmarked ones. A tag Knapsack doesn't know
still reads as unmarked; the TOC check at install (§6.4) rejects a build for
the wrong client either way.

A **test client** (PTR or beta) has its own client folder and AddOns, and
runs the same flavor of addons as its live client, so `…/_ptr_/Interface/
AddOns` is `retail`. It reads its own product's row in `.build.info` for
the out-of-date check. A profile for one sits beside the live client's, so
without a name `profiles create` calls it `retail-ptr`, leaving `retail` for the
live client. A test folder's flavor is only a default, though: Blizzard has
used `_classic_beta_` for betas of more than one Classic game, so `--flavor`
may differ from it (that profile is then `anniversary-beta`), and a stored
flavor isn't re-inferred from it. A live folder's flavor must match. When a
beta runs the next expansion,
its Interface numbers fall outside the flavor's range: the check is
skipped, and addons built only for the new expansion are refused until the
range here changes.

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

// Profile use cases behind `knapsack config …`: SetProfile, Profiles,
// UseProfile, CurrentProfile, DeleteProfile, ViewConfig.

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
  that stop everything (config unreadable, lock unavailable). Errors with a
  known fix (a conflict, a rate limit, the game holding files) become a
  `UserError` with a hint.
- **Profile**: a use case's `profile` argument, or an empty `Selector`, means
  the current profile. Each one takes the profile lock and loads config and
  state under it. Those that change AddOns run `install.Cleanup` on it first;
  `list` and `info` only read, so they work even when the folder is missing.
- **Results** come back in the order the addons were named. `remove` checks
  for cancellation before each addon.
- **Write order within `add`**: each addon's state is saved as soon as it's
  installed, so files on disk are never unrecorded for longer than one
  install; config is saved once at the end with every addon that made it.
  An interruption in between leaves addons in state but not config, which the
  next `add` adopts (§5.4).
- **`Update` and `Check`** cover the addons named, in the order given, or
  else every addon in config, in config order, then those only in state
  (prune candidates). An addon is up to date when the resolved release's
  SHA-256 matches state's (§5.3): with a SHA-256 published by the source,
  that's known without downloading; otherwise the release is downloaded and
  hashed, and `Check` does the same just to compare. `Update` prunes before
  installing, so freed folders are available, and never changes config.
  Without `--prune`, addons only in state come back as `not in config`, so
  the CLI can say they're left installed; a named one is an error, with a
  hint to add it back or prune it.
- **Several profiles** are processed in turn, with results grouped by
  profile. A failure that stops one profile (lock held, unknown profile)
  stops the run, and the results so far are still returned.
- **`info`** reads the title, author, notes and Interface numbers from the
  TOC the client loads from the addon's main folder: the longest folder name
  contained in the ref's short name (`baganator-mirror` → `Baganator`), or
  else the first not starting with `!`, the prefix bundled libraries use.
- **Reporter**: `Event(e Event)` receives events of kind `EventResolving`,
  `EventDownloading`, `EventProgress` (bytes so far and expected, from
  `httpx.Download`'s callback), `EventInstalled`, `EventRemoved`,
  `EventSkipped`, `EventChecked` (Check knows the plan), `EventWarned` and
  `EventFailed`, each naming the profile and ref. Every addon ends with one
  of Installed, Removed, Skipped, Checked or Failed. Implementations **must
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
| **CurseForge** | Official API needs an API key (console.curseforge.com). Authors can opt out of third-party distribution. The website's own JSON API needs none, by project ID | **v1** |
| **Wago** | Official addons API needs a per-user API key. The website needs none | **v1** |

**Official API or website.** CurseForge and Wago both give API keys out
only to some, so each can be read two ways, chosen per source in config
(`api`, §5.2) with `knapsack sources set-api <source> <official|website>`:

- **`website`**: what the source's own website loads, as a
  browser does, with no key. It isn't an API, so it can change without
  notice. Anything Knapsack doesn't understand (an unexpected status, a
  page without the data it reads, JSON of another shape) is
  `ErrUnexpectedWebsite`, saying what was wrong and which URL. Its hint
  suggests `self-update`, a newer release possibly understanding it, and
  the official API, and the CLI adds a `report:` line: a link that opens a
  GitHub issue with the error, the version and the platform filled in
  (§8.6). Not found and rate limits are still `ErrNotFound` and
  `ErrRateLimited`, and a 5xx is a plain error: an outage, fixed by
  retrying later, not by a report. A 403, such as a bot challenge, is
  unexpected, since it means the website no longer answers Knapsack.
- **`official`**, the default: the documented API, with the key. Without
  one, resolving is `ErrSourceNotConfigured`, whose hint says how to get
  and set a key, or to switch to the website. The official API is the
  default because it's the supported way in: the website is the fallback
  for those without a key.

The source's failure is the addon's, as any other, so the rest of a bulk
command carries on.

### GitHub resolution

1. `GET /repos/{owner}/{repo}/releases/latest`.
2. Choose an asset, in order:
   1. The `.zip` whose name ends with one of the flavor's markers
      (`-forever`, `-mists`, `-mainline`, …, after `-` or `_`). Markers are
      in the flavor table (§6.4). Only the end counts, so
      `Classic_Castbars-1.0.zip` isn't a classic build.
   2. Exactly one `.zip` with no marker for any client.
   3. Otherwise, fail with `ErrNoCompatibleRelease` and list the assets seen.
      Several candidates at one step is also a failure, rather than a guess.

   `-nolib` zips are skipped: they need their libraries installed
   separately. Other assets, such as the packager's `release.json`, are
   ignored.
3. Use the asset's `digest` (`sha256:<hex>`) as `Release.SHA256`. It's
   verified on download and compared with state to detect updates. Assets
   uploaded before GitHub recorded digests have none; `SHA256` is then empty,
   and the release can only be identified by downloading and hashing it.
4. **Private repositories** work with a token that can read them, from
   wherever Knapsack finds one (§5.2): the release is resolved with it, and
   the asset downloads from the API (the asset's `url`, with `Accept:
   application/octet-stream`), which redirects to a signed, short-lived
   link. Its `browser_download_url` would need a browser's login.
   `httpx.Download` drops the token from the redirect, which goes to
   another origin, and the token goes only to the API's own origin. With
   a token, public assets download the same way; without one, from
   `browser_download_url` as before. The download is then an API request
   too, one of the token's 5,000 an hour, so a refused download gets the
   source error a refused lookup would (401 unauthorized, 404 not found,
   429 or a spent 403 rate limited), and its hint.
5. A 404 is `ErrNotFound` (GitHub also answers 404 for a repo with no
   releases, and for a private one without a token that can read it,
   which the hint says), 401 is `ErrUnauthorized`, and 429, or 403 with no requests left
   or a `Retry-After`, is `ErrRateLimited`, with the reset time and, without a
   token, a note that one raises the limit.
6. Never fall back to source tarballs automatically. Mixed-TOC source trees
   are what caused the OpenMap problem.

### Wago resolution

#### Website

1. `GET https://addons.wago.io/addons/{id}`, `{id}` the Wago ID or slug, as
   for the API. The page is drawn by Inertia, which puts its data, as
   HTML-escaped JSON, in the app element's `data-page` attribute. Its
   `component` must be `Addon/AddonDetails`.
2. `props.metadata.recent_releases` maps a game version, named as for the
   API, to its newest stable release, with `label` and `download_link`. As
   in the API, `[]` or `null` is none. Without the flavor's game version,
   it's `ErrNoCompatibleRelease`.
3. The download link is signed and expires; it needs no key, so none is
   sent. It's resolved just before it's downloaded, so it doesn't expire
   first.
4. 404 is `ErrNotFound`, 429 `ErrRateLimited`, 5xx a plain error, and
   anything else unexpected `ErrUnexpectedWebsite` (§7).

#### Official API

The API isn't publicly documented. Its shape here is what WowUp and
instawow use, which agree.

1. `GET https://addons.wago.io/api/external/addons/{id}?game_version={gv}`
   with `Authorization: Bearer <key>`. `{id}` is the addon's Wago ID or the
   slug from its addons.wago.io link; both are accepted. `{gv}` is Wago's
   name for the flavor's game: `retail`, `mop` (Classic), `classic`
   (Classic Era), `bc` (Anniversary) and `forever`.
2. `recent_release` maps a stability to that game's newest release of it.
   Only `stable` is installed, as only GitHub's latest release is, never a
   pre-release. Without one, it's `ErrNoCompatibleRelease`, naming the
   stabilities there are, with a hint that only stable releases are
   installed. Wago sends `[]` for none.
3. The release's `label` is `Release.Version` and `download_link` the
   download. The download sends the key in the same header, as instawow
   does (WowUp adds `?token=<key>` instead), but only when the link has the
   API's own origin (scheme, host and port, ignoring case). `httpx.Download`
   drops it from a redirect to any other origin, where Go's default would
   forward it to subdomains and from https to http.
4. Wago publishes no checksum, so `Release.SHA256` is empty: `check`
   downloads the archive to compare it with state.
5. 404 is `ErrNotFound`, 401 and 403 are `ErrUnauthorized`, 429 is
   `ErrRateLimited`. A missing key is `ErrSourceNotConfigured`, whose hint
   names `knapsack sources set-key wago` and Wago's API keys page, and
   `knapsack sources set-api wago website`.

### CurseForge resolution

Refs are `curseforge:<id>` or `curseforge:<slug>`, and `add` takes a
`curseforge.com/wow/addons/<slug>` link. CurseForge's files are tagged
with game version types: 517 for Retail, 79434 for Classic (Mists), 67408
for Classic Era, 73246 for Anniversary (TBC) and 88568 for Forever. Only
release files (`releaseType` 1) are installed, never betas or alphas, as
on the other sources.

**The project ID is what's kept.** `add` stores `curseforge:<id>` whatever
it's given (`source.Identifier`), so a pasted link or slug is looked up
once, and either way of reading CurseForge can use the ref later. What it
was added as is kept in state (`added_as`, §5.3), so the link or slug
still names it. Duplicates are dropped after the lookup, so a slug and its
ID given together are one addon. A slug written into config by hand still
works, looked up on each resolve, and adding it again finds it there. The
lookup is the official API's search, or for the website, CFWidget
(below).

#### Website

1. A slug's ID comes from `GET https://api.cfwidget.com/wow/addons/{slug}`,
   whose `id` is the project's. CFWidget is a community service that
   mirrors CurseForge's project details. CurseForge's own pages and
   search, which would say, sit behind a Cloudflare challenge that only a
   browser passes: Go's client is refused every time, and Knapsack won't
   pretend to be a browser. 404 is `ErrNotFound`. 202 means CFWidget
   hasn't seen the project yet and is fetching it: retry shortly.
2. `GET https://www.curseforge.com/api/v1/mods/{id}/files?pageIndex={n}&pageSize=50&sort=dateCreated&sortDescending=true`,
   the JSON API the website lists files with, newest first. The first
   file with `releaseType` 1 and the flavor's type in `gameVersionTypeIds`
   is the release, looking through at most 4 pages. A file the author
   allows only CurseForge's app to download isn't marked in this listing,
   so unlike the official API, the website can't tell or refuse it: one
   reason the official API is the default. No files at all is
   `ErrNotFound`: the API answers an unknown project with an empty list.
3. The download is `…/mods/{id}/files/{fileId}/download`, which redirects
   to CurseForge's CDN. `displayName` is `Release.Version`. No checksum is
   published, so `check` downloads to compare, as for Wago.
4. Statuses are as for Wago's website (§7).

This is what `wow-addon-mirror-action` uses, which mirrors CurseForge
projects to GitHub releases.

#### Official API

1. `x-api-key: <key>` on every request to `https://api.curseforge.com`.
2. A slug's ID: `GET /v1/mods/search?gameId=1&slug={slug}`, the result with
   that exact slug. None is `ErrNotFound`.
3. `GET /v1/mods/{id}/files?gameVersionTypeId={type}&index={n}&pageSize=50`,
   paged until a page has an available release file, at most 4. The docs
   don't promise an order, so the newest such file by `fileDate` is
   chosen. The slug search sends no `classId`: the docs name none for
   WoW's addons, and the exact slug is matched.
4. A file without a `downloadUrl` is one whose author allows only
   CurseForge's own app to download it: `ErrNotDistributed`, whose hint
   suggests looking for it on GitHub or Wago. The CDN needs no key, so
   none is sent with the download.
5. 404 is `ErrNotFound`; 401 and 403 are `ErrUnauthorized` (the docs don't
   say which a bad key gets); 429 is `ErrRateLimited`.

---

## 8. CLI

### 8.1 Grammar

The structure and conventions follow kubectl, without copying it: they
should feel familiar, not be the same. **Profiles are like namespaces**:
each is a scope of addons, an AddOns folder with a flavor, and `-p` and
`-A` scope commands to one or to all, as `-n` and `-A` do. They're a
resource of their own (`knapsack profiles list`, `create`, `delete`), not
connection settings as kubectl's contexts are: there's only one "cluster",
the machine, and credentials are shared in `[sources.*]`. Where they
differ, a profile has fields, a path and a flavor, changed with
`profiles set` (as `kubectl set` changes a field), and there's a current
profile rather than a context's default namespace, switched with
`profiles use`, as kubens does. Up to v0.2, profiles were `config
set-profile`, `get-profiles`, `use-profile`, `current-profile` and
`delete-profile`, after kubectl's contexts. `-o`, `--prune` and the `check`
exit codes match kubectl's. The
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
knapsack update [addon...]         upgrade outdated addons, install missing ones
knapsack check [addon...]          show what update would do; changes nothing
knapsack list [--no-check]         addons, by title, and whether they're up to date  (alias: ls)
knapsack info <addon>              ref, version, owned folders, TOC flavors, install time

# Profiles, like namespaces: addon commands act on the current one, -p names another, -A all
knapsack profiles list             each profile, its flavor, addon count and folder   (alias: ls)
knapsack profiles create [name] --path <AddOns> [--flavor <flavor>] | --auto
knapsack profiles set <name> [--path <AddOns>] [--flavor <flavor>]
knapsack profiles edit <name>      its path, flavor and addons, in your editor
knapsack profiles use <name>
knapsack profiles current
knapsack profiles delete <name>    (alias: rm)

# Settings for all of Knapsack
knapsack config view
knapsack config edit               config.toml, in your editor

# Sources (shared by every profile)
knapsack sources list              each source, how it's read, whether it's ready, and where its key comes from
knapsack sources set-api <source> <official|website>
knapsack sources set-key <source>  store an API key (prompted, or from stdin)
knapsack sources unset-key <source>

# Other
knapsack tui                       interactive interface over the same core (§9)
knapsack version                   the version, and whether a newer one is out
knapsack self-update               replace knapsack with the newest release
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
| `-o, --output <fmt>` | | `wide` (extra columns) or `json`. Default: a human-readable table. Only commands that print data accept it, each with the formats it supports, so it's never silently ignored |
| `-v, --verbose` | | Debug logging and underlying errors to stderr via `slog` |
| `--config <file>` | | Use this config file (like `--kubeconfig`) |

### 8.4 Command behavior

- **`add`** resolves each ref, installs it, then records it in state and
  config, in that order (§5.4). A failed `add` leaves config unchanged.
  Adding an addon that's already in config is a no-op that says so. Adding
  one that's in state but not in config adopts it.
- **`update`** upgrades installed addons whose resolved release differs
  from the installed one (§5.3), which is what it's mostly used for. It also
  installs addons listed in config but not installed, so the profile
  matches its config: that makes it the one-step setup for a new machine
  or a hand-edited config too. `--prune` also uninstalls addons in state but not in config.
  Without `--prune`, `update` never removes anything. `--force` replaces
  folders owned by no addon.
- **`check`** runs the same resolution as `update` and prints the planned
  changes (install, upgrade, prune candidates) without making any.
- **`remove`** deletes only the folders recorded as owned in state, then
  removes the addon from state and config.
- **`list`** reads config, state and a listing of AddOns, and checks each
  addon's newest release with the same resolution as `check`. An installed
  addon is `up to date`, `update available`, or `unknown` when checking
  failed (the error prints under the table, and `list` exits 2, as `check`
  does). Otherwise an addon is `not installed` (in config only) or `not in
  config` (in state only). `--no-check` skips the network, so `list` is
  instant and installed addons are just `installed`. On a terminal, the
  table is drawn before checking, from config and state alone, with a
  spinner (`⠋ checking`) for each addon being checked; each row is redrawn
  as its check finishes (§8.6). `check`'s table does the same. `check` stays for
  scripts: its exit code says whether anything is pending.
  Addons are named by the `## Title` in their installed TOC, with WoW's
  color and texture codes removed (`toc.Plain`), or by repo name when not
  installed (`displayName`); commands take those titles too (§8.5). Two
  rows in a profile with the same name get their ref's ID added
  (`Details! (a/details)`), since titles needn't be unique. A SOURCE column
  shows the source's name (`github`), or with `-o wide` the full ref, in
  both `list` and `check`. Folders in AddOns that no addon in
  state owns are `unmanaged`: installed by hand or by another manager.
  They're listed only with `--unmanaged` (named by folder, with the TOC's
  `## Version`), since most users have many; otherwise a note on stderr
  says how many there are, so a profile made over an existing AddOns
  folder doesn't look empty without explanation, whatever the output
  format, after a blank line under a table. Hidden folders, files and
  Knapsack's own working folders don't
  count. An AddOns folder that can't be read just has none, so `list`
  still shows config and state.
- **Update check.** Once a day, any command run with stderr on a terminal
  asks GitHub for Knapsack's latest release, in the background while the
  command runs, and if it's newer prints one line on stderr afterwards:
  ``knapsack v0.2.0 is available (you have v0.1.0); run `knapsack
  self-update`.`` `version` checks every time, terminal or not, printing
  `v0.2.0 is available; …` on stderr so stdout stays just the version.
  The answer, or a failed or timed-out attempt (not one cancelled with
  Ctrl-C), is cached in `latest-release.json` in the cache directory, so
  being offline costs one attempt a day. Within the day, the check
  doesn't look for a GitHub token, so it never runs `gh`. A command
  waits at most 2 s for the check after it finishes, usually not at all,
  and a failed check prints nothing (`version -v` shows why). There's no
  check in CI (`CI` set), for `help`, `--help`, `completion` and command
  groups like `config`, for a dev build (including a `go build` from a
  checkout, whose pseudo-version or `+dirty` Go stamps), or with
  `KNAPSACK_NO_UPDATE_NOTIFIER` set. This is the one network use
  outside `add`, `update`, `check`, `list` and `self-update`, like gh's update
  notifier. The suggested command depends on how Knapsack was installed:
  `self-update`, `go install …@latest`, or the package manager.
- **`self-update`** replaces the running binary with the latest release's
  archive for its OS and architecture, verified against the SHA-256 GitHub
  publishes for it. The new binary is extracted next to the old one and
  renamed over it with the old one's permissions, so a failure leaves the
  old one; Windows can't replace a running `.exe`, so it's renamed to
  `.old` first and deleted on the next run (and if an earlier `.old` is
  still running, the hint says to close it). It refuses, with a hint, for a dev build, a `go install` build (no
  version set at build time; the hint is `go install …@latest`), and a
  binary under a package manager's directory (Homebrew, Scoop, winget, Nix,
  `/usr/bin`), whose records it would leave wrong. The checksum only
  catches corruption: it comes from the same release as the archive.
  Verifying build attestations would also catch a tampered release, and is
  a possible follow-up.
- **`profiles create --auto`** finds the game and makes a profile for
  each client folder (live or test) with `Interface/AddOns` in it, named
  as an unnamed `profiles create` names it (`retail`, `forever`, `retail-ptr`).
  `internal/discover` lists the usual places: on Windows, `Program Files
  (x86)`, `Program Files`, `Games`, `Blizzard` and the root of every drive;
  on macOS, `/Applications` and `~/Applications`; on Linux, `drive_c` in
  Wine prefixes, as Wine, Lutris (`~/Games/*`), Faugus (`~/Faugus/*`),
  Bottles and Steam's Proton make them. A folder counts when it holds a
  client folder. With `--path`, that game folder is used instead, and `-v`
  lists the places looked. Live clients are made first, across every game
  folder, so the profile that becomes current isn't a PTR or beta.
  Existing profiles for a folder are unchanged, and a client whose name
  another folder has is skipped with that error's hint, without stopping
  the rest. A client with no `Interface/AddOns` yet is skipped too:
  Knapsack doesn't make folders in the game's install. `--auto` takes no
  name or `--flavor`.
- **`profiles create`** creates a profile, and refuses one that exists,
  named or found by its folder, with a hint to use `profiles set`, as
  `kubectl create` refuses an existing namespace. `--path` is the
  AddOns folder, or the client folder or its `Interface` folder, which
  Knapsack completes to `Interface/AddOns`; given the game folder itself, the
  error lists its clients. A leading `~` is expanded, since shells don't
  expand it inside the quotes a path with spaces needs. An unquoted path the
  shell split into extra arguments (cmd.exe does; bash and fish usually
  reject `(x86)` first) is recognized, by an argument with `\`, `/` or
  parentheses, and the error shows it rejoined and quoted. Hints and help
  show this OS's usual path, quoted. After the first profile, a note on
  stderr says to add addons next. If `--flavor` is
  omitted, it's inferred from the client folder in the path (`_forever_` →
  `forever`), and it's an error if it can't be. The first profile created
  becomes current. The name is optional, since most people have one profile
  per flavor: without one, it's the flavor's. If a profile of that name
  already uses another folder, it's an error, not a move: a second install
  of the same client, such as a test copy, needs its own name, and moving
  the existing profile is `profiles set`. A test client's folder gets a
  name of its own, `retail-ptr` (§6.4). `--path` must be an existing
  directory, and an explicit `--flavor` that contradicts the path's client
  folder is an error.
- **`profiles set <name>`** changes only the flags given, `--path` or
  `--flavor`, as `kubectl set` changes one field; it needs at least one,
  and the profile must exist. The addons and state stay, so it's for a
  game that moved with its addons. Without `--path`, the flavor is never
  re-inferred: it stays as stored unless `--flavor` is given. Changing a
  profile never makes it current.
- **`profiles edit <name>`** and **`config edit`** open what they edit
  in the user's editor, as `kubectl edit` does: `$KNAPSACK_EDITOR`, then
  `$VISUAL`, then `$EDITOR`, else `vi` (Notepad on Windows), run by the
  shell, so it can take arguments (`code --wait`). `profiles edit` shows
  the profile's `path`, `flavor` and `addons`; `config edit`, all of
  `config.toml`, API keys included, as stored. Notes on what's edited,
  and the last error, are lines starting `#| `, dropped on save wherever
  they are, so the file's own comments stay: `config edit` saves the file
  as written. What's saved is validated as a load would, and a profile's
  path and flavor as `profiles set` checks them (an existing folder,
  completed to `Interface/AddOns`; a flavor that agrees with a live
  client folder). Invalid, the editor opens again with the error at the
  top. Unchanged, the edit is cancelled. Editing is declarative: addons
  added or taken out are installed by `update` and uninstalled by
  `update --prune`, which a note after saving says. A profile's path and
  flavor are checked only when the edit changes them, so a profile whose
  folder is missing for now (one for the other OS, in a shared config)
  can still have its addons edited; a changed path may use `~` and a
  client folder, as `--path` does. `config edit` deletes a profile taken
  out of the file as `profiles delete` does, with its state, and moves the
  state of one renamed (its folder, under a new name). No lock is held
  while the editor is open; instead, saving refuses if the profile or
  file changed meanwhile, as `kubectl edit` refuses a stale object, and
  saving takes the locks a change to those profiles would, so nothing is
  edited under a running `update`. Ctrl-C reaches Knapsack from the
  editor too, so it's ignored while the editor runs, as git ignores it:
  the editor alone decides when it's done. On Windows the editor runs
  through `cmd.exe /S /C`, given the command line as it is, since cmd.exe
  doesn't parse Go's quoting. The copy is next to `config.toml`, which it
  may hold all of, readable only by the user, and kept when an edit fails,
  or is cancelled or the editor fails after an invalid try, with its path
  in the hint; otherwise it's removed.
- **`profiles delete`** removes the profile and its state file. It never
  touches addon files on disk. If it was the current profile, none is current
  afterwards; Knapsack doesn't guess another.
- **`config view`** prints config with API keys shown as `REDACTED`, like
  `kubectl config view`.
- **`sources`** alone prints its help, like `config`. **`sources list`**
  (`ls`) lists every source with a status (`ready`, or `needs key` for
  one read through its official API without a key), how it's read (`official` or
  `website`; GitHub is always `official`), and where its key comes from: config, the environment variable that overrides it, or for
  GitHub, `GITHUB_TOKEN`, `GH_TOKEN` or `gh` (which listing may run).
  Until v0.1.4, bare `sources` was the list; it became a group so its
  subcommands show up, as `config`'s do.
  `sources set-key <source>` stores a key: typed at a prompt that doesn't
  echo on a terminal, else the first line of stdin (`pass show cf |
  knapsack sources set-key curseforge`; pass prints the secret first),
  never as an argument, which would land in shell history and `ps`, as `gh
  secret set` and `docker login --password-stdin` avoid. Ctrl-C at the
  prompt restores the terminal's echo. A key with spaces, control
  characters or invalid UTF-8 is refused before it's saved. These commands
  don't build network clients. The source is checked before the key is asked
  for; GitHub is refused, since its token is never stored. Both `set-key`
  and `unset-key` note on stderr when the environment variable overrides
  config, and `set-key` when the source is read from its website, which
  doesn't use the key.
  `sources set-api <source> <official|website>` chooses how a source is
  read (§7); choosing `official`, the default, removes the setting. It
  notes on stderr when the official API has no key yet. GitHub is
  refused: it has only its API.

### 8.5 Naming addons

- **Refs** take the form `<source>:<id>`: `github:owner/repo`,
  `curseforge:<id|slug>`, `wago:<id>`.
- **URLs** for each source are accepted anywhere a ref is, and are normalized
  to a ref before being stored.
- **Short names**: commands that act on installed addons (`remove`, `update`,
  `check`, `info`) also accept `Ref.ShortName()` (`baganator-mirror`) when
  exactly one addon in the profile matches. If several match, the error lists
  them.
- **Titles**: when no short name matches, the `## Title` of an installed
  addon's TOC, which is how `list` and `check` show it, matched without
  regard to case (`knapsack info "Leatrix Maps"`). Ambiguity is the same
  error. Unmanaged folders are shown by folder, not title, since the folder
  is what's in AddOns.
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
- A source's website answering unexpectedly (§7) adds a third line, a link
  to report it, with the error, version and platform filled in. The error
  names the addon and URL, nothing of the user's, such as paths:
  ```
  error: wago plater: unexpected answer from the website: GET https://addons.wago.io/addons/plater has no data-page attribute
  hint: Knapsack reads wago's website, which may have changed since this release. `knapsack self-update` may fix it; if not, please report it at https://github.com/austinrr/knapsack/issues, …
  report: https://github.com/austinrr/knapsack/issues/new?body=…&title=…
  ```
- When several addons are processed, one failure doesn't stop the rest. Each
  failure is reported on stderr after the successes, as its own `error:` line
  and hint, and the command exits non-zero at the end. A malformed ref is a
  usage error instead: it's caught with the arguments, before any work.
- `add` and `remove` print one line per addon on stdout (`Added
  github:austinrr/baganator-mirror 854 to forever.`). Warnings go to stderr, prefixed
  with the addon's ref, and so does
  each step (resolving, downloading) with `-v`. On a terminal without `-v`,
  progress is one status line on stderr, redrawn with `\r` at most every
  100 ms and erased before results print: `2 done · 1 resolving · 3
  downloading, 1.2 of 4.5 MiB`. One line needs no cursor movement, so it
  works in every terminal, the old Windows console included. `TERM=dumb`
  gets plain lines.
- Wherever Knapsack waits on the network on a terminal, a spinner turns
  (`⠋`, or `|/-\` in the Windows console, whose fonts lack braille), so a
  slow lookup never looks like a hang. One `spinner` drives three
  displays, all on a 100 ms ticker:
  - the progress bar above, prefixed with it while anything is in
    progress (`⠹ 2 done · 1 resolving`);
  - a one-line status on stderr for a wait with nothing else to show:
    `self-update` finding and downloading the release, `version`'s check,
    and the daily check when it's still running after the command. It
    appears only after 200 ms, so a quick answer doesn't flash one;
  - a live table for `list` and `check`, the commands whose output is a
    table of what the network says. It's drawn before anything is
    resolved, from config and state alone, with `⠋ checking` in STATUS or
    ACTION, and each row is redrawn as it's resolved (`app.Watcher`). The
    live table needs cursor movement (ANSI escapes, which Windows 10's
    console takes once `ENABLE_VIRTUAL_TERMINAL_PROCESSING` is set). Every
    line is cut to the terminal's width, so none wraps, and rows that
    don't fit on the screen are counted on a last line (`… 12 more`), so
    the cursor can always move back up over the table. It replaces the
    bar, and a warning prints above it. When checking is done the live
    table is erased and the finished one printed as anywhere else, so it's
    never cut. Piped output, `-o json`, `-v`, `TERM=dumb` and a console
    without VT support get the table only when it's done, as before. An empty `list` prints a note
  on stderr and nothing on stdout. Times are shown in UTC (RFC 3339).
- `update` prints a line per addon it installs, upgrades or removes, then a
  summary (`forever: 1 upgraded, 3 up to date`). `check` prints the table in
  §8.2, then a summary (`forever: 1 upgrade, 1 prune`, or `forever: up to
  date`). Under `-A`, lines are prefixed with the profile, tables get a
  `PROFILE` column, and the summary has one group per profile.
- Human-readable output always names the profile it acted on, so a wrong
  `-p`, `KNAPSACK_PROFILE` or current profile shows at once: summaries
  start with it, `add` and `remove` say `to forever` / `from forever`,
  `list` prints `Profile forever:` on stderr above its table (stdout stays
  just the table), `info` has a `Profile:` field, and an empty profile is
  `No addons in profile forever.` JSON already has a `profile` field. `-A` ignores
  `KNAPSACK_PROFILE`, and with no profiles it's an error.
- Exit codes follow the `diff`/`grep` and `kubectl diff` convention:

  | Code | Meaning |
  |---|---|
  | `0` | Success (for `check`: everything up to date) |
  | `1` | `check` only: changes are pending (prune candidates count only with `check --prune`) |
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

## 9. TUI

`knapsack tui` opens an interactive interface over the same `app` core as
the CLI, laid out like k9s: one table at a time, a short header, and
single-key actions. It starts minimal; anything it lacks, the CLI still
does. Like the CLI, it never decides anything itself: every action is one
`app` use case (§4, rule 1).

### 9.1 Layout

```
 Profile: forever                                [1] addons  2 profiles  3 sources
 Path:    C:\Program Files (x86)\World of Warcraft\_forever_\Interface\AddOns   ? help
┌─────────────────────────────── addons[6] ───────────────────────────────────────┐
│   NAME↑            VERSION   AVAILABLE   STATUS                 SOURCE          │
│ ● Baganator        851       854         ↑ update available     github          │
│   Details!         11.2.0    11.2.0      ✓ up to date           wago            │
│   Plater           610       ⠹ downloading 1.2 / 4.5 MiB        wago            │
│   Leatrix Maps     —         1.4.2       ○ not installed        github          │
│   WeakAuras        5.17.1    —           ✗ unknown              github          │
└─────────────────────────────────────────────────────────────────────────────────┘
 a add  u update  U update all  enter describe  ctrl-d remove  / filter   2 updates
```

- **Header**: the current profile and its AddOns path, labeled, each on its
  own line. A path too long for the line is cut in the middle, keeping the
  client folder (`C:\Program Files…\_forever_\Interface\AddOns`), and
  shortened with `~` as `profiles list` does. On the right, the views,
  with the current one bracketed and highlighted, and `? help`.
- **Footer**: the current view's most-used keys on the left; `?` lists
  all of them. On the right, the status: counts after a check, the last
  result, or an error with its hint, in red. A result ("forever: 1
  upgraded") is said until the next key, as k9s flashes it; a job's
  error stays until the next action, so a reload can't hide it. When
  they don't all fit, hints are left out whole, from the end.
- **A newer Knapsack release** is said after quitting, by the same
  notice as any command's (§8.4), since the daily check runs while the
  TUI does; the TUI doesn't say it again inside.
- **Rows** are named, ordered and labeled as in `list` (§8.4). STATUS is
  `app.Status`, with a glyph: `✓` up to date, `↑` update available, `○`
  not installed, `!` not in config, `✗` unknown, and a spinner while
  checking. A row being worked on shows its stage across AVAILABLE and
  STATUS instead: resolving, downloading with bytes, installing.
- **Unmanaged folders** aren't shown in v1. An empty profile says so and
  counts them (`Listing.Hidden`), so a profile made over an existing
  AddOns folder doesn't look broken: `No addons in forever. 23 folders
  in AddOns weren't installed by Knapsack.`
- **No profiles yet**: the TUI opens on the profiles view, which says
  `A` finds the game and `n` adds one by path. The profile made current
  is then shown, as is one used with `enter`.
- **Too small**: under 60×15, the screen says to make the window larger,
  rather than drawing a broken table.

### 9.2 Views and keys

Three views, switched with `1`–`3` or `tab`/`shift-tab`. Describe opens
over the addons view, so it has no number; `esc` closes it. Number keys
can't clash with any view's action keys, which a letter would (`a` is
add).

**Global**

| Key | Action |
|---|---|
| `1` `2` `3` | Addons, profiles, sources view |
| `tab` / `shift-tab` | Next / previous view |
| `/` | Filter the table by name, ref or source as you type; `enter` keeps it, `esc` clears it |
| `?` | Help: every key in the current view; `esc` closes it |
| `esc` | Close a modal, filter, help or describe |
| `j` `k`, `↓` `↑` | Move |
| `g` / `G` | Top / bottom |
| `ctrl-f` / `ctrl-b` | Page down / up |
| `q`, `ctrl-c` | Quit. While a job that changes anything runs, asks first; quitting cancels it, and installs in progress roll back (§9.6). A read, such as a check, is just abandoned |

**Addons**

| Key | Action |
|---|---|
| `enter` | Describe the addon |
| `a` | Add dialog (§9.5) |
| `u` | Update the marked addons, or the cursor's when none are: at once when each simply has a newer release, else through the plan. Addons known to be up to date are left alone, without the network; the status line says `c` checks again |
| `U` | Update all: the plan modal (§9.5) first |
| `ctrl-d` | Remove the marked addons, or the cursor's, after a confirm listing their folders |
| `c` | Check again: forget what's known and ask every source (§9.4) |
| `space` | Mark or unmark the row (`●`). Marks on rows the filter hides still count; the remove confirm lists every addon it removes |
| `ctrl-\` | Clear marks |
| `o` | Open the addon's page in the browser |
| `e` | Edit the profile in the editor, as `profiles edit` does (§9.6) |

**Describe** shows `app.Details`: title, ref, status with `installed →
available`, and, right under it for a row whose last job or check
failed, the error, its hint and its `report:` link (§8.6), so a long
list of folders never pushes them out of view; then author, notes,
install time, Interface numbers, folders and the source page. Describe
and help scroll with `j`/`k` (and `g`, `G`, `ctrl-f`, `ctrl-b`) when
they don't fit, with a last line saying how many more there are.

| Key | Action |
|---|---|
| `u` | Update this addon |
| `ctrl-d` | Remove it, after a confirm, then close describe |
| `o` | Open its page |
| `r` | Open the report link, when there is one; otherwise the status line says only a website answering unexpectedly has one |

**Profiles**: NAME (`(*)` for the current one), FLAVOR, ADDONS (how many
in config) and PATH, from `Profiles`. No update counts: they'd need the
network for every profile. `Profiles` reads only config, so the view
is read when it's opened, without waiting for a job; the changes are
jobs. Deleting the profile shown leaves none current, and the addons
view says so, with the command that picks one.

| Key | Action |
|---|---|
| `enter` | Use the profile (`UseProfile`) and show its addons |
| `n` | New profile form: path, then optional name and flavor (`SetProfile`) |
| `s` | The same form for the cursor's profile, filled in; only changed fields are sent, as `profiles set` takes them, and a field can't be emptied. A new path forgets what was checked in the old folder |
| `A` | Find the game and make a profile per client (`AutoProfiles`); the result, made and skipped, shows in a modal, as `profiles create --auto` reports it |
| `e` | Edit the profile in the editor; while a job runs or waits, it says to wait, since saving takes the lock the job may hold |
| `ctrl-d` | Delete the profile, after a confirm saying its addon files stay |

**Sources**: NAME, STATUS (`ready` or `needs key`), READ VIA and KEY FROM,
as `sources list` shows them.

| Key | Action |
|---|---|
| `enter` | Set the API key: masked input (§9.5) |
| `ctrl-d` | Remove the key, after a confirm (`SetSourceKey` with `""`) |
| `w` | Switch between the official API and the website (`SetSourceAPI`) |

`j` and `k` move here as everywhere, so setting a key is `enter`, and
removing one `ctrl-d`, as removing and deleting are in the other views.
On GitHub's row, the three say why they don't apply, as the CLI's
errors do: its token is never stored, and it has only its API. The view
is read when it's opened, as the profiles view is, with where GitHub's
token comes from (`Options.GitHubKeyFrom`, which may run `gh`). After a
change, what was checked for that source's addons is forgotten, since a
key or a way of reading changes the answer, and the status line says
`c` checks them again; a key the environment overrides, or one for a
source read from its website, is noted as `set-key` notes it.

### 9.3 Package and wiring

`internal/tui` is a frontend like `cmd/knapsack`, under the same import
rule (§4). It's built on Bubble Tea, Bubbles (`key`, `help`, `textinput`,
`textarea`, `viewport`) and Lip Gloss (§3). The table is drawn by `tui`
itself rather than with `bubbles/table`, since rows need a mark column,
per-cell styles and a progress span across two columns.

`cmd/knapsack/tui.go` adds the `tui` command and builds what `tui` can't
import from `main`:

```go
// package tui
type Options struct {
    // NewApp builds the App with the TUI's Reporter, which must exist
    // before the App does (app.Options.Reporter).
    NewApp  func(app.Reporter) *app.App
    Profile string // -p or $KNAPSACK_PROFILE; "" for the current one
    Home    string // for shortening paths
    Running string // "v0.2.0 on linux/amd64", for report links
    // GitHubKeyFrom says where GitHub's token comes from ("GITHUB_TOKEN",
    // "GH_TOKEN", "gh" or ""), as sources list finds it.
    GitHubKeyFrom func(context.Context) string
    // Edit runs the CLI's edit loop (§8.4, profiles edit) on a profile,
    // with the terminal handed over to the editor.
    Edit func(ctx context.Context, profile string) (saved bool, res app.ProfileEditResult, err error)
}

func Run(ctx context.Context, opts Options) error
```

Paths are shortened with `app.ShortenHome`, and an error's `report:` link
is `app.ReportURL`, as the CLI's are. So are rows named (`app.UniqueNames`),
counts and sizes said (`app.Count`, `app.FormatProgress`), and sources
described (`SourceInfo.Status`, `app.KeyFrom`, `app.DescribeKey`), from
`internal/app/display.go`, so both frontends say a thing the same way.

`tui` uses the App through an interface of the methods it calls, defined
in `tui` (§4, rule 5), so its tests can use a fake. `*app.App` satisfies
it.

The command refuses to start unless stdin and stdout are terminals, with
a hint to use the CLI's commands in scripts. `-p` opens on that profile
without changing the current one, as on any command; `-A` isn't taken.

**Model.** One root model holds the header, the three views, describe,
at most one modal, the status line, the job queue (§9.4) and what's
known about each addon. Keys go to the modal when there is one, else to
the filter when it's open, else to the view; global keys are tried
last, so a dialog's text fields can type `q` and digits. Views are plain
structs the root updates and draws, not separate `tea.Model`s, so state
lives in one place.

### 9.4 Jobs, progress and what's known

**Jobs.** Every `app` call runs in a `tea.Cmd`, never in `Update`.
The same work isn't queued twice: `c` while a check is running or
queued does nothing, an addon already in an update or remove is left
out of the next one, and opening describe on another addon, or closing
it, drops a queued `Info`. A job ending clears only its own rows'
stages, so one still queued keeps showing `queued`.
They run **one at a time**, in the order started, because profile locks
are file locks: a second lock on a profile from the same process fails
as `in use by another knapsack process`, just as from another one
(§5.5). Starting an action while a job runs queues it, and the footer
says `1 queued`. Reads that take the lock (`List`, `Info`) queue too;
they're quick, and describe shows the row's own fields at once and fills
in the TOC details when `Info` returns. `Profiles` and `Sources` read
only config and don't wait.

**No polling.** The TUI never reads in the background, because each read
takes the profile lock, so a scheduled or terminal `knapsack update`
running at that moment would fail. It reloads, from config, state and
AddOns, without the network, after each job, after the editor closes,
on switching profile or view, and on `c`.

**Reporter.** A row's stage (queued, resolving, downloading with
bytes, installing, removing) comes from the events of the update or
remove running; a check reports the same steps, but its rows say
`checking` instead, so its events are dropped. When a job ends, the
status line says what it did, as the CLI's summary does: `forever: 1
upgraded, 1 failed`. `tui`'s `app.Reporter` sends each event to the program
(`Program.Send`, safe from any goroutine). `httpx` reports progress on
every read, so `EventProgress` is coalesced: the reporter keeps the
latest per addon and sends them on a 100 ms tick, as the CLI's bar
redraws (§8.6). Other events are sent as they happen. A `Watcher` does
the same for `List`'s check: `Listed` and `Finished` become messages.

**What's known.** `List` without its check gives the rows instantly. What
the network said, per profile and ref (status, available version,
error), is kept separately and laid over them, so a reload never forgets
a check, and nothing checks again without being asked:

| Event | What's known afterwards |
|---|---|
| Opening a profile | A check runs once per profile per session, queued after the instant list |
| `c` | All forgotten for the profile, then checked again |
| Installed or upgraded | Up to date, at the version installed |
| Removed | Forgotten |
| Failed | Unchanged, plus the error for describe |
| A row whose ref is gone after a reload (an edit, the CLI) | Forgotten |

Checking only when asked matters: GitHub allows 60 requests an hour
without a token, and Wago addons are downloaded just to compare (§7).
There's no check on a timer.

### 9.5 Modals and input

- **Update plan** (`U`, and `u` when a marked row isn't simply outdated):
  built from what's known, with no network. Update available is an
  upgrade, not installed an install, not in config a prune candidate,
  left unchecked so it stays installed, as `update` without `--prune`
  leaves it. Unchecked or unknown rows are listed as `checked when
  updating`. A Force box sets `UpdateOptions.Force`. `enter` runs
  `Update`, which resolves everything again, so the plan never acts on
  stale answers.
- **Add**: a text area with line numbers, one ref or URL per line, so a
  list can be pasted (a paste goes to the dialog, or to the filter while
  it's open). Each line is parsed with `ref.Parse` on `ctrl-s`; if any
  fails, the dialog stays open, says which lines and why (`line 2:
  invalid addon ref …`), and nothing runs, as a malformed ref is a usage
  error in the CLI (§8.6). `tab` moves to the Force box, which sets
  `Add`'s `force`. When some don't fit, the problems shown are cut,
  with how many more, so the Force box stays in view. An addon `Add`
  can't add opens the dialog again with only the failed refs, each with
  its error and hint, and Force as it was: a folder conflict's fix is a
  tab and a space away. An addon `Add` adopts (installed already, not in
  config) is left unchecked, since it's recorded at the version on
  disk. An empty profile says `a` adds one.
- **Set key**: `textinput` in password mode, so the key is never drawn,
  only a `•` for each character, so a paste can be seen to have landed.
  `app` validates it as `set-key` does (§8.4). The key is cleared from the
  model once the job is sent, and never appears in a message, the status
  line or an error.
- **Profile form**: path, name and flavor fields for `SetProfile`, which
  expands `~` and completes a client folder to `Interface/AddOns`, as
  `--path` does. Its errors and hints show in the form.
- **Confirm**: one question, `y` or `enter` for yes, `n` or `esc` for no.
  Removing lists the folders that will be deleted; deleting a profile
  says its addon files stay.

Every confirm is the TUI's own. The CLI's rule against prompts (§8.6) is
about scripts, which the TUI isn't for; it's why the CLI needs `--force`
and `--prune` flags, and the TUI asks instead.

### 9.6 Terminal, editor and browser

- **Editor.** `e` hands the terminal to the editor with `tea.Exec`, running
  `Options.Edit`, the same loop as `profiles edit` (`cli.editProfile`,
  on the TUI's App), so reopening on an invalid edit and keeping the
  copy behave the same. The TUI reloads when it returns, and shows its
  result in the status line, with what installs or uninstalls the
  addons the edit added or took out (`U`), or its error.
- **Signals.** `main` cancels its context on SIGINT, which a Ctrl-C typed
  in the editor sends. So `tui.Run` keeps the context's values but not
  its cancellation: in raw mode Ctrl-C is a key, and Bubble Tea gets
  SIGINT and SIGTERM from outside itself, ignoring them while the editor
  has the terminal, as the edit loop does. Bubble Tea would quit on
  them at once, so a filter (`tea.WithFilter`) turns that quit into
  the TUI's own, as Ctrl-C answered yes: a job that writes is cancelled
  and rolled back first, and a second signal quits at once. Knapsack
  then exits 2, as the CLI does when interrupted.
- **Browser.** `o` and `r` open a URL with the OS's handler (`open`,
  `xdg-open`, `rundll32 url.dll,FileProtocolHandler`), in a small file per
  OS, without waiting for it or letting it write on the terminal, and
  without cancelling it on quit, so `o` then `q` still opens the page. A
  ref's page comes from `ref.Ref.URL`; one that won't open is an error in
  the status line.
- **Ctrl-C** arrives as a key, since the terminal is in raw mode. With no
  job that changes anything, it quits, from anywhere, the filter
  included. With one, it asks, and Ctrl-C again answers yes: that
  cancels the job's context, waits for it to roll back as the CLI does,
  and quits. One more Ctrl-C while waiting quits at once, as the CLI's
  second signal does (§8.6). If the job finishes before the question is
  answered, the question goes.
- **Colors** adapt to light and dark terminals (Lip Gloss), and `NO_COLOR`
  turns them off; marks and statuses have glyphs, so nothing depends on
  color alone. The spinner uses `|/-\` in the Windows console, as the
  CLI's does.
- **Mouse**: not in v1.

### 9.7 Testing

- **Model tests**: send key and result messages to the root model, with a
  fake App, and compare what it asks for (which use case, with which
  arguments) and what it draws. Drawn screens are golden files at 80×24
  with colors off, regenerated with `-update` as the CLI's are (§10).
- **Reporter**: progress coalescing and concurrent `Event` calls, under
  `-race`.
- **Command**: `knapsack tui` refuses a non-terminal (a golden test in
  `cmd/knapsack`).
- No `teatest` or other harness: messages in, state and strings out, is
  all Bubble Tea's design needs.

---

## 10. Testing strategy

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

## 11. Linting and code quality

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
- `context.Context` is the first parameter of anything that does network I/O
  or can block waiting. Local file writes can't be cancelled, and locks never
  wait (§5.5), so `atomicfile` and `lock` don't take one.
- Interfaces are small and defined in the package that uses them, except
  `source.Source` (§4).
- Constructors take dependencies explicitly. There's no global HTTP client and
  no `init()`.
- No `panic` outside truly impossible states.
- Exported identifiers get doc comments. Package docs go in `doc.go`.
- Files are kept focused. Split when a file stops having one clear job.

---

## 12. CI and releases

**CI** (GitHub Actions, on push and PR), on Linux, Windows and macOS:

1. `go mod tidy` diff check
2. `golangci-lint run`
3. `go test -race ./...`
4. `go tool govulncheck ./...` (Linux only)
5. `go build ./...`

Running the test matrix on all three OSes matters: path handling, renames and
file locking differ, especially on Windows.

A separate **PR Title** check validates conventional-commit titles
(`docs/CONTRIBUTING.md`).

**Releases** use semantic versioning (`vMAJOR.MINOR.PATCH`; see
`docs/CONTRIBUTING.md` for bump rules and the pre-1.0 policy). A manually
triggered **Release Cut** workflow computes the next version from commit
types with git-cliff, previews the release notes, and creates the tag. It
runs only from `main`, only when CI passed on that commit, and refuses when
nothing releasable (no `feat`, `fix`, `perf`, `refactor`, `docs` or `revert`)
landed since the last tag. It defaults to a dry run. It then
calls the release workflow directly (`workflow_call`), since a tag pushed
with the workflow's own token doesn't trigger other workflows. The release
workflow runs goreleaser, which builds linux/windows/darwin × amd64/arm64,
generates checksums, and publishes a GitHub release with git-cliff's notes.
The notes live only in the release, not in a committed `CHANGELOG.md`, so
releasing never pushes a commit to `main`. Running the release workflow by
hand with an existing tag rebuilds that release.
The version is injected with `-ldflags` and shown by `knapsack version`.
Package-manager publishing (AUR, Homebrew, Scoop) comes later.

---

## 13. Milestones and PR plan

| # | Scope | Done when |
|---|---|---|
| **M0** | Skeleton, test harness, CI | CI green on all three OSes |
| **M1** | Leaf packages, config, profile commands | Profiles can be managed end to end from the CLI |
| **M2** | TOC rules, installer, state | A local zip installs atomically, with rollback and cleanup tests |
| **M3** | HTTP, GitHub source, `add`/`remove`/`list`/`info` | Installs the author's mirror repos into Forever |
| **M4** | `update`, `check`, `-A` | Daily-driver replacement for the LemonUp fork |
| **M5** | Release tooling, `v0.1.0` | Binaries downloadable for all three OSes |
| **M6** | CurseForge, Wago, ETag cache | Planned in detail after v0.1.0 |
| **M7** | `knapsack tui` | Addons, profiles and sources manageable from the TUI, as from the CLI |

### 13.1 PR conventions

Each PR is sized so one Claude session can build, test and review it
without losing track. That means one clear concern, usually one package or
one command group.

- **Size**: aim for under ~500 lines of non-test code. Tests may be larger.
  If a PR grows past that, split it rather than stretch it.
- **Self-contained**: `make lint test` passes, every new exported identifier
  has a doc comment, and nothing is half-wired. A package with no callers yet
  is fine; a command that does nothing isn't.
- **Spec first**: each PR lists the sections of this doc it implements. The
  doc is the spec. If implementation shows the design is wrong, the PR
  updates the doc in the same change and says so in its description.
- **Tests in the same PR** as the code, following §10. No "tests later" PRs.
- **Out of scope stays out**: each PR names what it must *not* do yet, to
  prevent scope creep.
- **Branch per PR**, squash-merged to `main` in order. A PR depends only on
  PRs before it in this list. Branch names, PR titles and versioning follow
  `docs/CONTRIBUTING.md`. Each PR below suggests a title.

### 13.2 PRs

#### M0 — Skeleton

**PR 1 — Module, root command and test harness**
- Title: `feat: add module skeleton and version command`
- Implements: §3 (tooling), §8.7 (Cobra conventions), §8.6 (exit codes)
- `go.mod` (`go 1.26`; `govulncheck` as a `tool` dependency), `cmd/knapsack` with `main.go` and `root.go`, `knapsack
  version` (version injected through `-ldflags`, `dev` otherwise), Makefile,
  `.golangci.yml` from §11, `.gitignore`.
- Test harness in `cmd/knapsack`: a helper that runs the root command with
  args and env, captures stdout, stderr and the exit code, and compares them
  with golden files (`-update` regenerates).
- Done when: `knapsack version` works and has a golden test; unknown commands
  exit 2 with a one-line error.
- Not yet: global flags beyond what `version` needs; any `internal/` package.

**PR 2 — CI**
- Title: `chore(ci): add test matrix and PR title check`
- Implements: §12 (CI only)
- GitHub Actions workflow: Linux, Windows and macOS matrix running the five
  CI steps. Cache Go modules and the build cache.
- Also: the PR Title workflow from `docs/CONTRIBUTING.md`, and repository
  settings allowing squash merges only, with the PR title as the commit message.
- Done when: CI is green on all three OSes for PR 1's code, and a badly
  formatted PR title fails the check.
- Not yet: release workflow.

#### M1 — Foundations and profiles

**PR 3 — `flavor`**
- Title: `feat(flavor): add flavor type and client data table`
- Implements: §5.7 (Flavor), §6.4 (client data table only)
- `Flavor`, `Parse`, `FromPath`, `Client()` and the data table: client
  folders, game type, family, Interface range, product code.
- Done when: table-driven tests cover every flavor, every client folder, and
  Windows and Unix paths in `FromPath`.
- Not yet: `ClientInterface` (`.build.info`), which comes in PR 9.

**PR 4 — `ref`**
- Title: `feat(ref): parse and normalize addon refs and urls`
- Implements: §5.7 (Ref), §8.5 (refs, URLs, normalization)
- `Parse` for `source:id` and GitHub, CurseForge and Wago URLs;
  normalization (lower-cased GitHub IDs, trailing slashes, `.git`);
  `String`, `ShortName`.
- Done when: table tests cover valid and invalid input for each source, and
  a fuzz test checks `Parse(r.String())` round-trips.
- Not yet: resolving short names against installed addons (that needs state).

**PR 5 — `paths`, `atomicfile`, `lock`**
- Title: `feat: add paths, atomic writes and locking`
- Implements: §5.1 (directories, `KNAPSACK_HOME`), §5.3 (atomic writes),
  §5.5 (locking)
- Three small leaf packages. `lock` returns a sentinel `ErrLocked` when
  another process holds the lock; it doesn't know about user-facing text.
- Done when: tests cover `KNAPSACK_HOME` overrides, atomic-write failure
  leaving the old file intact, and a second lock attempt failing fast.
- Not yet: anything that uses them.

**PR 6 — `config` store**
- Title: `feat(config): load, validate and save config.toml`
- Implements: §5.2
- `config.Config` types, `Store` with `Load`/`Save` (through `atomicfile`),
  validation (known flavors, valid refs, existing current profile), env-var
  precedence for API keys, `0600` when a key is present, stable key order.
- Done when: round-trip tests, a golden test of the written TOML, and tests
  for every validation error.
- Not yet: profile commands.

**PR 7 — `app` scaffolding and `config` commands**
- Title: `feat(config): add profile commands`
- Implements: §6.5 (`New`, `Options`), §6.6 (`UserError`), §8.1 (profile
  commands), §8.3 (`-o`, `-v`, `--config`), §8.6 (error rendering)
- `app.New`, profile use cases (set, list, use, current, delete, view),
  `UserError`, error rendering and exit codes in `main`, the six
  `config` subcommands with table and `-o json` output.
- Done when: golden tests for every subcommand, including errors with hints
  (unknown profile, flavor not inferable from the path).
- Not yet: addon commands; `-p`/`KNAPSACK_PROFILE`, which land with the
  first commands that act on a profile (PR 16); state files
  (`delete-profile` removing a state file is added in PR 10).

#### M2 — Installing from a local zip

**PR 8 — `toc`**
- Title: `feat(toc): parse toc files`
- Implements: §6.4 (parsing)
- `toc.Parse`: headers, Interface lists, repeated
  `AllowLoadGameType`/`ExcludeLoadGameType`, BOM, CRLF.
- Done when: table tests from real-world TOCs in `testdata/` (including
  Baganator's multi-Interface TOC) and a fuzz test that never panics.
- Not yet: choosing between TOC files or validating them.

**PR 9 — Client interface and TOC validation**
- Title: `feat(install): validate addons against the game client`
- Implements: §6.4 (steps 1 and 2, `.build.info`)
- `flavor.ClientInterface` reading `.build.info`; TOC selection (game type,
  then family, then plain); the validation rules table, as
  `install.Validate(folderPath, flavor, clientInterface)`.
- Done when: one test case per row of the validation table, TOC selection
  precedence tests, and a `.build.info` fixture test (resolves open question
  1 for the clients there are fixtures for).
- Not yet: extraction or installing.

**PR 10 — `state` store**
- Title: `feat(state): load and save per-profile state`
- Implements: §5.3, §5.4
- `state.State` types, `Store` with `Load`/`Save`/`Delete`, schema
  version check. Wires `profiles delete` to remove the state file.
- Done when: round-trip and golden TOML tests, an unknown schema version
  gives a clear error.
- Not yet: anything writing state after an install.

**PR 11 — Extraction**
- Title: `feat(install): extract archives safely into staging`
- Implements: §6.3 (steps 2–3)
- Extract a zip into a staging folder: zip-slip, absolute path and symlink
  rejection; wrapper-folder normalization.
- Done when: tests build zips in-test covering each rejection and each
  normalization case.
- Not yet: swapping into AddOns.

**PR 12 — Install, swap, rollback, cleanup**
- Title: `feat(install): swap folders atomically with rollback`
- Implements: §6.3 (steps 1, 4–7, `Install`, `Remove`, `Cleanup`, Windows
  retries)
- `install.Install` tying extraction, validation, conflict checks and the
  swap together; rollback on failure; `Cleanup` of leftovers; `Remove`.
- Done when: tests for multi-folder addons, conflict refusal and `Force`,
  rollback after an injected failure mid-swap, and cleanup of leftovers.
  Passes on Windows CI.
- Not yet: HTTP or the app layer.

#### M3 — GitHub and the first addon commands

**PR 13 — `httpx`**
- Title: `feat(httpx): add http client and verified downloads`
- Implements: §6.2
- `NewClient` (timeouts, User-Agent, limited retries) and `Download` (stream
  to a file, SHA-256 verify).
- Done when: `httptest` tests for checksum match and mismatch, truncated
  bodies, retry limits and cancellation.
- Not yet: any source.

**PR 14 — `source` and GitHub**
- Title: `feat(source): add source registry and github source`
- Implements: §6.1, §7 (GitHub resolution)
- `Source` interface, `Release`, `Registry`, sentinel errors, and
  `source/github` with asset selection and digests. The token is a
  parameter; discovering it is wiring, so it lands with the first commands
  that need it (PR 16).
- Done when: fixture-based tests for each asset-selection rule, 404, 401,
  rate limiting, and a missing digest.
- Not yet: CurseForge and Wago.

**PR 15 — `app` addon use cases**
- Title: `feat(app): add, remove, list and describe addons`
- Implements: §6.5 (`Add`, `Remove`, `List`, `Info`, `Reporter`), §5.4
  (write order), §5.5 (locking), §8.4 and §8.5 (behavior and short names)
- Use cases with locking, cleanup before each operation, the fixed write
  order, short-name resolution, and per-addon results.
- Done when: `app` tests with a fake source on a temp `KNAPSACK_HOME`, covering
  partial failures, adopting an addon that's in state but not config, and
  lock contention.
- Not yet: CLI commands; `update` and `check`.

**PR 16 — `add`, `remove`, `list`, `info` commands**
- Title: `feat: add add, remove, list and info commands`
- Implements: §8.1, §8.4, §8.6 for these four commands, and §8.3 `-p` with
  `KNAPSACK_PROFILE`
- GitHub token discovery in `cmd/knapsack` (`GITHUB_TOKEN`, `GH_TOKEN`,
  `gh auth token`), passed to `github.New`. Only commands that download
  build the sources, so offline commands never run `gh`.
- Commands, `rm`/`ls` aliases, table/`wide`/`json` output, the CLI
  `Reporter` (lines, or a progress bar on a TTY).
- Done when: golden tests for each command, including errors with hints and
  `-o json`; a manual run installs the author's mirror repos into Forever.
- Not yet: `update`, `check`, `-A`.

#### M4 — Bulk updates

**PR 17 — `app` `Update` and `Check`**
- Title: `feat(app): update and check addons in bulk`
- Implements: §6.5 (`Update`, `Check`, `Selector`, concurrency), §5.3
  (identity comparison), §8.4 (`update`, `check`, `--prune`, `--force`)
- Parallel resolve and download, serial installs, `--prune`, multi-profile
  `Selector`.
- Done when: `app` tests for install-missing, upgrade, no-op when the
  SHA-256 matches, prune, partial failure, and several profiles. Passes with
  `-race`.
- Not yet: CLI commands.

**PR 18 — `update` and `check` commands, `-A`**
- Title: `feat: add update and check commands`
- Implements: §8.1, §8.2, §8.6 (`check` exit code 1)
- Commands, `-A/--all-profiles` on `update`, `check` and `list`, output
  grouped by profile with summaries.
- Done when: golden tests for single-profile and `-A` output, the exit codes
  of `check`, and flag conflicts (`-A` with `-p`).
- Followed by `feat(cli): show download progress on a terminal`: the
  progress bar, with byte counts from `httpx`.
- Followed, after v0.1.0, by `feat(cli): list unmanaged folders`:
  `list --unmanaged`, and the note when they're hidden (§8.4).
- Then `feat: add self-update and the update check`: `self-update`, the
  daily check and `version`'s (§8.4).

#### M5 — First release

**PR 19 — Release**
- Title: `chore: add release cut and goreleaser`
- Implements: §12 (releases)
- `goreleaser` as a `tool` dependency (deferred from PR 1: it pulls in
  hundreds of modules and nothing needs it earlier), in `tools/go.mod`
  because adding it to `go.mod` raised the `go` directive to 1.27.4,
  `cliff.toml`, the Release Cut workflow (version bump from commit types,
  release notes, tag, dry-run option), the release workflow it calls
  (`.goreleaser.yaml`), and install instructions and usage in the README.
- Done when: a `v0.1.0` tag produces binaries for linux/windows/darwin ×
  amd64/arm64 with checksums.

#### M7 — TUI

**PR 20 — What the TUI shares with the CLI**
- Title: `refactor(cli): share editing and path display with the tui`
- Implements: §9.3, §9.6 (`ref.Ref.URL`)
- `cli.edit` takes a context instead of a `*cobra.Command`, and
  `profiles edit`'s body becomes `cli.editProfile`, which `Options.Edit`
  will call; `shortPath` becomes `app.ShortenHome`, beside `ExpandHome`;
  the `report:` link becomes `app.ReportURL`; `ref.Ref.URL` gives each
  source's page for a ref, one `ref.Parse` reads back.
- Done when: the CLI's golden tests pass unchanged; tests for each
  source's URL, `ShortenHome` and `ReportURL`.
- Not yet: `internal/tui`.

**PR 21 — `knapsack tui`, read-only**
- Title: `feat(tui): add knapsack tui with the addons view`
- Implements: §3 (Bubble Tea, Bubbles, Lip Gloss), §9.1, §9.2 (global
  keys, addons view without actions, describe), §9.3, §9.4 (the job
  queue, `Watcher`, what's known), §9.6 (Ctrl-C, colors), §9.7
- The command, layout, instant list then the check, describe, filter,
  help, `c` and quitting. With no profiles, the screen names the
  commands that make one, until the profiles view.
- Done when: golden screens for a checked profile, a check in progress,
  an empty profile, no profiles and a too-small window; the command
  refuses a non-terminal.
- Not yet: any action that changes anything; the profiles and sources
  views, so `1`–`3` and `tab` come with them.

**PR 22 — Updating and removing**
- Title: `feat(tui): update and remove addons`
- Implements: §9.2 (`u`, `U`, `ctrl-d`, marks), §9.4 (the Reporter,
  progress, what's known after a job), §9.5 (plan, confirm)
- Done when: model tests for queued jobs, progress coalescing under
  `-race`, the plan built from what's known, remove's confirm, and a
  failed addon's error in describe.
- Not yet: adding.

**PR 23 — Adding, and opening pages**
- Title: `feat(tui): add addons and open their pages`
- Implements: §9.2 (`a`, `o`, `r`), §9.5 (add), §9.6 (browser)
- Done when: the add dialog keeps unparsable lines with their errors and
  runs nothing; `o` asks for the ref's URL.

**PR 24 — Profiles view**
- Title: `feat(tui): manage profiles`
- Implements: §9.2 (profiles, view switching), §9.5 (profile form),
  §9.6 (editor)
- Done when: model tests for use, create, set, auto, delete and edit,
  with the editor faked; the no-profiles screen leads here.

**PR 25 — Sources view**
- Title: `feat(tui): manage sources`
- Implements: §9.2 (sources), §9.5 (set key)
- Done when: the key never appears in a drawn screen or a message;
  GitHub's row explains why its keys don't apply.
- Followed by `docs: describe knapsack tui in the readme`.

---

## 14. Open questions

1. **`.build.info` product codes**: `wowf` (Forever) is confirmed from a
   real install, which is the fixture in `internal/flavor/testdata`. The
   other codes come from WowUp and still need a real file to confirm, as do
   the test clients' folders and codes; Anniversary's code is unknown, so
   its out-of-date check is skipped, and Anniversary and Forever have no
   known test clients.
2. **Importing existing installs**: should there be a one-time
   `knapsack import` from LemonUp's state, or does `add` for each addon cover
   it?
3. **Pinning**: is `github:owner/repo@v1.2.3` worth supporting in v1, or
   should it wait until someone needs it?
4. **Dependencies**: TOCs declare `## Dependencies`. Should Knapsack warn
   about missing ones? (Leaning: warn only, never auto-install.)
