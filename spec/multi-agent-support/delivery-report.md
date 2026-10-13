# Multi-Agent Support - Delivery report and follow-up work

Current main update and module review: see [rebase/layout report](rebase-layout-report.md).
It records the final rebase, module locations, the resolved R5 source-size
exception, additional failing-first fixes and the Go validation failures that
the user accepted for this push. The findings below retain their historical
checkpoint references.

Delivery report for PR 834 (`factory/claude-code-provider` into `main`),
written by claude-code-provider.1.3.124 *Delivery report: document current
limitations and prioritized follow-up work* under graph revision 32. The human
asked (revision 31) to deliver what exists rather than keep repairing: "be less
strict, we need to deliver this soon. work to deliver what we have, make a
report with the follow-up tasks".

This report changes no product code. It does not claim any defect below is
fixed. Sources: Factory verification 1.5 at `b03e27cc` (artifact
`ihpnokfoqu6uaayse7qzlm7idy`: `verification.md` findings R1-R6 and
`departures.md`), `deviations.md` and `conformance.md`. Code references are at
`b03e27cc`; product source was unchanged when this report was first written.
Main was subsequently integrated by Integrate current main and resolve Claude
provider delivery conflicts (claude-code-provider.1.3.134). Its inventory, checks,
original failures and official coverage results are in `integration-report.md`.
The historical defect references below remain at `b03e27cc`; none is claimed fixed.

Recovery: restore platform defaults and runtime availability after main integration
(claude-code-provider.1.3.136) corrects the additional B1/B2/C1 findings at
`e4d4d25`. Directory-bearing REST creation resolves the enabled saved default
before launch, catalog selection filters incompatible implicit models/agents across
platforms and machines while retaining explicit custom picks, and Claude launch
availability requires its resolved CLI plus tmux without disabling browsing.
Optional `hostsvc.RuntimeAvailability.RuntimeAvailable() bool` and the small
`draftCatalog.ts` extraction are the added structural changes. See
`defaults-recovery-report.md` for failing-first regressions, current checks and CI
status. These corrections do not repair or waive R1-R6, erase historical failures,
or certify `e4d4d25`. The complete departure and follow-up inventory below remains
applicable. Factory must dispatch fresh read-only verification on the pushed
recovery checkpoint before delivery.

## 1. What is decided, deferred and unmeasured

Three different things, kept apart:

| Kind | What it means | Items |
|---|---|---|
| **Approved departures** | A human approved the departure at a recovery gate. Delivered as designed. | Gate 1.3.31: `/ack`, leased snapshots, explicit snapshot/replay cursors, headless acknowledgment owner. Gate 1.3.68: FR-7 utilisation from observed CLI `rate_limit_event`s (latest five-hour/weekly windows; absent windows "not reported yet"; no credentials read). Gate 1.3.107 D1: 4 session children plus a separate machine-wide budget of 2 helper calls (up to 6 Claude processes; `--version` and plugin install/uninstall uncounted). Gate 1.3.107 D2: `Host.EnsureRuntime(ctx, platform, dir) (*EnsureRuntimeResult, error)`, the existing result struct with `Port()`, optional `RuntimeEnsurer`, JSON-envelope gRPC member and the old OpenCode RPC fallback. |
| **Approved obligation, still open** | Approved to be checked later; not waived. | Gate 1.3.67: the official frontend coverage comparison is repeated after delivery merges `main`. At `b03e27cc` it fails, 86.28 vs 89.33 (-3.05). Delivery must rerun `make coverage SUITE=frontend` and `make coverage-check SUITE=frontend` on the merged tree and report the result. |
| **Deferred defects (revision 32)** | Known defects shipped unfixed, with follow-up work in §2. Their conformance rows are marked *partial*. | R1-R6 below. |
| **Logged departures** | Recorded in `deviations.md`; a log entry is not an approval. | §3 (types, signatures, structure) and §4 (missing or partial behaviour). |
| **Unmeasured evidence** | Not observed on a real CLI; covered by synthetic fixtures or not at all. Not claimed. | §5. |

