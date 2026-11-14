Trimmed `GET /repos/{owner}/{repo}/releases/latest` responses from real
releases, keeping only the fields Knapsack reads. `packaged.json` is made up,
in the shape the BigWigs packager produces for a multi-client addon.
