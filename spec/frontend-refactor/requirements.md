# Frontend Refactor - Requirements

## Overview

The frontend has grown organically and now contains several files that
are far too large, deeply coupled, and almost entirely untested. The
three worst offenders are `SessionDetail.tsx` (3 574 lines, 45
`useState`, 43 `useEffect`, zero tests), `Composer.tsx` (1 374 lines,
22 `useEffect`, zero tests), and `OcmanRuntimeProvider.tsx` (701
lines, a single 497-line conversion function, zero tests).

This refactor decomposes those files into focused, testable modules
without changing any user-visible behaviour. The goal is not a
rewrite — it is a structural reorganisation that makes the existing
code maintainable, testable, and safe to extend.

## Goals

1. Reduce the largest frontend files to < 500 lines each by
   extracting hooks, pure functions, and constants into dedicated
   modules.
2. Eliminate all duplicated logic (message insertion, status
   inference, task-ID extraction, muted-tool lists, sidebar hashing,
   rename handling, chart config boilerplate).
3. Achieve meaningful test coverage for every extracted module —
   targeting the pure functions and data-transformation logic that
   currently have zero tests.
4. Replace the 5 stub-only tests with real behavioural tests.
5. Separate the 50 type/interface definitions in `api.ts` into a
   dedicated types file.
6. Introduce no new user-visible features, no new dependencies, and
   no breaking API changes.

## Target Users

The developer(s) maintaining and extending ocman. This refactor
reduces the cognitive load of working in the frontend and makes it
safe to add new features (new platforms, new tool renderers, new
sidebar views) without fear of regressions.

## Functional Requirements

### FR-1: Decompose `SessionDetail.tsx` into focused hooks

- **Description**: Extract the 11 logical sections identified in the
  analysis into standalone custom hooks and utility modules.
- **Acceptance Criteria**:
  - `SessionDetail.tsx` is reduced to a composition layer that wires
    hooks together and renders the JSX tree. Target: < 500 lines.
  - Each extracted hook is in its own file under
    `pages/session-detail/` or `lib/`.
  - All existing behaviour is preserved — no visual or functional
    changes.
  - `make test && make lint` pass after each extraction step.

### FR-2: Extract SSE event handling from `SessionDetail.tsx`

- **Description**: The 615-line SSE `useEffect` (lines 1370–1985)
  becomes a standalone `useSessionSSE` hook.
- **Acceptance Criteria**:
  - The hook encapsulates: EventSource lifecycle, reconnection with
    backoff, event parsing, message/part state mutations, permission
    and question prompt detection, subagent token capture, and
    fallback polling.
  - The 4 duplicated message-insertion patterns are replaced by a
    single `insertMessageByTime(prev, newMsg)` helper.
  - The 4 duplicated parts-merge patterns are replaced by a single
    `mergeParts(prev, newParts)` helper.
  - The 4 duplicated status-inference patterns are replaced by a
    single `inferStatusFromMessage(msg)` helper.
  - The 4 duplicated part-upsert patterns are replaced by a single
    `upsertPart(prev, part)` helper.
  - All helpers are pure functions with their own unit tests.

### FR-3: Extract pure functions from `SessionDetail.tsx`

- **Description**: The 14 pure functions already defined outside the
  component (lines 57–427) are moved to dedicated modules and tested.
- **Acceptance Criteria**:
  - Each function has a dedicated test file (or is grouped with
    related functions in a shared test file).
  - Functions include: `truncatePartField`, `trimSubagentTokens`,
    `extractMessageFromEvent`, `extractPartFromEvent`,
    `formatModelRef`, `isSessionStatusIdle`,
    `extractPendingPermission`, `extractPendingQuestion`,
    `normalizeQuestionItems`, `hasQuestionOutput`,
    `extractPendingQuestionFromPart`,
    `extractPendingQuestionFromParts`, `hasPendingQuestionInParts`,
    `truncateSseData`.
  - Additionally, the following inline logic is extracted and tested:
    `computeLiveTokens`, `mergeTokenStats`,
    `deriveActiveModelAndAgent`, `isSessionRunning`,
    `deriveRawStatus`, `computeSidebarHash`, `rollupGroupStatus`.

### FR-4: Decompose `Composer.tsx` into focused hooks and utilities

- **Description**: Extract the 7 logical sections identified in the
  analysis into standalone modules.