## 2. Deferred defects R1-R6

Priority: P1 can lose a user's running work; P2 records or changes the wrong
thing without loss of work; P3 contract or reliability gap; P4 hygiene.
Each is a follow-up task. The regression acceptance must fail on the
current code before the fix (repository rule: red, then green).

### FU-1 (P1) R1: a stale turn's terminal message failure kills the newer turn

- **Code:** `internal/claudeprovider/turns.go:58-86` (result handling: the child
  is marked idle and `l.mu` released, then `resultMessage`/`errorMessage` run),
  `turns.go:238-259` (`errorMessage`, `resultMessage`, `fail`),
  `internal/claudeprovider/live.go:121-131` (`emit` calls the generation-blind
  `publishFailed`), `live.go:156-166` (`publishFailedLocked` sets `error` and
  kills the busy child). The 1.3.120 fix (`live.turnFailed`, `live.go:141-154`)
  checks the generation only on the `settle` path.
- **Trigger:** turn A ends with result-only output, an error result or a
  limited error result. Publishing A's message to the spool fails. Before A's
  failure handler takes `l.mu`, the spool recovers and turn B is accepted and
  starts its child. A's handler then marks the session `error` and kills B's
  child. Without any spool failure, A's message is still published after B
  began. Found by static review; no fault-injection reproduction exists yet.
- **Impact:** the user's new prompt is aborted and the session shows `error`
  for a failure that belonged to the previous turn. Queue drain and routines
  see a failed turn.
- **Regression acceptance:** a test in `internal/claudeprovider` using the
  existing `testHook` seam fails A's terminal message publication (each of
  result-only, error and limited-error), pauses before failure handling,
  restores the spool and accepts B. B's child is not killed, B finishes
  `done`, exactly one idle edge per turn is published, and A's message is
  either published before B's acceptance or dropped, never after it.
  Passes under `go test -race -count=20`.

### FU-2 (P2) R2: AI approval provenance is lost or attributed to the user

- **Code:** `internal/autoapprove/background.go:330-342,349-380`
  (`respondAndPersistSafeApproval`: an ordinary transport error calls
  `finishAIResponse(false)`), `internal/autoapprove/inherit_capture.go:131-155`
  (`HandlePermissionReplied`), `:295-324` (`finishAIResponse` skips the audit as
  ambiguous), `:363-380` (`replayRecordedAIApproval`: a store read error logs
  and falls through to user attribution), `internal/claudeprovider/prompts.go:182-210`
  (the supervisor delivers the reply before the HTTP response returns).
- **Trigger:** (a) the supervisor delivers the AI's `once` reply but the HTTP
  response is lost or exceeds the caller's 10 s deadline, so the caller sees a
  transport error instead of `ErrReplyUnrecorded`; the later
  `permission.replied` is recorded as the user's, or, if it arrives while the
  call is in flight, the approval audit is skipped. (b) ocman crashes after
  delivery and before `RecordApprovedPermission`. (c) An `ai` row exists but
  reading it fails. Found by static review.
- **Impact:** the permission footnote and lifecycle say the user approved
  what the judge or safe-command cache approved, or no audit row is written.
  A safe approval is then not inherited by worktree children (they ask again).
  No command runs that the AI did not approve.
- **Regression acceptance:** tests in `internal/autoapprove` for the judge and
  the cache flow: response lost after delivery, deadline exceeded after
  delivery, restart between delivery and recording, and an approval read
  error. In each, no `user`/`once` row or `ocman.permission.approved` with
  `approvedBy: user` is produced; the emitted lifecycle and notice are either
  the AI's or an explicit "unknown" provenance; an inherited child gets the
  safe approval when it was recorded. Existing provenance tests keep passing
  unchanged.

### FU-3 (P2) R3: rename crosses the Claude configuration boundary

