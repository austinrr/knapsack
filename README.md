# Knapsack

A small, cross-platform command-line addon manager for World of Warcraft.

Knapsack installs, updates and removes addons for each of your game clients
(Retail, Classic, Classic Era, Anniversary, Forever) from GitHub releases,
with CurseForge and Wago planned. It aims for simplicity and refinement over
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

## Getting started

Make a profile for each game client, pointing at its `Interface/AddOns`
folder. The flavor is worked out from the client folder (`_retail_`,
`_forever_`, …), and the first profile becomes the current one.

```sh
knapsack config set-profile forever --path "$HOME/Games/World of Warcraft/_forever_/Interface/AddOns"
knapsack config set-profile retail  --path "$HOME/Games/World of Warcraft/_retail_/Interface/AddOns"
```

Then add addons by ref or GitHub URL:

```sh
knapsack add github:austinrr/baganator-mirror
knapsack add https://github.com/austinrr/syndicator-mirror -p retail
```

And keep them up to date:

```sh
knapsack check           # what update would do; exits 1 if anything is pending
knapsack update          # install missing addons, upgrade outdated ones
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
| `update [addon...]` | Install missing addons and upgrade outdated ones; `--prune` also removes addons no longer in config |
| `check [addon...]` | Show what `update` would do, without changing anything |
| `list` (`ls`) | List the profile's addons and whether each is installed; never uses the network. `--unmanaged` also lists folders Knapsack didn't install |
| `info <addon>` | Show one addon's version, folders, install time and TOC details |
| `config set-profile <name> --path <AddOns>` | Create or change a profile |
| `config get-profiles`, `use-profile`, `current-profile`, `delete-profile`, `view` | Manage profiles, like kubectl contexts |
| `version` | Print Knapsack's version |

An addon can be named by its ref (`github:austinrr/baganator-mirror`), its
URL, or its short name (`baganator-mirror`) when only one addon matches.
`knapsack <command> --help` shows every flag.

### Common flags

| Flag | Meaning |
|---|---|
| `-p, --profile <name>` | Act on this profile instead of the current one (or `$KNAPSACK_PROFILE`) |
| `-A, --all-profiles` | Act on every profile (`update`, `check`, `list`) |
| `-o, --output <format>` | `json`, or `wide` for `list` |
| `--force` | Replace folders in AddOns that no addon owns (`add`, `update`) |
| `-v, --verbose` | Show each step and the underlying cause of errors |
| `--config <file>` | Use another config file |

Exit codes: `0` success, `1` from `check` when changes are pending, `2` on
any error.

### GitHub rate limits

GitHub allows 60 anonymous API requests an hour. Knapsack uses a token
from `GITHUB_TOKEN`, `GH_TOKEN` or the [GitHub CLI](https://cli.github.com/)
(`gh auth login`) when there is one, which raises that to 5,000. It never
stores the token.

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
