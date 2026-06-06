# CI Coverage Ratchet — Architecture

## Storage model: orphan `gh-pages` branch

The baseline lives on an orphan branch `gh-pages` (no shared history
with `main`):

```
coverage/
  go.json          # { "pct": 72.4, "sha": "<commit>", "ts": "<iso8601>" }
  frontend.json    # { "pct": 65.1, "sha": "<commit>", "ts": "<iso8601>" }
  index.html       # optional: browsable summary (v2)
```

Properties:

- Permanent (no 90-day artifact retention).
- Read cross-run by `git fetch origin gh-pages` then reading the
  files, or by raw URL — no Actions API needed.
- `main` stays free of coverage numbers (R8).
- Branch git log is a free coverage history.

## Local tooling

### `scripts/coverage-collect.sh`
Runs both suites with coverage and emits `coverage/go.json` and
`coverage/frontend.json`.

Go:
```sh
go test ./... -coverprofile=coverage/go.out -covermode=atomic
pct=$(go tool cover -func=coverage/go.out | awk '/^total:/ {gsub("%","",$3); print $3}')
```

Frontend:
```sh
( cd frontend && pnpm test -- --coverage )
pct=$(node -e 'console.log(require("./frontend/coverage/coverage-summary.json").total.lines.pct)')
```

Each writes `{ "pct": <n>, "sha": "<git rev-parse HEAD>", "ts": "<date -u>" }`.

### `scripts/coverage-ratchet.sh`
Compares `coverage/<suite>.json` against `<BASELINE_DIR>/<suite>.json`:

- Missing/unreadable baseline file -> PASS for that suite (R5).
- `new < old - 0.1` -> FAIL for that suite (R4, tolerance).
- Prints a per-suite table `old -> new (delta)` and an overall verdict.
- Writes a machine-readable summary (`coverage/ratchet-result.json`)
  consumed by the PR-comment step.

### `scripts/coverage-collect.sh [go|frontend|all]`
Collects one or both suites. `all` (default) writes both
`coverage/go.json` and `coverage/frontend.json`; a single-suite arg
writes only that file and never runs the other suite.

### `scripts/coverage-ratchet.sh <BASELINE_DIR> [suite ...]`
Compares the named suites (default: both) against the baseline dir.
Suite-scoping lets a per-side hook check only what it collected.

### `scripts/coverage-local-check.sh <go|frontend|all>`
Wrapper for the pre-push hooks: collect the named suite -> fetch the
`gh-pages` baseline into a temp dir -> ratchet check (scoped to that
suite). Missing baseline / offline -> pass.

### Makefile targets
```make
coverage:            ## Run both suites with coverage -> coverage/*.json
	./scripts/coverage-collect.sh

coverage-check:      ## Compare coverage/*.json against $(BASELINE_DIR)
	./scripts/coverage-ratchet.sh "$(BASELINE_DIR)"
```

### Dev-loop policy (R11)

`make test` / `test-backend` / `test-frontend` are intentionally left
**unchanged** — no coverage instrumentation, no network. Rationale:
they are the hot inner loop, Vitest v8 instrumentation adds overhead,
and the baseline lives on a remote branch (would break offline / before
`gh-pages` exists).

Coverage enforcement instead lives in two places:

- **CI** — the `coverage` job (authoritative gate).
- **Pre-push git hook** — opt-in via `make install-hooks`. Added to
  `.pre-commit-config.yaml` as **two** `stages: [pre-push]` hooks,
  `coverage-ratchet-backend` (`files: '\.go$'`) and
  `coverage-ratchet-frontend` (`files: '^frontend/'`). pre-commit's
  `files:` filter only fires a hook when matching files are in the
  pushed range, so editing only Go runs just the Go coverage suite and
  vice-versa — neither pays for the other side. `install-hooks` runs
  both `pre-commit install` and
  `pre-commit install --hook-type pre-push`.

## Frontend vitest config change