- **Acceptance Criteria**:
  - `Composer.tsx` is reduced to < 500 lines.
  - Extracted modules:
    - `lib/audio/encodeWav.ts` — WAV encoder (pure function).
    - `lib/models/contextWindows.ts` — `MODEL_CONTEXT_WINDOWS` +
      `getContextWindow` + `formatTokenCount`.
    - `lib/commands/builtinCommands.ts` — `BUILTIN_COMMANDS` +
      `KNOWN_AGENTS`.
    - `hooks/useRecording.ts` — voice dictation lifecycle.
    - `hooks/useImageAttachments.ts` — image paste/drop state.
    - `hooks/useComposerDraft.ts` — localStorage draft persistence.
    - `hooks/useTokenPopover.ts` — token usage popover state + cost
      fetch.
    - `hooks/useSlashCommands.ts` — slash command fetch, filter,
      selection.
  - All pure functions have unit tests.
  - The 15+ ref-sync `useEffect` hooks are replaced by a shared
    `useSyncRef(ref, value)` utility to reduce boilerplate.

### FR-5: Extract and test `convertMessages` from `OcmanRuntimeProvider.tsx`

- **Description**: The 497-line `convertMessages` function is moved
  to its own module and thoroughly tested.
- **Acceptance Criteria**:
  - `convertMessages` lives in
    `lib/convertMessages.ts` (or `lib/messageConversion.ts`).
  - The function is tested with representative inputs for every
    `switch(pd.type)` case: `text`, `tool` (all 12 sub-branches),
    `reasoning`, `patch`, `file`, and the default case.
  - The `isQueued` detection logic is tested with edge cases (first
    message, consecutive user messages, unfinished assistant turns).
  - The `msgAgent` resolution logic is tested (forward scan,
    fallback to `pendingAgent`).
  - The caching strategy (WeakMap) is tested for correctness
    (cache hit when inputs unchanged, cache miss when any input
    changes).
  - Helper functions `isImageMime`, `parsePart`, `truncate`,
    `relativizePath`, `computeIsRunning` are tested individually.

### FR-6: Extract and test pure functions from `AssistantThread.tsx`

- **Description**: The 14 pure functions in `AssistantThread.tsx` are
  moved to dedicated modules and tested.
- **Acceptance Criteria**:
  - Functions extracted and tested: `escapeHtml`,
    `inferLanguageFromPath`, `inferDiffLanguage`,
    `highlightDiffCode`, `renderOutput`, `parseJsonObject`,
    `parseJsonObjectFromMixedText`, `extractPatchPayload`,
    `splitToolArgs`, `parsePatchSections`, `summarizePatch`,
    `shortenPatchPath`, `parseQuestionAnswers`, `parseQuestions`.
  - The duplicated muted-tool-name lists (lines 170, 219, 709) are
    replaced by a single shared constant `MUTED_TOOL_NAMES` (and a
    separate `MUTED_LINE_TOOL_NAMES` that adds `__skill__`).
  - `parseQuestionAnswers` is tested with: JSON array, double-
    encoded JSON, prose format (`"Q"="A"`), object answers with
    `label`/`answer`/`value`/`text` keys, sub-array answers,
    non-string input, empty string, single string answer.

### FR-7: Separate types from `api.ts`

- **Description**: The 50 type/interface definitions in `api.ts` are
  moved to `lib/api.types.ts`.
- **Acceptance Criteria**:
  - `api.ts` contains only the `api` object, helper functions
    (`fetchJSON`, `postJSON`, `throwForStatus`), and re-exports the
    types from `api.types.ts`.
  - All existing imports of types from `api.ts` continue to work
    (re-exports ensure no import changes are needed).
  - `api.ts` is reduced from ~1 196 lines to ~450 lines.

### FR-8: Replace stub tests with real behavioural tests

- **Description**: The 5 stub-only test files are replaced with
  tests that exercise actual logic.
- **Acceptance Criteria**:
  - `useCapabilities.test.ts` — tests the capability merging logic,
    default values, and per-platform overrides.
  - `useSessionChanges.test.ts` — tests the debounced fetch, abort
    on unmount, and cache behaviour.
  - `useSessionInfo.test.ts` — tests the debounced fetch, abort on
    unmount, and cache behaviour.
  - `useInfiniteRows.test.ts` — tests pagination state transitions.
  - `useWorkingTreeDiff.test.ts` — tests the debounced fetch and
    abort behaviour.
  - Where hooks require React rendering context that cannot be
    tested without `@testing-library/react`, test the underlying
    pure logic or Zustand store interactions directly (following the
    existing `apiStore.test.ts` pattern).

### FR-9: Decompose `Dashboard.tsx` into per-tab files

- **Description**: The 1 300-line `Dashboard.tsx` is split into
  focused per-tab components and shared chart utilities.
