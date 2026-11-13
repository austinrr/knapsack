# Knapsack

A small, cross-platform command-line addon manager for World of Warcraft.

Knapsack installs, updates and removes addons for each of your game clients
(Retail, Classic, Classic Era, Forever) from GitHub, with CurseForge and Wago
planned. It aims for simplicity and refinement over features.

> **Status:** in design. Nothing is installable yet. See
> [docs/DESIGN.md](docs/DESIGN.md) for the architecture and plan.

## Planned usage

```sh
knapsack config set-profile forever --path ~/Games/wow/_forever_/Interface/AddOns
knapsack add github:austinrr/baganator-mirror
knapsack check -A        # what's outdated, across every profile
knapsack update -A       # update everything, everywhere
```

## Contributing

See [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md) for branch names, PR titles
and versioning.

## License

[MIT](LICENSE)
