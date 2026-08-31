# Contributing to skillsctl

Thanks for taking the time to contribute. This file covers how to get set up
and submit a change; [AGENTS.md](AGENTS.md) is the source of truth for build
commands, architecture, and code conventions — read it before writing code.

## Getting set up

```bash
mise install      # pins go, golangci-lint, goreleaser to the versions CI uses
make build         # go build -o skillsctl ./cmd/skillsctl
```

## Before opening a pull request

```bash
make test         # go test -race -cover ./...
make lint         # golangci-lint run
make tidy-check   # fails if go.mod/go.sum are out of date
```

All three must pass. If your change affects a user-visible command, flag, or
output, update `README.md` in the same PR — see AGENTS.md's
[Keeping the README current](AGENTS.md#keeping-the-readme-current) section.

## Commit messages and PR titles

This repository requires [Conventional Commits](https://www.conventionalcommits.org/)
for every commit and every PR title, since the PR title becomes the
squash-merge subject and drives the release changelog. See AGENTS.md's
[Commit messages](AGENTS.md#commit-messages) section for the format, allowed
types, and real examples from this repository's history.

## Reporting bugs and requesting features

Use the issue templates offered when you open a
[new issue](https://github.com/richardcase/skillsctl/issues/new/choose).

## Reporting a vulnerability

Do not open a public issue for a security vulnerability — see
[SECURITY.md](SECURITY.md).

## Design reference

`docs/superpowers/specs/2026-08-13-skillsctl-design.md` holds the intended
design: channels, store layout, receipt model, and full CLI surface. Several
unbuilt commands are already specified there and should be built as
specified.