Add to `frontend/vitest.config.ts`:
```ts
coverage: {
  provider: 'v8',
  reporter: ['text', 'json-summary'],
  reportsDirectory: './coverage',
  include: ['src/**/*.{ts,tsx}'],
  exclude: ['src/**/*.test.{ts,tsx}', 'src/**/*.d.ts', 'e2e/**',
            'src/wailsjs/**'],   // confirm generated-binding paths
}
```
(`@vitest/coverage-v8` is already a devDependency.)

## CI wiring (`.github/workflows/ci.yml`)

### New `coverage` job (runs on PR and on main push)
Depends on `[frontend, backend]` so it only computes coverage once the
suites are green.

```
needs: [frontend, backend]
steps:
  - checkout (fetch-depth: 0)
  - setup-go, setup-node, install pnpm + deps
  - run: make coverage
  # --- fetch baseline from gh-pages ---
  - run: |
      mkdir -p baseline
      if git fetch --depth=1 origin gh-pages 2>/dev/null; then
        git --work-tree=baseline checkout origin/gh-pages -- coverage/ 2>/dev/null || true
        mv baseline/coverage/* baseline/ 2>/dev/null || true
      fi
  - run: make coverage-check BASELINE_DIR=baseline
```

### PR comment step (R9) — after coverage-check, `if: always()`
Reuses the Forgejo API pattern already in the `tag` job
(`curl` with `Authorization: token ${github.token}` against
`${github.api_url}/repos/${owner}/${repo}/...`):

- `GET  .../issues/{pr}/comments` -> find an existing comment whose
  body starts with a marker `<!-- coverage-ratchet -->`.
- `PATCH .../issues/comments/{id}` to update, or
  `POST .../issues/{pr}/comments` to create.
- Body rendered from `coverage/ratchet-result.json`.
- Wrap in `|| true` so comment failures (fork PRs) never fail CI (R9, R10).

PR number on Forgejo Actions: `${{ github.event.pull_request.number }}`.

### Baseline publish (R6) — main push only
A step gated by `if: github.event_name == 'push' && github.ref == 'refs/heads/main'`:

```sh
git config user.name 'forgejo-actions[bot]'
git config user.email 'forgejo-actions[bot]@noreply'
# Stage just the coverage JSON onto an orphan gh-pages worktree.
tmp=$(mktemp -d)
git fetch origin gh-pages 2>/dev/null \
  && git worktree add "$tmp" gh-pages \
  || git worktree add --orphan -b gh-pages "$tmp"
mkdir -p "$tmp/coverage"
cp coverage/go.json coverage/frontend.json "$tmp/coverage/"
( cd "$tmp" && git add coverage \
    && git commit -m "chore(coverage): update baseline for ${GITHUB_SHA::8}" \
    && git push origin gh-pages )
git worktree remove "$tmp" --force
```
(Exact orphan-create syntax depends on the runner's git version; older
git uses `git checkout --orphan gh-pages` inside the worktree. Confirm
on the runner.)

## Fork-PR behaviour (R10)

- `coverage-check` is read-only (raw/anon `gh-pages` fetch) -> runs
  everywhere.
- Baseline publish only runs on `push` to `main` -> never on PRs.
- PR comment uses the token; on forks it may lack write scope, so the
  step is best-effort (`|| true`).

## Bootstrap

First `main` push after merge auto-creates `gh-pages` (R6). Until then,
PRs see a missing baseline and pass (R5). No manual seeding required.

## Open items to confirm during implementation

1. Frontend `coverage.exclude` globs — pin down generated/wails paths
   so the % is stable.
2. Runner git version -> correct orphan-branch creation syntax.
3. Forgejo PR-comment endpoint shape on this instance (mirror the
   `tag` job's working API base).
4. `go test ./...` coverage spans all packages incl. `main`; decide
   whether to scope to `./internal/...` for a more meaningful number
   (matches existing `make test-coverage`).
