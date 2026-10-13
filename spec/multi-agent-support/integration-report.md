# Main integration checkpoint

Integrate current main and resolve Claude provider delivery conflicts
(claude-code-provider.1.3.134), shared branch `factory/claude-code-provider`,
existing Forgejo PR 834 into `main`.

## Refs and scope

- Starting checkpoint and remote branch: `794fc9e95f636c1b57c2660310d9b28740d04e5a`.
- Integrated main: `e243236a3caac87efe4bad0399de39876d1fdab6`.
- PR was open, unmerged, and not mergeable at preflight. `git fetch origin`
  and `git merge-tree --write-tree --name-only HEAD origin/main` confirmed
  54 conflicting files. The shared branch uses a merge, without rewriting its
  completed checkpoints: `git merge --no-commit origin/main`.
- Approved design revision 2 and scope revision 32 still apply. Recovery gate
  1.3.135 authorized resolving oversized integration files in place without
  splitting. `internal/claudeprovider/prompts.go` is unchanged, including R5.
- The resulting HEAD is recorded in this Issue's completion comment, Factory's
  checkpoint receipt and PR 834's integration inventory. The commit containing
  this report is the integrated tree measured below; coverage was collected
  before the merge commit, so its JSON `sha` still names the starting HEAD.

## Integration inventory

- **Creation and drafts.** Main's IndexedDB draft identities, atomic start claims,
  project defaults, start progress and first-delivery receipts coexist with
  Claude platform selection. Machine/platform switches keep `draftId`, text and
  attachments while clearing incompatible model/agent/reasoning selections.
  Platform selection moved into main's `RoutineEditorDrawer`; the unused old
  `routineForm.ts` extraction was removed. Main's owner-qualified API modules,
  shared controls, plugin panes, upstream preferences and reload behavior remain.
- **MCP and routines.** Main's default-worktree MCP creation carries the selected
  platform into either target. Explicit `worktree: false` still chooses the given
  checkout. Routine worktree runs carry the base platform to the owning host;
  limited descendants prevent cleanup. The fake-Claude MCP test now checks both
  worktree and current-checkout creation and waits for background branch naming
  before deleting its temporary repository.
- **Factory and runtime.** Frozen policy platforms remain attached to dispatch,
  planning and main's new control sessions. Main's scratch grants, graph approval,
  delivery recovery, usage and planning-model behavior remain. OpenCode replacement
  history, stop recovery, authorization and reload callbacks remain wired through
  main's split local-host files. Claude uses `EnsureRuntime` and its provider
  supervisor, without probing disabled OpenCode. Main's joined shutdown workers
  retain their lifecycle; the provider prompt stream and bound MCP/hook routes remain.
- **Permissions, status and analytics.** Main's custom reviewer endpoint takes
  precedence; absent one, the OpenCode/Claude judge selection remains. Provider
  reply-outcome handling and existing provenance deferrals remain. Status carries
  `limited` and `resetsAt` through main's split session types and event files.
  The common idle sink closes owner-scoped user-wait intervals before queue/routine
  handling. Main's changed-row analytics reconciliation remains for OpenCode-only
  mirrors; mixed mirrors retain the provider copy path before orphan cleanup.
- **Migrations and RPC.** Main independently consumed versions 112/113 and reached
  121. Migration 122 idempotently reconciles both histories: main's interrupted-seen
  column and running-routine index, then platform columns/frozen policies and
  `managed_provider`. Existing Claude platform values survive. `remote.proto`
  retains advertised platforms and `EnsureRuntime` alongside main's added RPCs and
  usage service; `make proto` regenerated the stubs with protoc 33.4. No generated
  file was hand-edited.
- **Docs.** The architecture diagrams combine both changes. `conformance.md` and
  `deviations.md` retain the historical evidence and every approved unresolved
  deferral. Read-only standards and spec integration reviews found no additional
  high-confidence issue in the reviewed resolutions.

## Checks and original failures

Environment: macOS arm64, Go 1.26.1, `GOMAXPROCS=4 GOFLAGS=-p=4` on Go commands.
Tests use temporary databases/directories and fake Claude executables. No development
application was started against a live/shared database. No assertion, coverage
threshold, exclusion or test skip was weakened. E2E used one Chromium worker and
zero retries, serving only the built frontend; `/api` and `/mcp` proxied to closed
port 1. The temporary preview config and process were removed after the run.