- **Code:** `internal/claudeprovider/live_ops.go:194-213` (`rename` updates
  `owned.Title`/`TitlePending` without the `l.foreign(o)` check that send and
  fork do at `live_ops.go:163,266` and `pool.go:177`), `claude_projects.go:325-330`
  and `internal/platforms/provider/operations.go:87-89` forward it unchecked.
- **Trigger:** a session created under `CLAUDE_CONFIG_DIR` A is renamed while
  ocman runs under configuration B. The shared ownership record is rewritten;
  when B has no transcript for it, `flushTitle` returns nil and the rename
  reports success. Found by static review.
- **Impact:** a read-only session of another Claude configuration gets a new
  title in ocman's ownership file, and in that configuration's transcript once
  it is flushed. Breaks the configuration-scoped read-only contract in
  `AGENTS.md`.
- **Regression acceptance:** extend `TestProjectSupervisorFollowsDashboardConfiguration`
  or add a `claudeprovider` test: renaming an A session under B returns 403
  "unavailable under <config>", `claude-sessions.json` and the A transcript are
  byte-identical afterwards, and the same holds through `claudePlatform`
  routing and the provider adapter. Renaming under A still works.

### FU-4 (P3) R4: protocol version 3 only, no previous version (AD-5)

- **Code:** `internal/providerprotocol/protocol.go:13-28`
  (`PreviousVersion = CurrentVersion = 3`; `ExactPatternsVersion` and
  `ReplyOutcomeVersion` stay 2 for draining).
- **Trigger:** any ocman upgrade that bumps the protocol. A supervisor of the
  previous version is drained and replaced once idle instead of being reused.
- **Impact:** after an upgrade, sends to that repository get `ErrBusy` until
  the old supervisor's turns finish, and its permissions get no auto-approve
  (left to the user). No turn is interrupted. AD-5 requires current and
  previous versions.
- **Decision needed first:** either revise AD-5 to "current only, drain the
  rest" (spec change, human approval) or implement version-2 handling that
  cannot replay acknowledged lifecycle side effects or misread an unrecorded
  reply. Do not restore the unsafe version-1/2 handling to satisfy the range.
- **Regression acceptance:** if implemented, a test connects a real
  `providersupervisor` speaking `CurrentVersion-1`, drives a turn, a permission
  and a replay from 0, and asserts it is reused (not drained), lifecycle events
  of acknowledged turns run no queue/routine side effects, and an unrecorded
  reply is never read as success; `TestVersionsAndHandshakeErrors` asserts the
  range. If the spec is revised instead, `architecture.md` AD-5 and the
  conformance row change in the same commit.

### FU-5 (P3) R6: intermittent cancellation race in the watcher test

- **Code:** `internal/autoapprove/watcher_stall_test.go:200-212`
  (`TestStreamOnceCancellationLeavesNoGoroutines`); the code under test is
  `streamOnce` in `internal/autoapprove/watcher_stream.go`.
- **Trigger:** under `go test -race`, the test cancels once the fake server
  counted the connection, which can be before the client's request returned;
  `streamOnce` then returns `connect /global/event: ... context canceled`
  instead of nil. Seen once at verification 1.5 (`b03e27cc`) and once at
  1.3.105; 20 single-test race repetitions and a full-package race run passed
  afterwards. The initial failures stand as evidence.
- **Impact:** a flaky required check; no user impact shown. Whether the
  production watcher logs a spurious connect error on shutdown is unknown.
- **Regression acceptance:** decide whether a cancellation during connect is a
  clean stop (then `streamOnce` returns nil for `ctx.Err() != nil` on connect)
  or a test ordering problem (then the fake signals after headers are written).
  Keep the `nil` assertion. A deterministic test cancels inside the connect
  window; `go test -race -count=200 -run TestStreamOnceCancellation ./internal/autoapprove`
  passes.

### FU-6 (P4) R5: `prompts.go` exceeds the 400-line source rule

- **Code:** `internal/claudeprovider/prompts.go`, 405 lines, new on this
  branch. No exception was granted for it.
