# AGENTS.md

## Project Overview

`proxemby` is a Rust project (tokio + hyper). Performance and memory use are core goals: avoid extra copies, allocations and buffering on the proxy path. Requirements are still being gathered, so keep the initial structure small and avoid speculative abstractions.

## Development Guidelines

- Prefer standard Cargo tooling and idiomatic module structure.
- Keep changes narrowly scoped to the current request.
- Run `cargo fmt` and `cargo clippy --all-targets` before finishing.
- Use `cargo test` when tests or Rust code are added.
- Do not add external dependencies unless they are clearly needed.

## Commits and Pull Requests

- Add a changeset (`npx changeset`) for every user-facing change; see `.changeset/README.md`. Do not edit versions or `CHANGELOG.md` by hand.
- Never add AI attribution to commits or pull requests: the commit author must be the repository owner (not `Claude <noreply@anthropic.com>`), and there must be no `Co-Authored-By: Claude ...`, `Claude-Session:`, "Generated with Claude Code" or similar lines in commit messages, PR titles, PR bodies or code comments.

## Repository Notes

- Crate name: `proxemby` (library in `src/lib.rs`, binary in `src/main.rs`).
- Integration tests live in `tests/` and run the proxy against local mock upstreams.
- Release builds target `x86_64-unknown-linux-musl`.
- `package.json` exists only for Changesets; its version is synced into `Cargo.toml` and `Cargo.lock` by `scripts/sync-version.mjs`.
