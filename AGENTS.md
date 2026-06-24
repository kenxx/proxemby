# AGENTS.md

## Project Overview

`proxemby` is a Go project. Requirements are still being gathered, so keep the initial structure small and avoid speculative abstractions.

## Development Guidelines

- Prefer standard Go tooling and idiomatic package structure.
- Keep changes narrowly scoped to the current request.
- Run `gofmt` on modified Go files before finishing.
- Use `go test ./...` when tests or Go code are added.
- Do not add external dependencies unless they are clearly needed.

## Repository Notes

- Module name: `proxemby`
- Go version is managed by `go.mod`.
- This repository currently contains only the project bootstrap files.
