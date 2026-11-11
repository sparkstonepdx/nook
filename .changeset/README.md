# Changesets

Every PR that should reach servers adds one file here: `npx changeset` asks what kind of change it is and
for a line for the changelog. Or write it by hand:

```md
---
"nook": minor
---

Deploy tokens can now push secrets without deploying.
```

- `patch`: fixes. `minor`: new features. `major`: something that stops working for existing servers or
  deploy workflows.
- When a PR with a changeset merges, the release workflow combines all pending changesets, bumps the
  version in package.json, writes CHANGELOG.md, commits that to main, and publishes the signed release.
  Servers see it at their next update check (or the dashboard's Check now).
- PRs without a changeset (docs, CI, refactors) don't release anything.
