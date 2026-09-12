# Contributing to ocman

Ocman's primary repository and CI are on
[Forgejo](https://forgejo.nousefreak.be/dries/ocman). GitHub hosts a mirror.
Please submit pull requests to Forgejo and check its
[issue tracker](https://forgejo.nousefreak.be/dries/ocman/issues) before
opening a new issue on either host.

## Reporting bugs and requesting features

For bugs, include steps to reproduce, expected and actual behavior, your
ocman and OpenCode versions, OS, and browser. Mention whether you use the
web UI or desktop app and whether a remote machine is involved. Include
relevant logs or screenshots, with tokens and private session content removed.

For feature requests, describe the problem you want to solve, your proposed
behavior, and any workarounds you have tried. Discuss larger changes in an
issue before implementing them.

## Local development

Install Go at the version required by [go.mod](go.mod),
[mise](https://mise.jdx.dev/), and OpenCode. Run `mise install` to install
the tools pinned in [mise.toml](mise.toml), including Node.js, pnpm, air,
and golangci-lint. Install `tmux` for managed OpenCode launches and terminals.
Run OpenCode once to create its local database before starting ocman.

Fork the repository, clone your fork, and create a branch from the latest
`main`. From the repository root:

```sh
mise install
pnpm --dir frontend install --frozen-lockfile
make dev
```

Open http://localhost:8228. The backend runs on port 8229.
Use pnpm for all frontend commands.

See the [development guide](docs/other/contributing.md) for the project
layout and workflows, and [AGENTS.md](AGENTS.md) for repository conventions.
The [Makefile](Makefile) is the authoritative command reference.

## Before submitting a pull request

- Keep each pull request focused on one change and link the related issue.
- Follow the surrounding code's naming and structure. Keep the frontend
  platform-agnostic and route host operations through `hostsvc.Host`.
- Add tests for new behavior. For bug fixes, first reproduce the failure in
  a regression test, then verify that the fix makes it pass. Test coverage
  must not drop.
- Update user-facing documentation in `docs/` when behavior changes. Update
  [architecture diagrams](docs/other/architecture.md) when their components
  or data flows change.
- Describe what changed and how you verified it. Include screenshots for
  visible UI changes.

Run these checks from the repository root:

```sh
make test        # Go and frontend tests
make lint        # Go and frontend linters, typecheck, repository guards
```

Run `make test-e2e` for changes to browser workflows and `make build` for
build or packaging changes. CI runs on Forgejo and also checks coverage.
Documentation-only changes need a review of formatting, links, and commands.
