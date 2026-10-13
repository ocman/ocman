# PR 834 main update and module review

## Second main update

PR head before this update: `723b1de7b141b3477ab42342bf823aef831d6cf8`.
New main: `3278cca2ca566a53ee3f47ea55442bb9d454b1e4`.
Verified source checkpoint: `010b1d749`.

Main's feature-module extraction is integrated into the existing integration
checkpoint, with the four later commits rebased onto it. Routine HTTP and MCP
inputs retain their platform selection in `internal/routines`. Push-only prompt
reconciliation now lives in `internal/inbox`; the old server implementation is
removed. Session idle reaches sharing through the typed module Bus with the
owning platform ID. Tmux fixtures use main's shared `testutil.WriteScript`
warmup helper. The application/platform module layout remains intact.

The initial compile check exposed stale routine-service and Inbox reconciliation
test references. Those tests now use the module service/accessor and compile.

Fresh checks pass:

- Compile every package and its tests: `go test ./... -run '^$'`.
- Complete module, routines, Inbox, sharing, webhook, artifacts and queue tests.
- Server Claude routine, permission Inbox, hook and platform-idle integration tests.
- App Claude-only startup, no-OpenCode startup and hook integration tests.
- Go vet, golangci-lint with zero issues, and platform/Host/settings guards.
- CLI and desktop-tag builds and whitespace checks.

The user chose to retain the existing oversized `internal/server/server.go`
layout during this integration. No new root files were added. The previous full
Go coverage/race failures below remain recorded; those full suites were not
repeated or certified by this update.

## History

Original PR head: `15131453cf2568065ee3cf8ece0fa0ae0d8b691c`.
Updated main: `730bc3d7228393fdea6136472e5670e08f1c389e`.

The existing provider implementation and its previous main-integration merge
remain in history. Main was merged into that integration checkpoint, then the
three subsequent commits were rebased onto the updated checkpoint. Conflicts
in the architecture diagram and `useSessionSeen.ts` preserve both main's archive
handling and the provider's owner-scoped lifecycle updates.

## Current layout

- `main.go` only embeds the five skills and passes them, with `main.version`,
  into `internal/app.Run`. Existing CLI and desktop linker flags still work.
- `internal/app` owns application startup, flags, database selection, logging,
  authentication, server composition and startup integration tests.
- `internal/platforms/claudecode` owns browsing/project adapter composition,
  routing, draining, recovery and the provider/hook subcommands. Startup passes
  skills and the MCP address explicitly; the adapter reads no application flags.
- Recorded CLI fixtures now live in `internal/claudeprovider/testdata/2.1.220`.
- Provider account settings live in the dashboard module and use `ChartCard`.
- Prompt HTTP handling lives in `internal/claudeprovider/prompt_http.go`.
  This resolves the earlier R5/FU-6 production source-size exception.

The follow-up structural review found no new root files and checked all 83
added production source files against the 400-line limit. All passed. Tests
and the fake CLI executable are test support and retain the test exemption.
Historical reports use the paths and checkpoints they originally inspected.

## Findings fixed

Directory enumeration and transcript scanning now return non-EOF errors.
An unreadable directory cannot be treated as proof of transcript deletion.
`TestIndexSyncPreservesRowsWhenDirectoryCannotBeRead` failed for projects,
project and subagent directories before the fix, and passes afterwards.
`TestScanFileReturnsReadErrors` also failed before the fix and passes afterwards.

Hook installation/removal now enters through the local Host's optional
`HookPluginManager`. Hook status events still reach the platform.
`TestHookPluginChangesUseLocalHost` failed before the routing change and passes
afterwards. Missing managers are covered by a local Host test.

Fake tmux and provider executables are warmed before readiness/deadline checks.
The warmup does not write the tmux argument log or run the fixture body.
No production deadline or test assertion was widened or removed.

## Verification

Passing checks:

- Go vet and golangci-lint, zero issues.
- TypeScript, ESLint, CSS and the platform/Host/settings guards.
  ESLint reports 104 warnings and zero errors.
- All 421 frontend test files and 4,475 tests.
- Official frontend coverage comparison: 90.21 versus 89.41, +0.80 points.
  Baseline data is for main `730bc3d7`; the 0.1 tolerance is unchanged.
- Complete ordinary tests for `internal/app` and `platforms/claudecode`.
- Failing-first index/scan/Host-hook regressions and existing hook tests.
- Narrow reruns of remote Claude and provider runtime/replay tests.
- Complete ordinary tmux tests after fixture warmup.
- CLI and desktop-tag builds, and `go mod tidy -diff`.
- Beads/Slack example tests and `sdk/plugin` tests.
- Full `platforms/claudecode` race suite.
- Targeted index/scan and Host-hook race regressions in claudeprovider, server
  and hostsvc/local.
- Working-tree whitespace check and a read-only follow-up module review.

Unsuccessful attempts remain part of the evidence:

1. Default-parallel Go coverage failed `TestCommandCapsConcurrentGitProcesses`,
   provider runtime/replay readiness tests, `TestHubDrivesRemoteClaudeSession`
   and tmux fixture-startup tests. Narrow ordinary reruns cleared the
   provider/remote failures. Tmux failure reproduced in isolation before warmup.
2. Coverage with `GOFLAGS=-p=2` still failed provider readiness tests and the
   shell command reached its 900-second execution limit. No complete result.
3. After provider warmup, coverage with `GOFLAGS=-p=4` failed
   `TestUsageLimitHoldsUntilReset`, `TestFirstTurnCrashResumes`,
   `TestClaudeWorktreeFromParentSession`, `TestPluginPaneBeadsAcceptance`,
   `TestPluginPaneRemoteOwner`, `TestPluginPaneReadCancellation` and
   `TestSlackPluginDescribesAndConforms`. The server package then exceeded its
   default ten-minute package timeout. No Go total coverage comparison is claimed.
4. The app race run failed `TestStartupWithoutOpenCode`: its one-second test
   shutdown cancelled startup before instance identity was read.
5. SDK conformance failed `TestFixture/describe-binding` with EOF after its
   subprocess deadline. Other example and SDK package tests passed.

These failures do not certify the complete Go suite or delivery readiness.
The user explicitly authorized pushing the rebase and cleanup with the failures
documented. Other recorded lifecycle, provenance and protocol-version deferrals
are not resolved by this layout work.
