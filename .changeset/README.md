# Changesets

Every user-facing change needs a changeset. Create one with:

```sh
npx changeset
```

Pick `patch`, `minor` or `major` for `proxemby` and describe the change for
the release notes. Commit the generated Markdown file with your change.

When changes land on `main`, the Changesets workflow opens a release pull
request that bumps the version in `package.json`, `Cargo.toml` and
`Cargo.lock` and updates `CHANGELOG.md`. Merging that pull request tags the
release and builds the release artifacts and Docker image.