- **Acceptance Criteria**:
  - Each tab is its own file: `StatsTab.tsx`, `UsageTab.tsx`,
    `SettingsTab.tsx`, `ProjectsTab.tsx`, `SessionsTab.tsx`.
  - Chart configuration boilerplate is extracted into a
    `lib/chartConfig.ts` with a `buildChartOptions(type, overrides)`
    factory.
  - `HeatmapChart` and `HourlyTokensChart` are their own files.
  - Data transformation helpers (`generateHourlySlots`,
    `aggregateModelTokens`, `buildHeatmapWeeks`,
    `computeHeatmapLevel`) are extracted and tested.
  - `renderModel` and `shortSessionID` are moved to `lib/format.ts`.
  - Pagination controls are extracted into a shared `<Pagination>`
    component.

### FR-10: Introduce `useSyncRef` utility hook

- **Description**: A tiny utility hook that replaces the 25+
  ref-sync `useEffect` patterns scattered across `SessionDetail.tsx`
  and `Composer.tsx`.
- **Acceptance Criteria**:
  - `useSyncRef(value)` returns a `RefObject` whose `.current` is
    always the latest `value`, updated via a single `useEffect`.
  - All existing ref-sync patterns in `SessionDetail.tsx` (14
    instances) and `Composer.tsx` (15+ instances) are replaced.
  - The utility has a unit test.

## Non-Functional Requirements

### NFR-1: Zero behaviour change

- **Description**: This is a pure refactor. No user-visible behaviour
  may change.
- **Acceptance Criteria**:
  - All existing Go and frontend tests pass after every step.
  - `make test && make lint && make build` pass at the end.
  - No new features, no removed features, no changed API contracts.

### NFR-2: Incremental delivery

- **Description**: Each FR can be delivered as an independent,
  reviewable unit of work.
- **Acceptance Criteria**:
  - Each FR results in a working codebase (`make test` passes).
  - FRs have minimal cross-dependencies (FR-10 is a prerequisite
    for FR-1 and FR-4; FR-7 is independent; FR-5 and FR-6 are
    independent).

### NFR-3: No new dependencies

- **Description**: The refactor must not add any new npm or Go
  dependencies.
- **Acceptance Criteria**:
  - `package.json` `dependencies` and `devDependencies` are
    unchanged (except version bumps from unrelated updates).
  - No new Go modules.

### NFR-4: Test coverage targets

- **Description**: Every extracted pure function and data
  transformation must have tests.
- **Acceptance Criteria**:
  - All pure functions listed in FR-2, FR-3, FR-4, FR-5, FR-6, and
    FR-9 have at least one test covering the happy path and one
    covering an edge case.
  - The 5 stub tests are replaced with real tests (FR-8).
  - Net new test count: 100+ test cases across the extracted modules.

## Data Requirements

None. This refactor does not change any data models, API contracts,
or database schemas.

## Integration Points

- **`pages/SessionDetail.tsx`**: decomposed into hooks and utilities;
  the page file remains as the composition root.
- **`components/assistant/Composer.tsx`**: decomposed into hooks and
  utilities; the component file remains as the composition root.
- **`components/OcmanRuntimeProvider.tsx`**: `convertMessages` and
  helpers extracted; the provider file remains.
- **`components/AssistantThread.tsx`**: pure functions extracted; the
  component file remains.
- **`lib/api.ts`**: types extracted to `lib/api.types.ts`; re-exports
  preserve all existing imports.
- **`pages/Dashboard.tsx`**: split into per-tab files; the layout
  component remains.

## Constraints

- All new files live under `frontend/src/`.
- No platform branching in the frontend (existing rule).
- Follow the existing test patterns: vitest, `vi.fn()`, factory
  helpers, table-driven where appropriate.
- No `@testing-library/react` — test hooks via their underlying
  stores or pure logic, following the `apiStore.test.ts` pattern.
- Preserve all existing import paths via re-exports where needed.

## Out of Scope

- **Adding `@testing-library/react`**: deferred. Component-level
  rendering tests are out of scope for this refactor.
- **Rewriting the CustomEvent bridge in `Composer.tsx`**: the bridge
  pattern is preserved as-is; it is only relocated into a dedicated
  hook. A future refactor may replace it with `useEffectEvent` or
  controlled components.
- **Standardising POST helpers in `api.ts`**: migrating the 24 raw
  `fetch` calls to `postJSON` is a separate effort (tracked
  separately).
- **Adding AbortSignal to POST endpoints**: separate effort.
- **Splitting `ToolCallDisplay` into per-tool components**: deferred
  to a follow-up refactor after the pure-function extraction
  stabilises the data flow.
- **E2e test updates**: no e2e tests should break (no behaviour
  change), but writing new e2e tests is out of scope.

## Success Criteria

1. No file in `frontend/src/` exceeds 500 lines (excluding
   `api.types.ts` which is a pure type-definition file).
2. Every extracted pure function has at least 2 test cases.
3. The 5 stub-only tests are replaced with real behavioural tests.
4. `make test && make lint && make build` pass.
5. The total frontend test count increases by at least 100 cases.
6. No user-visible behaviour changes.