- **Impact:** none at runtime; breaks the repository's file-size rule.
- **Regression acceptance:** split by responsibility (for example prompt
  registration/cancellation and reply/record retry) into files under 400
  lines each, with no behaviour change: `go test -race ./internal/claudeprovider`
  passes and no test is edited beyond moves.

## 3. Type, signature and structure departures (logged, not approved unless §1 says so)

From `departures.md` §"Types"; full entries in `deviations.md`.

1. Protocol v3 only (FU-4). `protocol.go:13-28`.
2. `providerprotocol.Event.Acknowledged` (`acknowledged,omitempty`) suppresses replayed lifecycle side effects.
3. `ReplyOutcomeVersion`, error code `reply_unrecorded`, `platforms.ErrReplyUnrecorded`, optional `platforms.ReplyOutcomeReporter.ReplyOutcomes(ctx, sessionID) bool`.
4. `ExactPattern`/`PatternValue` escape `*`, `?`, `\`; Claude `webfetch`/`websearch` inherit per pattern, not as flat keys.
5. `claude-rules.json` markers `tentative`, `exactPatterns`, `exactRules`; `Rules.EscapeInheritedApprovals([]state.ApprovedPermission)`; `state.DB.EscapeLegacyApprovalPatterns`, `ListPlatformApprovals`, `ApprovedPermission`.
6. `live.turnFailed(id, err, gen)`, `live.deferred`/`unsettled`, settlement retry workers; optional `approvalReader` (SettingsStore unchanged); AI rows not replaced by user rows. R1/R2 remain gaps (FU-1, FU-2).
7. Machine-wide in-process browsing supervisor owns index and conversion; per-project tmux supervisors (NoSync) drive owned sessions; root `claudePlatform` routes sockets outside the protocol.
8. `ProviderManager.Recover`, per-root locks, non-git `ProviderRoot` fallback, shared flock ownership, machine-wide slot/waiter files (200 ms waiter poll), `ProviderRuntime.Args`, provider CLI flags; `ocman provider` accepts `claude` and `claudecode`.
9. Runtime identity (CLI, config dir, plugin) hashed into the tmux name; `claude-launch.json` carries the MCP address for each child spawn.
10. Claude permission rules live in the adapter (`claude-rules.json`); supervisor rules routes return 501.
11. Capability DTO fields `forkAtMessage`, `revert`, `runtimeRestart`, `pushPrompts`, host `opencode`; `SessionLifecycle.readOnly`; older remotes get assumed values; `opencodeLaunch` also means provider-runtime launch.
12. `POST /hook` on the MCP listener, provider `POST /hooks`, `HookEvent`, `hooks` capability; installed state read from `settings.json` `enabledPlugins`.
13. Analytics: `OpenAnalyticsStore`, `Server.analytics`/`WithAnalytics`, platform-source identity, `AnalyticsSource.changedSince`, `AnalyticsMessage.ToolTimes`, schema 3.
14. Transcript parser, scanner and converter rewritten, not salvaged.
15. Subagent prompt attribution by parent session plus `metadata.agentID`, not `GetSessionParentIDs`; Factory Claude sessions also allow `mcp__ocman__factory`.
16. Fake CLI validates every stdin line, control payloads, interrupt subtype, resume config and MCP connectivity.
17. `providerprotocol` imports `internal/db` and `internal/platforms` and aliases shared DTOs (permitted as shared DTOs); `provider/rules_upgrade.go` imports `internal/state`.
18. `prompts.go` 405 lines (FU-6).

## 4. Missing or partial behaviour, grouped as follow-up work

Each group is one follow-up task, not one per line. Priority after FU-1 to FU-6.
`D` = `deviations.md` entry title.

### FU-7 (P2) Turn lifecycle and failure visibility (with FU-1)
- Terminal `error`/`limited` state and reset timers live only in a surviving supervisor's memory; a replaced supervisor loses them; a missing `resetsAt` resets at once. D *A failed turn's `error` status is held by the supervisor only*, *`limited` never clears...*.
- Waiting on a prompt is a flag on `busy`, not a status; cancelled questions carry no reason; abort on a terminal session returns 204. D *Waiting on a prompt is a flag*, *Cancelled questions are rejected without a reason*, *Read-only terminal sessions and abort rely on server errors*.
- Persistence failures show as `error` status, the turn's message when it can be spooled, and logs; no dedicated notice. An ownership-write failure kills the child and loses in-flight output beyond the transcript. D *Verification 1.5 blockers fixed (1.3.97)*, *Verification 1.5 recovery (1.3.99)* F5.
- Acceptance: a replaced supervisor restores terminal/limited state from the spool or transcript (test); a persistence failure produces a visible UI notice (vitest + Go).

### FU-8 (P2) Permission provenance and draining (with FU-2)
- Rules-store outage holds the headless stream at that event, retrying each second. D *1.3.108* V6.
- Draining SSE frames have no ids; a browser past the 64-frame buffer needs a reconnect. Outcome-less (v1) supervisors get no auto-approve. D *1.3.99* F3, *1.3.120* R4.
- `ReplyOutcomeReporter` is not forwarded through the remote hub; a pre-upgrade remote on protocol 7 can carry raw patterns. D *1.3.108* V1, *1.3.120* Limits.
- Pending prompts are not re-decided when rules change; hidden rule-reply failures show only logs. D *Claude prompts are push-only; rule replies retry*.
- Acceptance: each limit either removed with a failing-first test or restated as an approved design decision.

### FU-9 (P2) Configuration isolation (with FU-3)
- Ownership rows recorded before `Config` existed stay drivable under any configuration; a running child keeps the old MCP address until the turn ends; a rate window observed between re-login and the next account read is attributed to the new login. D *1.3.99* F6, *1.3.97* entry.
- Acceptance: a one-time migration scopes legacy rows (test), and the usage attribution window is closed or documented as approved.

### FU-10 (P3) Supervisor durability and version handling (with FU-4)
- A new supervisor process does not reload spools: sequence restarts at 0, a reboot or tmux loss ends turns and pending prompts (the next send resumes the transcript). Socket paths over 104 bytes fail. D *Supervisor spool and previous protocol version are nominal*, *Claude project supervisors outlive ocman, not the machine*.
- The CLI version is read once at startup; driving is pinned to `2.1.220`; browsing works with others; an update goes unnoticed until replacement. D *The Claude CLI version is read once at startup*.
- Acceptance: a supervisor restarted over an existing spool directory resumes its sequence and re-delivers pending prompts (test); a CLI change is detected before the next spawn (test).

### FU-11 (P3) Claude feature parity
- List rows carry no model (FR-3). D *Claude list rows carry no model*.
- Model picker uses static aliases; commands/subagents come from initialize with a 10-minute cache (shape not captured); failed init gives no commands; no primary agent choice. D *Claude composer catalog is static and fork is HEAD-only*.
- Fork at a message unsupported; whole-session fork only. D *Fork at a message is a separate capability*.
- No shell operation (`/shell` 501, `!cmd` is prompt text); hold-for-next-turn is ocman's queue (provider hold 501). D *No shell operation*, *Hold-for-next-turn uses ocman's queue*.
- Live text/thinking stream as accumulated upserts, not `message.part.delta`; tool input not streamed; signature-only thinking can keep live text until reread. D *Live replies stream as message and part upserts*.
- Legacy root-level sidechain transcripts skipped; live child ids assume `task_id = agent_id` (one capture); output before `task_started` absent live. D *Legacy root-level sidechain transcripts are not indexed*, *Live subagent sessions are named from task_started*.
- Allow-always is exact with escaped display; migrations can double-escape intermediate grants or narrow coincident custom globs; inheritance drops `exitplanmode`, `killshell`, unmapped tools and ambiguous OpenCode MCP keys. D *Claude "Allow always" approves the exact request*, *Some Claude "Allow always" approvals are not inherited*.
- Provider sessions cannot be disposed; a failed create can leave an empty session. D *Provider sessions cannot be disposed*.
- Acceptance: per item, a test or an explicit decision to keep the limit (spec updated).

### FU-12 (P4) Configuration, entry points and analytics polish
- Cap of 4 and 600 s idle are fixed. D *The Claude process cap is not configurable*.
- Claude judge offered only when Claude Code is enabled. D *The Claude judge is offered only when Claude Code is enabled*.
- Driven `--plugin-dir` includes hook files ignored for owned sessions; hooks embed the binary path; hook busy expires after 5 min; a killed CLI sends no hook. D *Launched sessions get the hook plugin...*, *Installed plugin state is read from settings.json*.
- Optional platform on MCP/Factory create; PR launch uses the active session's platform without a selector; Factory platform frozen at Epic creation; plugin platform is a declared string; worktree naming follows the session platform. D titles under FR-2 in `conformance.md`.
- Non-OpenCode project sessions scan on refresh (up to 5 min for terminal projects); a machine/platform switch keeps the draft but clears model/agent/reasoning. D *The projects index lists every other platform's sessions on refresh*.
- Mixed analytics wait for the first mirror, can be stale after a sync failure, rebuild on source change, fall back to OpenCode-only if the mirror cannot open; OTel metrics are OpenCode-only; usage is account-level; API-equivalent label applies to any zero-cost platform; no per-session estimate in the composer. D *Analytics run from a platform-neutral store...*, *Claude cost is labelled API-equivalent...*.
- Acceptance: per item, a test or a recorded decision.

### FU-13 (P3) Test reliability (with FU-5)
- `TestSendRacingLimitResetKeepsTurnOpen` timed out once under load (1.3.120); `TestStartupWithoutOpenCode` fails under `-race` only; `NewConversation.lifecycle.test.tsx:173` failed once, not reproduced. D *Intermittent check failures at verification 1.5 (1.3.99)*, *Recovery check run (1.3.105)*, *1.3.120* Limits.
- Acceptance: each made deterministic without weakening an assertion, or left recorded with its observed rate.

## 5. Unmeasured evidence (not claimed)

- No real-CLI smoke test (optional, not done). D *No real-CLI smoke test and no Claude restart test*.
- A natural usage-limit rejection was never hit; the fixture is synthetic. D *Usage limit detection follows the documented rejected status*.
- Prompt waits over 180 s and a hard-limit rejection are unmeasured. D *Permission wait and hard-limit assumptions remain bounded evidence*.
- When the CLI echoes a mid-turn input; whether it keeps an appended `custom-title`; initial transcript existence at the echo. Unechoed input can keep a turn open; abort ignores `still_queued`. D *Rename and first-turn restarts can race the CLI (unverified)*, *Each stream-json result reads as an idle edge*.
- Result-only and stream-start fixtures are synthetic. D *Verification 1.5 blockers fixed (1.3.97)*.
- Live subagent id mapping rests on one capture. D *Live subagent sessions are named from task_started*.

## 6. Delivery obligations

- Repeat the official frontend coverage comparison after merging `main`
  (gate 1.3.67) and report it in the PR; it is not waived. This was repeated by
  Integrate current main and resolve Claude provider delivery conflicts
  (claude-code-provider.1.3.134): final Go 87.5 vs 86.7 and frontend 90.17 vs 89.38
  pass with unchanged thresholds. Delivery still checks its then-current refs and
  baseline; see `integration-report.md`.
- Factory runs fresh read-only verification of this checkpoint against graph
  revision 32 before delivery. `b03e27cc` is not marked successful under the
  old scope.
- The check run for this checkpoint is *Delivery report check run (1.3.124)*
  in `deviations.md`: lint, tests, targeted race, builds, tidy, proto, docs and
  isolated Chromium pass; Go coverage passes the official baseline (86.7 vs
  86.7); frontend coverage fails it (86.28 vs 89.39, -3.11), the approved
  deferral above.
