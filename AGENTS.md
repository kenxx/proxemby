# AGENTS.md

## Project Overview

`proxemby` is a Rust project (tokio + hyper). Performance and memory use are core goals: avoid extra copies, allocations and buffering on the proxy path. Requirements are still being gathered, so keep the initial structure small and avoid speculative abstractions.

## Development Guidelines

- Prefer standard Cargo tooling and idiomatic module structure.
- Keep changes narrowly scoped to the current request.
- Run `cargo fmt` and `cargo clippy --all-targets` before finishing.
- Use `cargo test` when tests or Rust code are added.
- Do not add external dependencies unless they are clearly needed.

## Repository Notes

- Crate name: `proxemby` (library in `src/lib.rs`, binary in `src/main.rs`).
- Integration tests live in `tests/` and run the proxy against local mock upstreams.
- Release builds target `x86_64-unknown-linux-musl`.
