# Knapsack

A small, cross-platform command-line addon manager for World of Warcraft.

Knapsack installs, updates and removes addons for each of your game clients
(Retail, Classic, Classic Era, Anniversary, Forever) from GitHub releases
and Wago Addons, with CurseForge planned. It aims for simplicity and refinement over
features: it checks that a release is really built for your client before
installing it, only ever deletes folders it installed, and rolls back an
install that fails halfway.

## Install

Download the archive for your system from the
[latest release](https://github.com/austinrr/knapsack/releases/latest),
extract it, and put `knapsack` (`knapsack.exe` on Windows) somewhere on your
`PATH`. Builds are for Linux, Windows and macOS, on amd64 and arm64.
`checksums.txt` has each archive's SHA-256.

With Go 1.26 or newer, you can build it instead:

```sh
go install github.com/austinrr/knapsack/cmd/knapsack@latest
```

Check it works with `knapsack version`.

### Updating Knapsack

`knapsack self-update` replaces Knapsack with the newest release, after
checking the download's SHA-256. Once a day, any command run in a terminal
checks for a newer release and says so on stderr; `knapsack version`
checks every time. Set `KNAPSACK_NO_UPDATE_NOTIFIER=1` to turn the check
off. If you installed with `go install`, run it again with `@latest`
instead.

## Getting started

Let Knapsack find the game and make a profile for each client it has
(`retail`, `forever`, …):

```sh
knapsack config set-profile --auto
```

It looks where the game is usually installed: `Program Files (x86)` and the
like on every Windows drive, `/Applications` on macOS, and Wine, Lutris,
Faugus, Bottles and Steam prefixes on Linux. If it isn't found, pass the
`World of Warcraft` folder with `--path`, or make each profile yourself.

Or make a profile for each game client yourself, pointing at its client folder
(`_retail_`, `_forever_`, …) or the `Interface\AddOns` folder in it. The
flavor is worked out from the client folder, the profile is named after
it, and the first profile becomes the current one. Quote the path: it
usually has spaces in it.

```powershell
knapsack config set-profile --path "C:\Program Files (x86)\World of Warcraft\_retail_"
knapsack config set-profile --path "C:\Program Files (x86)\World of Warcraft\_forever_"
knapsack config use-profile forever    # switch between them
```

On macOS the game is usually in `/Applications/World of Warcraft`; on
Linux, inside the Wine prefix your launcher made, such as
`"$HOME/Games/battlenet/drive_c/Program Files (x86)/World of Warcraft"`.

A second profile for the same client, such as a test copy of the game,
needs its own name: `knapsack config set-profile forever-test --path …`.

Then add addons by ref or GitHub URL:

```sh
knapsack add github:austinrr/baganator-mirror
knapsack add https://github.com/austinrr/syndicator-mirror -p retail
```

And keep them up to date:

```sh
knapsack list            # each addon, and whether it's up to date
knapsack check           # what update would do; exits 1 if anything is pending
knapsack update          # upgrade outdated addons, install missing ones
knapsack update -A       # every profile
```

If the AddOns folder already has addons, Knapsack leaves them alone:
`list` says how many folders it doesn't manage, and `list --unmanaged` shows
them. To have Knapsack take one over, `add` it with `--force`, which
replaces the folder with the release.

`update` makes a profile match its config, so on a new machine,
`config set-profile` plus `update` reinstalls everything. Releases are
compared by checksum, not version number.

## Commands

| Command | What it does |
|---|---|
| `add <ref\|url>...` | Add addons to the profile and install them |
| `remove <addon>...` (`rm`) | Uninstall addons, deleting only the folders Knapsack installed |
| `update [addon...]` | Upgrade outdated addons, and install any in config that aren't installed; `--prune` also removes addons no longer in config |
| `check [addon...]` | Show what `update` would do, without changing anything |
| `list` (`ls`) | List the profile's addons by title, with whether each is up to date and its source. `--no-check` skips the network; `--unmanaged` also lists folders Knapsack didn't install |
| `info <addon>` | Show one addon's version, folders, install time and TOC details |
| `config set-profile [name] --path <AddOns>` | Create or change a profile, named after its flavor unless you name it |
| `config get-profiles`, `use-profile`, `current-profile`, `delete-profile`, `view` | Manage profiles, like kubectl contexts |
| `sources list`, `set-key`, `unset-key` | List addon sources and where their API keys come from, and manage keys for every profile |
| `version` | Print Knapsack's version, and say if there's a newer one |
| `self-update` | Update Knapsack to the newest release |

An addon can be named by its ref (`github:austinrr/baganator-mirror`,
`wago:<id>`), its GitHub or addons.wago.io link, or its short name (`baganator-mirror`) when only one addon matches.
`knapsack <command> --help` shows every flag.

### Common flags

| Flag | Meaning |
|---|---|
| `-p, --profile <name>` | Act on this profile instead of the current one (or `$KNAPSACK_PROFILE`) |
| `-A, --all-profiles` | Act on every profile (`update`, `check`, `list`) |
| `-o, --output <format>` | `json`, or `wide`: the full ref as the source in `list` and `check`, the full AddOns path in `config get-profiles` |
| `--force` | Replace folders in AddOns that no addon owns (`add`, `update`) |
| `-v, --verbose` | Show each step and the underlying cause of errors |
| `--config <file>` | Use another config file |

Exit codes: `0` success, `1` from `check` when changes are pending, `2` on
any error.

### Sources and API keys

`knapsack sources list` shows where addons come from and whether each is ready.
GitHub allows 60 anonymous API requests an hour. Knapsack uses a token
from `GITHUB_TOKEN`, `GH_TOKEN` or the [GitHub CLI](https://cli.github.com/)
(`gh auth login`) when there is one, which raises that to 5,000. It never
stores the token.

Wago Addons, and CurseForge once it's supported, need an API key, shared
by every profile. Wago's is on your
[account's API keys page](https://addons.wago.io/account/apikeys):

```sh
knapsack sources set-key wago                 # asks for it
pass show wago | knapsack sources set-key wago  # or pipe it in
knapsack add https://addons.wago.io/addons/plater
```

`KNAPSACK_WAGO_API_KEY` and `KNAPSACK_CURSEFORGE_API_KEY` override the
stored keys. Wago publishes no checksums, so `check`, and `list` unless
`--no-check`, download each Wago addon to tell whether it changed.

## Where files live

| | Linux | Windows | macOS |
|---|---|---|---|
| Config and state | `~/.config/knapsack` | `%AppData%\knapsack` | `~/Library/Application Support/knapsack` |
| Download cache | `~/.cache/knapsack` | `%LocalAppData%\knapsack` | `~/Library/Caches/knapsack` |

`config.toml` holds your profiles and their addons and is safe to edit by
hand. `state/` records what's installed in each profile. Set `KNAPSACK_HOME`
to keep everything under one folder instead.

## Contributing

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for branch names, PR titles,
versioning and releases, and [docs/DESIGN.md](docs/DESIGN.md) for the
architecture.

## License

[MIT](LICENSE)
