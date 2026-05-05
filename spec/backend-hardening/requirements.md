# Backend Hardening — Requirements

## Overview

This is a maintenance / quality feature. There is no new user-visible
behaviour. The goal is to reduce risk in a handful of high-complexity
backend areas that today are either entirely untested, undertested, or
silently swallow errors that affect what the dashboard shows.

The work is scoped to **the Go backend only** (`internal/...`,
`main.go`). The frontend is out of scope, except that any new public
API behaviour must remain backward compatible with the existing
client.

The feature is delivered as a series of focused, independently
shippable steps. Each step closes a specific risk identified in the
codebase analysis (see `analysis-summary.md` companion document if
present, or the inline summary in this file's "Background" section).

## Goals

- Add unit-test coverage to high-risk backend modules that currently
  have **zero direct tests**: `internal/pricing`,
  `internal/server/whisper.go`, parts of `internal/server/handlers.go`,
  `dispatchSessionSubpath`, `fetchSessionFromOpenCodeCtx`,
  `convertOpenCodeMessages`, `GetMetricsDashboard`.
- Eliminate **silent error swallowing** in user-facing data paths
  (cost calculation, agent / model lookups, message conversion) so
  that a bad upstream response is observable in logs and metrics
  rather than producing wrong numbers in the UI.
- Make package-level mutable state in `internal/platforms/opencode`
  test-isolatable so the suite is safe under `go test -race -p N`.
- Add `recover()` to long-running background goroutines so a single
  panic does not silently disable a feature for the rest of the
  process lifetime.
- Lift the test coverage floor of the worst-covered packages
  (`internal/server` and `internal/platforms/opencode`) without
  rewriting them.

## Non-Goals

- **No behavioural changes** to the HTTP API or the dashboard. Pinned
  sessions still pin, archive still archives, the metrics dashboard
  still returns the same shape. Anything that would change the JSON
  contract is out of scope.
- **No refactor of `handlers.go` into smaller files.** That is a
  larger architectural change and is tracked separately. This spec
  only adds tests and small, surgical fixes.
- **No new platform adapters.** Platform support is unchanged.
- **No changes to the frontend.** This is a pure backend pass.
- **No new dependencies** unless explicitly justified per step
  (e.g. a fuzz corpus is fine; a new test framework is not).
- **No coverage chase for its own sake.** We are not trying to hit
  a number; we are closing specific risks.

## Background

A complexity / coverage analysis of the Go backend identified the
following critical areas:

| Area | Lines | Direct tests | Risk |
|------|------:|:-------------|------|
| `internal/pricing/pricing.go` | 177 | none | Cost calculation uses bidirectional `strings.Contains` matching — `"gpt-4"` matches `"gpt-4o"`, `"gpt-4-turbo"` etc. Global `sync.Once` makes it untestable. |
| `internal/server/whisper.go` | 179 | none | Shells out to `whisper` / `ffmpeg`, global `sync.Once` state, no DI. |
| `internal/server/handlers.go` `handleSessions` | ~95 | indirect only | Core dashboard fan-out. Pinned-session force-include + state overlay all funnel through here. |
| `internal/server/handlers.go` `dispatchSessionSubpath` | ~110 | indirect only | Hand-rolled URL router. A typo gives a silent 404. |
| `internal/platforms/opencode/client.go` `fetchSessionFromOpenCodeCtx` | ~110 | none | The whole live-session pipeline. |
| `internal/platforms/opencode/client.go` `convertOpenCodeMessages` / `sessionFromOpenCode` | ~120 | none | Untyped `map[string]interface{}` JSON wrangling against the OpenCode HTTP API. |
| `internal/db/stats.go` `GetMetricsDashboard` | ~308 | indirect only | 11 parameters, nested aggregation, gap-filled time series. |
| `internal/db/sessions.go` `InferSessionStatus` | in 548-line file | indirect only | Status state machine that drives every status badge in the UI. |
| `internal/server/projects_index.go` `runProjectsIndexLoop` | in 89-line file | none | Background goroutine, no `recover()`. |
| `internal/server/handlers.go` `runAutoArchiveLoop` (in `server.go`) | small | none | Same risk class. |

Current package-level coverage (from `go test -cover ./internal/...`):

| Package | Coverage |
|---------|---------:|
| `internal/server` | **48.4%** |
| `internal/platforms/opencode` | **57.6%** |
| `internal/telemetry` | 30.1% (mostly bootstrap glue, low-risk) |
| `internal/state` | 77.1% |
| `internal/platforms/claudecode` | 78.0% |
| `internal/gitinfo` | 79.9% |
| `internal/db` | 80.0% |
| `internal/platforms` | 86.3% |
| `internal/srvtiming` | 87.7% |
| `internal/worktree` | 84.7% |
| `internal/pricing` | (no test files) |

`internal/server` and `internal/platforms/opencode` are the two
packages that genuinely need help. `internal/telemetry` is low because
it is mostly OTLP-exporter bootstrap which is not worth unit-testing.

## Target Users

The maintainer of ocman. The acceptance criteria are framed as
verifiable engineering checks, not user-facing behaviour.

## Functional Requirements

### FR-1: Pricing module is unit-tested

- **Description**: `internal/pricing/pricing.go` gets a dedicated test
  file with table-driven tests for `Lookup`, `CalcCost`, and the
  fetch / parse path.
- **Acceptance Criteria**:
  - A new `pricing_test.go` exists with tests for `Lookup`,
    `CalcCost`, and `fetch` (using a mock `http.Client`).
  - Tests cover the substring-matching ambiguity: `"gpt-4"` vs
    `"gpt-4o"` vs `"gpt-4-turbo"` produce the model the user
    intended, or an explicit "no match" result.
  - Tests cover `CalcCost` for: zero tokens, only input, only
    output, cache reads, cache writes, mixed.
  - Tests cover malformed JSON from upstream (returns a non-nil
    error, table stays empty).
  - The `sync.Once` global is replaced by, or supplemented with, a
    constructor-style `New` that takes an `http.Client` and a
    URL — so tests don't share state across runs. The package-level
    `Load` / `Lookup` / `CalcCost` API stays for callers.
  - Coverage of `internal/pricing` is ≥80% after the change.

### FR-2: `Lookup` model-name matching is correct and documented

- **Description**: The substring matcher in `Lookup` must not
  accidentally match a longer or unrelated model when the user
  asked for a specific one.
- **Acceptance Criteria**:
  - Given a model table containing `gpt-4`, `gpt-4-turbo`, `gpt-4o`,
    a lookup of `"gpt-4"` returns the entry for `gpt-4` exactly,
    not one of the longer matches.
  - Given a lookup of `"gpt-4-turbo-2024-04-09"`, the function
    returns the `gpt-4-turbo` entry (longest-prefix-style match
    against the table is acceptable).
  - The matching algorithm is documented in a doc comment on
    `Lookup`, including its rules and known limitations.
  - If the matching rules need to change to satisfy the criteria
    above, the change is covered by table-driven tests.

### FR-3: `pricing.Load` failures are observable

- **Description**: A failure to fetch the pricing table currently
  results in `Lookup` silently returning `nil` for every model,
  which means every cost is reported as `0`.
- **Acceptance Criteria**:
  - On a fetch failure, the error is logged once at WARN level
    via the existing `logrus` logger, including the URL and the
    underlying error message.
  - A package-level metric or counter is incremented (using the
    existing `internal/telemetry` setup if one exists; otherwise
    a simple atomic counter exposed via a getter is fine).
  - `Lookup` continues to return `nil` (no behavioural change)
    but the failure is no longer invisible.
  - Tests assert the log line is emitted on failure (using a
    `logrus` test hook).

### FR-4: `whisper.go` is testable and tested

- **Description**: `internal/server/whisper.go` is refactored so its
  logic can be unit-tested without the `whisper` and `ffmpeg`
  binaries being installed.
- **Acceptance Criteria**:
  - The package-level `whisperState` and `sync.Once` are replaced
    by, or wrapped behind, a constructor that takes an interface
    for binary discovery and command execution. The existing
    public surface (`initWhisper`, `whisperAvailable`,
    `transcribeAudio`) is preserved at the package level.
  - A new `whisper_test.go` covers:
    - `whisperAvailable` returns `false` when no binary is
      configured.
    - `convertToWav` invokes the executor with the expected
      `ffmpeg` arg list (asserted via a fake executor).
    - `transcribeAudio` invokes the executor with the expected
      `whisper` arg list and returns the captured stdout.
    - An `exec.ExitError` from the executor is surfaced as a
      structured error including the captured stderr.
  - Coverage of `internal/server/whisper.go` is ≥75% after the
    change.

### FR-5: `handleSessions` has direct unit tests

- **Description**: The core dashboard endpoint has its own test
  suite, not just integration coverage.
- **Acceptance Criteria**:
  - New tests in `handlers_test.go` (or a dedicated
    `handlers_sessions_test.go`) cover `handleSessions` using the
    existing `fakePlatform` test helper.
  - Tests cover:
    - Single platform, no sessions → empty array, `200`.
    - Single platform, multiple sessions → sorted by 5-min bucket
      then `timeUpdated` desc.
    - Two platforms → results merged and sorted.
    - State overlay: archived / seen / pinned flags are correctly
      applied to the response.
    - `since` parameter respected.
    - `limit` parameter respected.
    - Pinned-session force-include: a session pinned in `state.db`
      but outside the `since` window appears in the response.
    - A platform whose `Sessions` returns an error does not abort
      the request — the other platform's sessions still appear.
  - Tests assert the response body shape, not just the status code.

### FR-6: `dispatchSessionSubpath` has a routing test

- **Description**: The hand-rolled URL router gets a table-driven
  test that maps every supported sub-path to the handler that
  should fire.
- **Acceptance Criteria**:
  - A new test enumerates every supported `/api/session/{id}/...`
    sub-path and asserts the correct handler runs (verified via
    a stub registry of handlers, or via the response body / status
    each handler produces).
  - An unsupported sub-path returns `404` with a body that names
    the request path (so a future typo in production surfaces
    quickly).
  - Adding a new sub-path requires updating the test, by
    construction (the test enumerates handlers from a single source
    of truth — see AD-3 in the architecture doc).

### FR-7: `fetchSessionFromOpenCodeCtx` has an integration test

- **Description**: The live-session pipeline is exercised end-to-end
  against a `httptest.Server` that mimics OpenCode's HTTP API.
- **Acceptance Criteria**:
  - A new test stands up a `httptest.Server` returning canned
    JSON responses for the OpenCode endpoints (`/session`,
    `/session/{id}/message`, etc.).
  - The test invokes `fetchSessionFromOpenCodeCtx` (or its
    successor public-within-package equivalent) and asserts on the
    resulting `db.Session` and message slice.
  - Test cases cover:
    - Healthy response → expected session + messages.
    - Upstream returns `500` → function returns an error,
      session is not partially populated.
    - Upstream returns malformed JSON → function returns an
      error.
    - Pagination cut-off → expected number of messages returned.
    - A message with missing `info` → message is skipped, but
      the count of skipped messages is observable (log line or
      counter; see FR-9).
  - Coverage of `internal/platforms/opencode/client.go` is ≥70%
    (currently 57.6% for the whole package).

### FR-8: `convertOpenCodeMessages` and `sessionFromOpenCode` are
unit-tested

- **Description**: The two functions that translate untrusted
  OpenCode JSON into typed `db.Session` / message structs get
  table-driven tests.
- **Acceptance Criteria**:
  - Tests cover:
    - All known message roles (`user`, `assistant`, `system`).
    - All known part types (text, tool, file, etc.) — at least
      one canonical example per type that ships in OpenCode today.
    - Missing optional fields: function does not panic, fields
      are zero-valued.
    - Missing required fields (e.g. no `info`): message is
      skipped or the function returns an explicit error
      (whichever the function does today; this test pins the
      contract).
    - Wrong types in JSON (e.g. `"id": 5` instead of `"id": "5"`):
      function does not panic.
  - At least one fuzz target (`testing.F`) is added for
    `convertOpenCodeMessages` with a small seed corpus drawn from
    the canned JSON used in FR-7.

### FR-9: Silent-error sites are made observable

- **Description**: Two specific patterns are removed or made
  observable:
  - `AgentCatalog` / `SlashCommands` returning `(nil, nil)` on
    upstream failure.
  - `convertOpenCodeMessages` skipping messages with `nil` info.
- **Acceptance Criteria**:
  - On upstream failure, `AgentCatalog` and `SlashCommands` log a
    single WARN-level line including the upstream URL and the
    error. They continue to return `(nil, nil)` so the existing
    HTTP contract is preserved (frontend still sees an empty
    array).
  - When `convertOpenCodeMessages` skips a message, it logs a
    single DEBUG-level line per call summarising the skipped
    count (not one line per message — that would be too chatty).
  - Tests assert the log lines using a `logrus` test hook.

### FR-10: `GetMetricsDashboard` has dedicated tests

- **Description**: The 308-line, 11-parameter aggregator gets
  dedicated tests with hand-written fixtures.
- **Acceptance Criteria**:
  - A new test file (or an addition to `db_test.go`) seeds an
    in-memory SQLite with a fixed set of sessions and messages
    spanning a known time range.
  - Tests cover:
    - Hourly granularity with a 24h window.
    - Daily granularity with a 30d window.
    - Empty database → all series return zero-filled buckets,
      not `nil`.
    - Project filter narrows the result correctly.
    - Cumulative cost is monotonically non-decreasing.
    - Top-N session log and project log respect the limit and
      are sorted by cost descending.
  - The 11-parameter signature is reviewed; if the parameters can
    be grouped into a `MetricsDashboardOptions` struct without
    rewriting the function, we do that. Otherwise the parameter
    list is documented in a header comment.

### FR-11: Background goroutines have `recover()`

- **Description**: Long-running background goroutines panic-safely.
- **Acceptance Criteria**:
  - `runAutoArchiveLoop` and `runProjectsIndexLoop` wrap their
    iteration body in a `defer recover()` that logs at ERROR with
    the panic value and a stack trace.
  - On panic, the loop continues running on its next tick (it
    does not exit silently).
  - A test injects a panicking iteration via a test seam (e.g.
    a function variable) and asserts the loop survives one tick
    and runs again on the next tick.

### FR-12: `internal/platforms/opencode` package-level state is
test-isolatable

- **Description**: The package-level test seams (`discoverPortsImpl`,
  `pendingPromptCacheTTL`, `pendingPromptCache`) are wrapped so the
  test suite can run with `-race -p 4`.
- **Acceptance Criteria**:
  - `make test-race` (new Makefile target — see FR-13) passes
    with `-race`.
  - Either:
    - The mutable globals are moved into a struct that is
      instantiated once per test, or
    - The existing globals are protected by an explicit `sync.Mutex`
      and the test seams (`swapPendingPromptCacheTTL`,
      `resetPortCacheForTests`) are made parallel-safe.
  - The decision is documented in the architecture doc.
  - Existing tests still pass.

### FR-13: New Makefile targets

- **Description**: Add Makefile targets for the new test commands
  introduced by this spec.
- **Acceptance Criteria**:
  - `make test-race` runs `go test -race ./internal/...` and the
    frontend tests as today.
  - `make test-fuzz` runs every `Fuzz*` target with a short time
    budget (e.g. `-fuzztime=10s`) for CI-friendliness.
  - `make test-coverage` runs `go test -cover ./internal/...` and
    prints a per-package summary.
  - `make help` lists the new targets.
  - CI is unchanged in this spec (running `-race` in CI is a
    separate decision).

## Non-Functional Requirements

### NFR-1: No regression in existing behaviour

- **Description**: All existing tests pass after every step.
- **Acceptance Criteria**: `make test && make lint && make build`
  is green at the end of every step in the implementation plan.

### NFR-2: No new dependencies (with one exception)

- **Description**: The work uses only the Go standard library and
  the dependencies already in `go.mod`.
- **Acceptance Criteria**: `go.mod` and `go.sum` are unchanged
  except for any version bumps already implied by `go test ./...`.
  The one allowed exception is adding a `logrus` test hook
  dependency if the project doesn't already vendor `logrus/hooks/test`
  (it likely does — `logrus` is already a dependency).

### NFR-3: Coverage targets

- **Description**: Per-package coverage rises measurably for the
  two worst-covered packages.
- **Acceptance Criteria**:
  - `internal/pricing`: ≥80% (from 0%).
  - `internal/server/whisper.go`: ≥75% (from 0%).
  - `internal/server`: ≥55% (from 48.4%).
  - `internal/platforms/opencode`: ≥70% (from 57.6%).
  - The number is measured by `go test -cover` on the package.

### NFR-4: Test runtime

- **Description**: The new tests are fast — no real network calls,
  no real subprocesses.
- **Acceptance Criteria**:
  - `make test` runtime increases by less than 30s.
  - No new test relies on a binary being installed on the host
    (no `whisper`, no `ffmpeg`, no `claude`, no `opencode`).
  - All HTTP testing uses `httptest.Server`.

### NFR-5: Match existing style

- **Description**: New tests look like existing tests in the same
  package.
- **Acceptance Criteria**:
  - Table-driven where the existing tests are table-driven.
  - Reuse the existing `fakePlatform`, `tmuxRunner`, and
    `httptest.Server` patterns.
  - No new test framework — `testing` only.

## Data Requirements

None. No schema changes. No new state.db tables.

## Integration Points

- `internal/pricing` — refactor to expose a constructor; keep
  package-level `Load` / `Lookup` / `CalcCost`.
- `internal/server/whisper.go` — extract an executor interface;
  keep the package-level entry points.
- `internal/server/handlers.go` — add tests; possibly extract a
  routing table for `dispatchSessionSubpath` (FR-6, AD-3).
- `internal/platforms/opencode/client.go` — add tests; possibly
  extract test seams behind a struct (FR-12).
- `internal/db/stats.go` — add tests; possibly group parameters
  into an options struct (FR-10).
- `internal/state` — no change.
- `Makefile` — three new targets (FR-13).

## Constraints

- All new Go code lives under `internal/`.
- No platform branching changes.
- No frontend changes.
- No new external dependencies (NFR-2).
- All shell-out paths in `whisper.go` must remain inert when the
  binaries are missing (NFR-4).

## Out of Scope

- Splitting `handlers.go` into smaller files.
- Splitting `stats.go` into smaller files.
- Replacing `logrus` with `slog`.
- Adding OpenAPI / generated client typing.
- Adding fuzz targets to packages other than
  `internal/platforms/opencode` (one fuzz target is enough to
  validate the harness; expanding fuzz coverage is a follow-up).
- Replacing the `lsof`-based port discovery with a more portable
  mechanism.
- Adding a new model-pricing source. The current upstream
  (`models.dev`-style table) is unchanged.

## Success Criteria

- Every item in `Functional Requirements` has at least one test
  asserting it.
- `go test -race ./internal/...` is green.
- Per-package coverage hits the NFR-3 numbers.
- The four "critical hotspot" risks (pricing matcher, live-session
  pipeline, metrics dashboard, hand-rolled router) are no longer
  invisible — each has at least one regression-style test that
  would fail if the existing behaviour broke.
- The maintainer can run `make test-race` locally and see it pass.
