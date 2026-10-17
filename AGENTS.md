# Project instructions

Ocman is a Go backend and React/TypeScript dashboard for OpenCode and Claude
Code sessions. It supports local and remote machines through shared adapters.

## Rules for every task

- This repository is hosted on Forgejo. Use `tea`, not `gh`, for issues and
  pull requests. Pass `--repo dries/ocman` when auto-detection is unavailable.
- Keep Git history linear. Fetch and rebase instead of merging. Never create
  merge commits, use `git pull` without `--rebase`, use `--rebase-merges`, or
  force-push without explicit permission. Integrate PRs by squash, rebase or
  fast-forward only.
- Never run development code against live, shared or another checkout's writable
  databases. Use disposable databases isolated to this project and worktree or
  branch, verify the effective paths before startup, and use temporary databases
  in tests. A different port is not database isolation. Read the development
  guide before starting any process that can write data or apply migrations.
- Use `pnpm` for Node commands, not `npm`. Versions are pinned in `mise.toml`
  and `frontend/package.json`.
- Keep changes small and consistent with surrounding code. New behavior needs
  tests; bug fixes need a regression test observed failing before the fix.
  Coverage must not drop. Do not lower the coverage baseline.
- Route session work through `platforms.Platform` and host/directory work through
  `hostsvc.Host`. Explicit remote owners must fail closed when disconnected.
- Keep the frontend platform-agnostic. Gate behavior through capabilities, not
  platform identity. Reuse shared UI components and existing design tokens.
- Never edit a shipped migration. Append new migrations to the owning sequence.

## Common commands

```sh
make help             # authoritative target list
make test             # Go and frontend tests
make lint             # Go, TypeScript, ESLint and repository guards
make build            # build frontend, then embed it in the Go binary
make docs-build       # build the documentation site
```

For code changes, run the relevant tests and `make lint`; run the coverage
checks described in the testing guide before committing. For documentation-only
changes, check links and build the site when changing published `docs/` content.

## Read the guidance for your task

Before changing an area, read the matching files below. These are repository
instructions, not optional background. Paths are relative to the repository
root. Read only the files relevant to the task, following their links as needed;
do not load the whole folder. Markdown links do not automatically load files.

| Task | Read |
| --- | --- |
| Setup, dev servers, builds, database isolation, forge operations | [.agents/development.md](.agents/development.md) |
| Tests, coverage, CI or verification | [.agents/testing.md](.agents/testing.md) |
| Finding package ownership or architecture documentation | [.agents/repository.md](.agents/repository.md) |
| Go modules, routes, workers, events or persistence | [.agents/backend.md](.agents/backend.md) |
| React, UI, routing, settings or browser state | [.agents/frontend.md](.agents/frontend.md) |
| Platform adapters, Claude, OpenCode compatibility or runtimes | [.agents/platforms.md](.agents/platforms.md) |
| Session creation, drafts, worktrees, permissions, status or queues | [.agents/sessions.md](.agents/sessions.md) |
| Remote ownership, projects, PRs or issues | [.agents/remotes-and-forges.md](.agents/remotes-and-forges.md) |
| Plugin protocol, SDK, panes or conversation delivery | [.agents/plugins.md](.agents/plugins.md) |
| MCP, routines, webhooks, artifacts, sharing or security | [.agents/integrations.md](.agents/integrations.md) |

## Maintaining these instructions

- Keep this file short. Put task-specific rules in the matching `.agents/` file
  and keep the reading table current when adding or renaming a guide.
- Keep each rule in one topic file. Root safety rules are summaries; topic files
  supply details, not conflicting exceptions.
- Link to existing `docs/`, `spec/`, package READMEs and build configuration
  rather than copying their full contents. Instructions describe constraints
  and non-obvious behavior, not a change log or an exhaustive feature catalog.
- Use explicit paths and runnable commands. Remove stale guidance when behavior
  changes. Do not add a wildcard instruction import that loads every guide.
- When architecture changes, update the affected diagram and bullet list in
  `docs/other/architecture.md` in the same PR. This includes new or merged
  packages, external dependencies, adapter boundaries and browser/backend flows.
  Keep diagrams near ten blocks and put detail in the accompanying prose.
