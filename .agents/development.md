# Development and repository workflow

## Database isolation

Every development run that can write data or apply migrations must use a
disposable database isolated to the project and worktree or feature branch.
This includes startup, dev servers, scripts, migration commands and integration
tests. Startup may migrate automatically, so inspect resolved paths first.

- Never use the user's live database, a shared development database or another
  checkout's database. A different HTTP port does not isolate a database.
- Prefer existing database-path overrides. Use an ignored checkout-local path
  such as `.dev/<branch-key>/state.db`, with a collision-resistant branch key.
  Server databases need a separate database or schema for project, checkout and
  branch. Tests use temporary databases.
- Start empty with migrations and test fixtures. If realistic data is needed,
  use a database-consistent backup or export into the isolated target. Never
  blindly copy a running database or copy experimental state back to live data.
- Verify the effective paths of subprocesses and background services too. If
  there is no isolation override, add the smallest explicit override first.
  Never fall back to a shared default.
- Keep databases and credentials out of version control. Only migrate a live or
  shared target when the user explicitly requests that specific deployment.

## Tooling and commands

`mise` provides `air`, `node` and `pnpm`. Run `mise install` if tools are missing.
Use `pnpm` for all Node commands. The Makefile and `make help` are authoritative.

```sh
make dev              # Air backend :8229 and Vite frontend :8228, with HMR
make dev-prod         # Air and Vite preview, manual frontend rebuild
make dev-prod-watch   # Air and Vite preview, automatic frontend rebuild
make dev-backend      # Air only
make dev-frontend     # Vite only
make dev-remote       # backend plus remote gRPC listener :8230
make kill-dev         # clean up development port occupants
make build-desktop    # Wails desktop app in build/bin/
make proto            # regenerate gRPC stubs, requires protoc
make install-hooks    # pre-commit and pre-push hooks
make clean            # removes binary, tmp/ and static/assets/
make docs             # Hugo live reload, default :1313
make docs-build       # site/public
make otel-up          # local Grafana LGTM stack :3000/:4318
make otel-down
make otel-logs
make otel-reset       # also wipes persisted telemetry data
```

Conversation storage and ingestion are always enabled. Dev targets keep
`state_conv.db` in this checkout's root. Tests use temporary stores; builds do
not start ingestion.
Air must keep `clean_on_exit = false` in `.air.toml` (`make dev-remote` reuses
it): deleting `tmp/` also deletes history from older dev runs still using that
location. Preserve an existing `tmp/state_conv.db` with a SQLite-consistent
backup before changing its path; never silently replace it with an empty store.

Vite proxies `/api` to `localhost:8229`. Air rebuilds Go but does not rebuild
embedded frontend assets. Use Vite on `:8228`, or run `pnpm build` in `frontend/`
and trigger an Air rebuild to refresh the embedded bundle.

## Production build order

1. In `frontend/`, run `pnpm install --frozen-lockfile && pnpm build`.
2. At the repository root, run `go build -o ocman .`.

The frontend build writes to `internal/webui/static/`, which Go embeds. Build
the frontend first. Keep the tracked `robots.txt` placeholder so `go:embed
static/*` also works in a clean checkout. Hashed build assets stay gitignored.

## Forge and Git

Use `tea` for this Forgejo repository, not the GitHub mirror. Use
`--repo dries/ocman` outside the root or when detection fails.

```sh
tea issues ls
tea issues create -t "title" -d "body"
tea issues close <id>
tea pulls ls
tea pulls create -t "title" -d "body"   # push the branch first
```

Before integrating a PR, select squash, rebase or fast-forward. Never use an
operation that creates a merge commit. Keep feature branches current by fetch
and rebase, not merge; do not force-push without explicit permission.
