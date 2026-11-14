# Contributing

## Workflow

1. Branch from `main` (see [Branch naming](#branch-naming)).
2. Open a PR. **The PR title becomes the commit message**: it lands on `main`
   as written when the PR is squash-merged.
3. CI checks the PR title format, along with lint and tests. If the title
   check fails, edit the title on GitHub and CI re-runs.
4. Merge with **Squash and merge**, the only merge method allowed.

WIP commits on a branch (`fix lint`, `address review`) are fine. They're
squashed away.

PRs follow the plan and conventions in [DESIGN.md §12](DESIGN.md#12-milestones-and-pr-plan).

## PR title format

```
<type>(<scope>): <short description>
```

- **`<type>`** is required, from the table below.
- **`<scope>`** is optional: the package or command the change is about,
  lowercase (`flavor`, `install`, `update`, `ci`). Leave it out when the
  change spans several areas.
- **`<short description>`** is imperative, lowercase, with no trailing
  period, and the whole title is at most 70 characters.

Add `!` before the colon to mark a breaking change:
`feat(config)!: rename default_profile to current_profile`.

| Type | When to use |
|---|---|
| **`feat`** | A new user-visible feature or capability |
| **`fix`** | A bug fix |
| **`perf`** | A performance improvement with no behavior change |
| **`refactor`** | Restructuring with no behavior or performance change |
| **`docs`** | Documentation only: README, `docs/`, code comments |
| **`test`** | Adding or fixing tests with no production code change |
| **`chore`** | Tooling, CI, builds, dependency bumps, lint config |
| **`revert`** | Reverts an earlier commit; the body names the reverted SHA |

One PR is one commit with one type. If a fix is needed for a feature to
work, the PR is a `feat`. If the fix could ship on its own, split it out.

Examples:

```
feat(flavor): add flavor type and client data table
feat(install): roll back a failed swap
fix(toc): accept a UTF-8 byte order mark
chore(ci): run tests on windows and macos
docs: describe TOC validation rules
feat(config)!: rename default_profile to current_profile
```

## Branch naming

`<type>/<short-slug>`, using the same type as the PR title. The slug is
lowercase words separated by hyphens.

```
feat/flavor-package
feat/install-swap-rollback
fix/toc-bom
chore/ci-matrix
docs/pr-plan
```

## Versioning

Releases use **semantic versioning**: `vMAJOR.MINOR.PATCH`.

| Bump | When | Example |
|---|---|---|
| **Major** | A breaking change to the CLI, config or state format (`!` in a title) | `v1.4.2` → `v2.0.0` |
| **Minor** | New features or notable behavior changes (`feat`) | `v1.4.2` → `v1.5.0` |
| **Patch** | Fixes, dependency bumps, internal-only changes | `v1.4.2` → `v1.4.3` |

**Before 1.0**, breaking changes bump the minor version instead
(`v0.3.1` → `v0.4.0`) and everything else bumps the patch. `v1.0.0` ships
once the CLI grammar, config format and state format are stable.

A breaking change to the state format must ship with an automatic migration
(the `version` field in §5.3 of the design), so upgrading never strands an
install.

Go requires a module path ending in `/v2` for major versions 2 and up. If
Knapsack reaches v2, the module path changes to
`github.com/austinrr/knapsack/v2` in the same release.

## Releases

Releases are cut manually from GitHub Actions:

1. Run the **Release Cut** workflow from `main`. It works out the next
   version from the commit types since the last tag (or takes an explicit
   bump), previews the release notes from
   [git-cliff](https://git-cliff.org/) in the run summary, and creates an
   annotated tag. It's a dry run unless you untick **Only preview**, and it
   refuses to run if CI didn't pass on that commit or nothing releasable
   landed since the last tag (`test` and `chore` commits don't count, unless
   they're breaking).
2. The same run calls the release workflow, which builds binaries with
   goreleaser and publishes a GitHub release with those notes. To rebuild a
   release, run the **Release** workflow by hand with its tag.

`make snapshot` builds every release archive into `dist/` locally without
publishing, and `make release-check` validates `.goreleaser.yaml`.

Because the changelog is built from PR titles, a well-formed title becomes
a clean, grouped release-notes entry. One that isn't conventional is left
out of the notes and doesn't count toward the version bump, which is why
CI checks titles before merge.