| Command | Result, including initial failures |
|---|---|
| `pnpm install --frozen-lockfile` in `frontend` | Pass. |
| `go test -run '^$' ./...`; `pnpm exec tsc -b` in `frontend` | Initial failures from moved source types/imports, old Host test calls, duplicate payload field, stale API test and missing provider DTOs. Passed after resolving those integration errors. |
| `go test -count=1 ./...` | First run found `TestClaudeOnlyStartup`'s lost MCP gate, then hit the 120-second tool limit. Second run found broadcast coalescing mismatches, MCP's changed default-worktree expectation and a mock Host lacking capabilities, then stopped on its panic. Server rerun found three planning tests still using the old fake platform. Final complete `internal/...` coverage collection and `go test -count=1 .` passed; example/SDK suites passed in the second full run. |
| `pnpm test` in `frontend` | Initially 7 failed files: removed toast CSS, incomplete routine capability mocks, owner-qualified project-settings expectations and platform-switch draft tests. A targeted rerun exposed saved selections leaking across a machine remount; fixed. Final 417 files / 4,431 tests pass, also under coverage. |
| `go test ./internal/state -run TestProviderMainMigrationReconcilesBothHistories -count=1` | Failing-first check with reconciliation disabled: main lacked `platform`, provider history lacked `interrupted`. With migration 122 enabled both histories pass, including repeated startup and preserved Claude values. |
| `go test ./internal/server -run '^TestProviderIdleResolvesOwnerWaits$' -count=1` | Failing-first check with the old direct idle sink: provider wait remained open. Restoring main's wait-resolution hook to the common sink passes, while the other owner's interval remains open. |
| `make lint` | Initial `errcheck` and whitespace failures in integration test edits, fixed. Final pass: Go lint 0 issues, TypeScript, ESLint 0 errors / 105 warnings, CSS lint and all guards pass. |
| `go test -race -count=1 ./internal/claudeprovider ./internal/platforms/provider ./internal/providersupervisor ./internal/providerprotocol ./internal/permissions ./internal/sessionsvc ./internal/autoapprove ./internal/state ./internal/queuesvc ./internal/hostsvc/local ./internal/remote ./internal/routines ./internal/db ./internal/server` | First 13 packages passed; tool terminated after 600 seconds before the server result. Not claimed as a passing combined run. |
| `go test -race -count=1 ./internal/server` | Failed: MCP worktree test deleted its repository while the background namer still wrote it; added a naming-completion wait. Also exhausted Go's default 10-minute package budget while running `TestPluginManagementToleratesSlowStart`. |
| `go test -race -count=1 -timeout=20m ./internal/server` | All tests pass, 804.558 seconds. Only the total package budget was raised; individual test deadlines/assertions are unchanged. R6 did not recur; its earlier evidence remains. |
| `go test -race -count=1 -run 'Claude\|Drain\|Browser\|Provider\|DualPlatform' .`; `go test -count=1 .` | Both pass, 57.890 and 25.855 seconds. |
| `go test -race -count=1 ./internal/server -run 'TestProviderIdleResolvesOwnerWaits\|TestClaudeMCPCreate\|TestBroadcast'` | Pass after final sink/test cleanup, 8.387 seconds. |
| `pnpm --dir frontend build`; `go build -o /dev/null .`; `go build -tags desktop,exclude_graphdriver_devicemapper ./...` | Pass. |
| `go mod tidy -diff`; `make proto`; `git diff --exit-code -- internal/remote/proto` | Pass; no unstaged module/generated-code diff after regeneration was staged. |
| `make docs-build` | Pass, 46 pages; repeated after this report was added. |
| `pnpm exec vite preview --config vite.integration.config.ts --host 127.0.0.1 --port 18355 --strictPort` in `frontend` | Isolated static preview. Temporary config overrides both proxy targets to `http://127.0.0.1:1`, retaining the usual error handlers. |
| `E2E_BASE_URL=http://127.0.0.1:18355 E2E_NO_WEBSERVER=1 pnpm exec playwright test --project=chromium --workers=1 --retries=0` in `frontend` | Initial 315 pass / 2 fail: MCP toast fixtures omitted the new host `opencode` capability. Added the production capability to the OpenCode fixture, keeping all assertions. Full rerun 317/317 pass. |

## Official coverage after main integration

Baseline comes from fetched `origin/gh-pages` commit
`743b6eb5489bf08194da0466e00c8e6ec3af1217`, data for integrated main
`e243236a3caac87efe4bad0399de39876d1fdab6`.
Exact extraction: `mkdir -p tmp/integration-baseline` then
`git archive origin/gh-pages coverage | tar -x -C tmp/integration-baseline`.

| Commands | First integrated comparison | Final source comparison |
|---|---|---|
| `make coverage SUITE=go`; `make coverage-check BASELINE_DIR=tmp/integration-baseline/coverage SUITE=go` | 87.4 vs 86.7, +0.70, PASS | 87.5 vs 86.7, +0.80, PASS |
| `make coverage SUITE=frontend`; `make coverage-check BASELINE_DIR=tmp/integration-baseline/coverage SUITE=frontend` | 90.14 vs 89.38, +0.76, PASS | 90.17 vs 89.38, +0.79, PASS |

Neither integrated comparison initially failed. The pre-integration frontend
failures in `deviations.md` are not erased. The comparison deferred by recovery
gate 1.3.67 has now been repeated against main's official baseline with the
unchanged 0.1% tolerance. Final delivery must still check its then-current head,
main and official baseline, plus required CI.

## Handoff

R1-R6 and other active omissions in `delivery-report.md` remain unresolved.
This checkpoint is for a fresh read-only verify (claude-code-provider.1.5).
The old verification of `794fc9e9` does not certify it. deliver
(claude-code-provider.1.2) remains cancelled until integration and verification
succeed. PR 834 is reused, not merged or marked delivered by this Issue.
