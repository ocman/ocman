# CI Test-Coverage Ratchet

Tracks issue #100.

## Background

CI (`.github/workflows/ci.yml`) runs the Go and frontend test suites
on every push to `main` and every PR, but nothing guards against a PR
silently *lowering* overall test coverage. We want a "ratchet": a
baseline that can only rise (or stay flat), so coverage trends up over
time without committing a brittle fixed threshold.

The issue's original approach (Pattern A) stored the baseline as a
Forgejo Actions artifact, which carries a 90-day retention limit and
requires cross-run artifact download via the Forgejo Actions API
(endpoint/token surface unconfirmed on this instance).

Instead we store the baseline on a dedicated **orphan `gh-pages`
branch** as committed JSON. This gives permanent storage with no
retention limit, no API/token gymnastics, and zero churn on `main`
(no coverage numbers ever land on `main`). It doubles as a coverage
history via the branch's git log, and can optionally serve a browsable
HTML report.

## Goal

Fail a PR when Go or frontend total line coverage drops below the
latest `main` baseline (beyond a small noise grace), with the baseline
stored on `gh-pages` and updated automatically as `main` advances.

## Decisions (locked)

- **Tolerance**: a PR fails only if `new < old - 0.1`. The 0.1%
  grace absorbs v8 / Go `atomic` counting jitter.
- **Granularity**: total line % per suite only (one number for Go,
  one for frontend). Per-package floors are out of scope for v1.
- **Logic location**: `make coverage` + `make coverage-check`, with
  CI calling the make targets (consistent with `make test` /
  `make lint`, runnable locally).
- **PR feedback**: CI posts/updates a PR comment showing
  `old% -> new% (delta)` per suite.

## Functional requirements

- **R1.** `make coverage` MUST run both suites with coverage and write
  `coverage/go.json` and `coverage/frontend.json`, each containing at
  least `{ "pct": <number> }` (total line coverage percent).
- **R2.** Go coverage MUST be measured with
  `go test ./... -coverprofile -covermode=atomic`; the total is the
  `total:` row of `go tool cover -func`.
- **R3.** Frontend coverage MUST use the already-installed
  `@vitest/coverage-v8` provider with a `json-summary` reporter; the
  total is `total.lines.pct` from `coverage/coverage-summary.json`.
- **R4.** `make coverage-check BASELINE_DIR=<dir>` MUST compare the
  current `coverage/*.json` against the baseline files in
  `BASELINE_DIR` and exit non-zero if any suite's
  `new < old - 0.1`.
- **R5.** A missing baseline (no `gh-pages`, missing file, first run,
  or unreadable JSON) MUST be treated as a PASS for that suite.
- **R6.** On push to `main`, after tests pass, CI MUST publish the
  current `coverage/*.json` to the `gh-pages` branch under
  `coverage/`, creating the orphan branch if it does not yet exist.
  Uses the existing `contents: write` permission and `github.token`.
- **R7.** On a PR, CI MUST fetch the baseline from `gh-pages`, run
  `make coverage-check`, and fail the job on a drop.
- **R8.** No coverage numbers are committed to `main` (only to
  `gh-pages`).
- **R9.** CI MUST post a PR comment (created once, then updated
  in-place on subsequent runs) listing each suite's
  `old% -> new% (delta)` and overall pass/fail. Comment posting
  failures (e.g. fork PRs without write token) MUST NOT fail the job.
- **R10.** Fork PRs (no write token) MUST still run the read-only
  `coverage-check`; only the `gh-pages` baseline update (R6) and the
  PR comment (R9) are skipped/degraded gracefully.
- **R11.** `make test` (the dev inner loop) MUST stay fast and offline:
  it does NOT collect coverage or contact `gh-pages`. The ratchet
  check is CI-enforced and additionally available as an opt-in
  `pre-push` git hook (`make install-hooks`) so regressions are caught
  before a push. The hook MUST treat a missing baseline / offline as a
  pass so it never blocks a push spuriously.
- **R12.** The pre-push hooks MUST be split per suite (backend /
  frontend) and gated on changed paths, so a push touching only one
  side runs only that side's coverage suite. Collection and the
  ratchet check MUST both be runnable for a single suite in isolation.

## Acceptance criteria

- A PR that lowers Go or frontend total coverage (beyond the 0.1%
  grace) fails CI.
- The baseline updates automatically when `main` advances.
- First run / missing baseline does not break CI.
- No coverage numbers are committed to `main`.
- PRs receive a comment showing `old -> new (delta)` per suite.
- `make coverage` and `make coverage-check` run locally.
