# Multi-Agent Support - Conformance review

Re-audit of `factory/claude-code-provider` (`ee5e33264..HEAD`) against
`requirements.md`, `architecture.md` and every Factory acceptance criterion,
after the recovery tasks claude-code-provider.1.3.53-1.3.60 (Factory task
1.3.61, replacing the 1.3.17 review). Each departure is an entry in
`deviations.md`; *italic* titles below name those entries.

Status: **implemented**; **implemented, departs** (works, with a recorded
departure in shape or scope); **partial, deferred** (a known defect from
verification 1.5 at `b03e27cc`, shipped unfixed by graph revision 32; see
below); **optional, not done**. "Implemented" means the
behaviour exists with a test; it does not cover the logged omissions and
unmeasured CLI behaviour in the *Departures* column and below, which are
missing or partial and are not claimed. Evidence is a test name (run by `make test`) or a file. Paths:
`cp/` is `internal/platforms/claude/provider/`, `ps/` is `internal/providersupervisor/`,
`pa/` is `internal/platforms/provider/`; bare `.go` names are in
`internal/server/` except the root-package files `main.go`,
`claude_platform*.go`, `claude_projects*.go`, `provider_command*.go`,
`hook_command*.go` and `platform_startup_test.go`.

Main integration by Integrate current main and resolve Claude provider delivery
conflicts (claude-code-provider.1.3.134) merges `e243236a3c` without changing
scope revision 32's R1-R6 deferrals. See `integration-report.md` for moved paths,
migration 122's reconciliation of both database histories, regenerated RPCs,
creation/draft integration, exact check commands and original failures. The
official frontend comparison deferred by gate 1.3.67 now passes on the integrated
tree, 90.17 vs 89.38; Go is 87.5 vs 86.7. Final delivery still checks its then-current
refs, required CI and baseline. Fresh read-only verify (claude-code-provider.1.5)
is required; the previous verification does not certify this merged code.

Corrected after verification 1.5 (comment #23) by claude-code-provider.1.3.97:
the 1.3.61 audit overstated these paths, each found broken or unevidenced in the
production composition and now covered by a test that failed before its fix
(*Verification 1.5 blockers fixed (1.3.97)*): rules, ownership and spool write
failures; a pending permission on an out-of-range supervisor at dashboard
startup; the tmux launch environment; sends racing a result or a limit reset;
the live-process cap; disabled OpenCode in machine targets; stale model/agent on
a platform switch; list status and reset time after a restart; a rebound MCP
listener; the production command catalog; subagent spool truncation; usage
windows across configurations; and a real kill of the whole ocman process.

Corrected again after verification 1.5 at `bf1548e2` (comment #27) by
claude-code-provider.1.3.99: the 1.3.97 claims were overstated for a result
read before its own wait ticket was removed (lost idle edge), a prompt reply
Claude received but the spool could not record (acknowledged), prompts asked
while an out-of-range supervisor drains (never delivered, Allow-always outside
the rules store), a turn completing between a REST read and the browser
snapshot (no terminal state), a failed ownership update at the replay echo
(turn could still succeed and relaunch fresh), a `CLAUDE_CONFIG_DIR` change
(old sessions resumed against the new history), helper processes (outside any
cap) and slot I/O errors (waited forever). Each now has a test that failed on
`bf1548e2` (*Verification 1.5 recovery (1.3.99)*). One result is a departure
awaiting approval: helper `claude` calls have their own machine-wide budget of 2
beside the cap of 4 (NFR-2 row).

Corrected again after verification 1.5 at `f279f45c` (comment #30) by
claude-code-provider.1.3.105: the 1.3.99 claims were overstated for literal `*`
and `?` in Allow-always arguments (they globbed, also when inherited), a turn
ending while the spool stays down (its idle edge was never retried, stranding
the supervisor turn, queue, routines and drain), cancellations (the prompt was
dropped before its cancellation was spooled, and during a reply write),
browsers connected before recovery found an out-of-range supervisor (no
draining prompts), and a conflicting retry after an unrecorded decision
(acknowledged, with hooks and rules following the wrong decision). Each now
has a test that failed on `f279f45c` (*Verification 1.5 recovery at
`f279f45c` (1.3.105)*). The human approved the 4 + 2 helper budget (NFR-2) and
the EnsureRuntime result, interface and JSON RPC shape (AD-1) at recovery gate
claude-code-provider.1.3.107.

Corrected again after verification 1.5 at `251290f6` (comment #34) by
claude-code-provider.1.3.108: the 1.3.105 claims were overstated for a
version-1 supervisor surviving the upgrade, approvals already in
`claude-rules.json` and audit rows inherited by children (raw patterns read as
globs), a second turn whose end failed while a retry ran (abandoned), a prompt
resolved after its turn's idle edge and a headless reconnect (turn reopened
forever, ask pruned), a child exiting after it received a decision (rewritten
as a cancellation), a drain discovered before a browser stream subscribed
(missed), a rules-store failure after the tentative Allow-always was saved
(acknowledged and lost) and an AI approval Claude received but the supervisor
recorded late (attributed to the user, or skipped). Each now has a test that
failed on the unfixed code (*Verification 1.5 recovery at `251290f6`
(1.3.108)*), which also lists the structural departures this adds (protocol
version 2, the `exactPatterns` store marker, a one-time audit-row upgrade, the
`reply_unrecorded` error code, a headless stream held at an unrecordable
Allow-always outcome, AI rows not replaced by user rows) and its limits.

Corrected again after verification 1.5 at `fdcda97c` (comment #41) by
claude-code-provider.1.3.120: the 1.3.108 claims were overstated for approvals
a child inherited before exact patterns (kept as globs in its ruleset and
passed on to grandchildren), a failed old turn end handled after a newer turn
was accepted (which it killed), the acknowledged idle edge a late resolution
kept in the spool (replayed into the queue as a new turn end), AI approvals
behind a surviving version-1 supervisor (generic 503, read as the user's) and
the replayed reply's emitted provenance (`user`/`once`). Each now has a test
that failed on the unfixed code (*Verification 1.5 recovery at `fdcda97c`
(1.3.120)*), which lists the departures this adds (protocol version 3 with
`Event.Acknowledged` and no previous version reused, `platforms.ReplyOutcomeReporter`,
the `exactRules` store marker, `state.DB.ApprovedPermission` /
`ListPlatformApprovals`, `live.turnFailed`) and its limits.

Approved departures from the approved design (recovery gates, not silent):
replay acknowledgments and snapshot leases (gate 1.3.31); FR-7 utilisation from
observed CLI rate-limit events (gate 1.3.68); the frontend official-baseline
coverage comparison deferred to delivery's main merge (gate 1.3.67); the
separate budget of 2 helper Claude processes beside the cap of 4 and the
EnsureRuntime result/interface/JSON RPC shape (gate 1.3.107).

Deferred at delivery, not fixed (claude-code-provider.1.3.124, graph revision
32, *Delivery deferrals at `b03e27cc` (1.3.124)*): verification 1.5 at
`b03e27cc` found R1 a stale turn's terminal message failure that kills a newer
turn, R2 AI approval provenance lost or recorded as the user's when a reply's
response is lost, ocman restarts before recording, or the row cannot be read,
R3 rename across Claude configurations, R4 protocol version 3 only (no previous
version), R5 `prompts.go` over 400 lines and R6 an intermittent race-check
failure. The rows they affect are marked **partial, deferred** below; the
follow-up tasks with code references, triggers, impact and regression
acceptance are in `delivery-report.md`. The 1.3.120 rows below list the tests
that pass; they do not cover these paths.

Unmeasured, stated as such and not claimed: a natural usage-limit rejection
(the fixture is synthetic); a prompt wait over 180 s; when the CLI echoes a
mid-turn input; whether it keeps an appended `custom-title`; the optional
manual real-CLI smoke test.

## Functional requirements

| Req | Status | Evidence | Departures |
|---|---|---|---|
| FR-1 Runs without OpenCode | implemented | `TestClaudeOnlyStartup` (`-platforms claudecode`, no `opencode` on PATH: lists, opens, archives, pins, analytics, default platform, projects, capabilities, doctor, judge options and MCP config, then asserts `opencode` never ran); `TestStartupWithoutOpenCode`; `TestOpenCodeDisabledSkipsOpenCodeJudge`, `TestOpenCodeSettingsWithoutOpenCode`, `TestDoctorWithoutOpenCodeSkipsItsDatabase`, `TestDefaultPlatformIsAlwaysEnabled`; `README.md`, `docs/configuration/_index.md` | *Claude-only workflow recovery adds capability flags*; *The setup panel blocked a Claude-only ocman on OpenCode's database* (resolved) |
| FR-2 Platform choice on every create path | implemented, departs | Composer selector `ComposerPlatformSelector.tsx`; default `internal/state/default_platform.go` (`TestDefaultPlatformIsAlwaysEnabled`, `TestCreatePlatformDefaultFollowsRemote`, `TestCreateDefaultsToRemotePlatform`); `/wt` and worktrees view `TestClaudeWorktreeCreateAndLaunch`, `TestClaudeWorktreeFromParentSession`; PR launch `TestClaudePRLaunchWorktree`; routines `TestClaudeRoutineSettlesOnIdle`, `TestRoutinePlatformIsSavedAndDispatched`; MCP `TestClaudeMCPCreate`, `TestClaudeMCPCreateDefaultsAndCallingSession`; Factory `TestClaudeFactoryEpicDispatchesToClaude`; plugins `TestClaudePluginConversationCreatesAndResumes`, `TestPluginProjectCatalogUsesInstallationPlatform`; pickers `TestProjectsIndexMergesPlatformDirectories`; machine and platform switching `TestLocalTargetUsesEnabledPlatform`, `NewConversation.test.tsx` (platform or machine switch submits the new catalog's model and agent, draft kept) | *MCP `sessions create` and Factory `create` take an optional platform*; */wt and continued routines take the parent session's platform*; *PR/Issue launches use the active session's platform, without a selector*; *The projects index lists every other platform's sessions on refresh*; *Factory platform is set when the Epic is created*; *Plugin conversation platform is a plugin-declared setting* |
| FR-3 Claude session browsing | implemented, departs | `CLAUDE_CONFIG_DIR` `cp/provider.go`; index `TestWarmStartParsesNoFiles`, `TestPagingReadsOnlyRequestedRange`; stored subagents `TestSubagentRendersUnderTaskCall`; live subagents `TestLiveSubagentStaysUnderTask`; archive/seen/pin `TestClaudeOnlyStartup` | *Claude list rows carry no model*; *Legacy root-level sidechain transcripts are not indexed*; *Live subagent sessions are named from task_started*; *The transcript parser was rewritten, not salvaged* |
| FR-4 Interactive Claude sessions | partial, deferred (R1, R3); otherwise implemented, departs | First prompt and stream `TestClaudeComposerStart`; idle stop and resume `TestIdleTimeoutResumesSession`; abort `TestAbortInterruptsTurn`, `TestAbortWhileWaitingForSlot`; turn outcome `TestFailedTurnsSettleAsError`, `TestMidTurnInputKeepsTurnOpen`; rename/first turn `TestRenameDuringTurnWaitsForIdle`, `TestFirstTurnCrashResumes`; catalog `TestDirectoryCatalogFromCLI`; read-only terminal sessions `TestQueueRefusesReadOnlySession`, `TestHubSeesRemoteTerminalSessionReadOnly`, `TestClaudeOnlyStartup`; restart survival mid-turn and mid-permission `TestClaudeSupervisorSurvivesOcmanRestart`, whole-process SIGKILL `TestDashboardKillRestartComposition`; per-worktree cwd and shared supervisor `claude_projects_test.go`; durability `TestOwnershipFailureIsVisible`, `TestSpoolFailureFailsTurnVisibly`, `TestStartedPersistenceFailureFailsTurn`, `TestSpoolOutageAcrossContinuationAndExitSettles`, `TestSettlementRetryOwnsEveryGeneration`, `TestLateResolutionAfterIdleAndReconnect`; no false or lost idle `TestSendRacingResultKeepsTurnOpen`, `TestAcceptedUnwrittenSendKeepsTurnOpen`, `TestSendRacingLimitResetKeepsTurnOpen`, `TestResultBeforeTicketRemovalSettles`; terminal snapshot `TestSnapshotCarriesTerminalState`; configuration scope `TestProjectSupervisorFollowsDashboardConfiguration`; slot errors `TestBrokenSlotStoreFailsVisibly`; settled status after restart `TestRestartedDashboardKeepsSettledStatus`; result-only output `TestResultOnlyOutputIsShown` (synthetic) | *Claude project supervisors outlive ocman, not the machine*; *No shell operation for Claude sessions*; *Hold-for-next-turn uses ocman's queue, not the provider operation*; *Claude composer catalog is static and fork is HEAD-only* (partly resolved); *Fork at a message is a separate capability*; *Read-only terminal sessions and abort rely on server errors* (abort on a terminal session still 204); *Rename and first-turn restarts can race the CLI (unverified)*; *A failed turn's `error` status is held by the supervisor only*; *Verification 1.5 blockers fixed (1.3.97)*; *Verification 1.5 recovery (1.3.99)* (sessions of another Claude configuration are read-only) |
| FR-5 Permissions and auto-approve | partial, deferred (R2); otherwise implemented, departs | Rules in ocman `pa/rules.go`; YOLO inheritance `TestYOLOParentChildIsNotPrompted`; Allow-always `TestAllowAlwaysIsRecordedAndInherited`; questions `TestAskUserQuestionAnsweredFromOcman`; Claude judge `TestClaudeJudgeDecidesWithoutOpenCode`; judge intent `TestRecentUserMessagesFromSession`; push-only `TestRuleAllowedPromptNeverReachesBrowser`, `TestRuleReplyFailureRetriesWithoutLeaking`, `TestPermissionInboxReconciliationNeverPollsPushOnlyPlatforms`, `TestFailedReplyWriteKeepsPromptPending`, `TestUnrecordedReplyIsRetriedNotRedelivered`; exact Allow-always `TestAllowAlwaysWithWildcardCharactersIsExact`, `TestExactPatternsDoNotGlob`, `TestFlatKeysAreExactOnClaude`; conflicting retries `TestConflictingReplyRetryIsRefused`, `TestConflictingQuestionRetryIsRefused`, `TestTentativeAllowAlways`; cancellation `TestCancellationDuringOutageIsRecorded`, `TestCancelDuringReplyWriteResolvesOnce`; while draining `TestOutOfRangeSupervisorDeliversPromptsWhileDraining`, `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts`; persistence `TestRulesPersistFailureIsVisible`, `TestAdapterRulesPersistFailure`, `TestAllowAlwaysOutcomeSurvivesStoreFailure`; upgrade `TestLegacySupervisorPatternsAreExact`, `TestLegacyRulesStoreApprovalsBecomeExact`, `TestLegacyClaudeApprovalsInheritExact`, `TestLegacyInheritedRulesBecomeExact`; exit after delivery `TestDecisionDeliveredBeforeExitStands`; AI provenance `TestDelayedAIApprovalKeepsProvenance`, `TestAIApprovalIsNotOverwrittenByUser`, `TestLegacySupervisorLeavesApprovalToUser`, `TestApprovedPermissionLookups`; limitation documented `docs/features/claude-code.md` | *Claude "Allow always" approves the exact request*; *Verification 1.5 recovery at `f279f45c` (1.3.105)* (escape syntax for exact patterns); *Verification 1.5 recovery at `251290f6` (1.3.108)* (pattern upgrade, rules-store outage, AI provenance); *Verification 1.5 recovery at `fdcda97c` (1.3.120)* (inherited legacy grants, unknown reply outcomes, replayed AI provenance); *Some Claude "Allow always" approvals are not inherited* (`exitplanmode`, `killshell` still dropped); *Claude prompts are push-only; rule replies retry*; *Claude rulesets live in the ocman-side adapter, not behind the rules RPC*; *The Claude judge is offered only when Claude Code is enabled* |
| FR-6 Live status for terminal sessions (opt-in) | implemented, departs | `TestHookPluginInstallUninstallKeepsSettings`, `TestRunHookReturnsWithinTimeoutWhenOcmanIsDown`, `TestTerminalSessionHooksDriveStatus`, `TestOwnedSessionIgnoresHooks`; toast `HookPluginPrompt.tsx` | *Hook events reach the provider over a new protocol route*; *Installed plugin state is read from settings.json*; *Launched sessions get the hook plugin and an unverified MCP address* (hooks still copied into `--plugin-dir`) |
| FR-7 Subscription limits | implemented, departs (approved) | `limited` + `resetsAt` held until reset, across restart `TestUsageLimitHoldsUntilReset`, `TestHeadlessReconnectRetainsLimitedState`; queue/routines `TestLimitedSessionDoesNotDrainSweep`, `TestLimitedRunRemainsRunning`; UI `StatusBadge.test.tsx`; account `TestDrivenSessionRecordsRateLimit`, `TestAccountNeverReadsCredentials`, `TestUsageWindowsScopedToConfiguration`, `ProviderAccountSettings.test.tsx`; list `resetsAt` after restart `TestRestartedDashboardKeepsSettledStatus` | *Claude account utilisation comes from observed CLI rate-limit events* (gate 1.3.68); *Usage limit detection follows the documented rejected status* (synthetic fixture); *`limited` never clears and does not survive a restart* (resolved; a replaced supervisor loses the timer) |
| FR-8 Feature coverage | implemented, departs | Rows below | Rows below |
| FR-9 Terms of use | implemented | `docs/features/claude-code.md` "Terms of use" | - |

### FR-8 rows

| Feature | Status | Evidence / departures |
|---|---|---|
| Composer, queue, attachments | implemented | `TestClaudeComposerStart`; live connection for launched sessions (`claude_projects.go` overlay, 1.3.56); *Existing Claude sessions report no live connection* (resolved) |
| `/wt`, worktrees view, PR/Issue launch | implemented, departs | `claude_create_test.go`; *Claude worktree naming follows the session's platform* |
| Routines, MCP `sessions` | implemented, departs | `TestClaudeRoutineSettlesOnIdle`, `TestClaudeMCPCreate`; *Provider sessions cannot be disposed* |
| Factory per Epic | implemented, departs | `TestClaudeFactoryEpicDispatchesToClaude`; *Factory sessions on Claude also allow `mcp__ocman__factory`* |
| Plugin conversations per installation | implemented, departs | `TestClaudePluginConversationCreatesAndResumes`; *Plugin settings catalog is OpenCode-only and the platform is free text* (resolved; still a declared string setting) |
| Multi-remote | implemented | `TestHubDrivesRemoteClaudeSession`, `TestHubAnswersRemoteClaudePermission`, `TestClaudeRemoteFailsClosedWhenDisconnected`, `TestManagerAdvertisedPlatforms` |
| Analytics / usage | implemented, departs | `TestAnalyticsStoreWithoutOpenCode`, `TestMixedAnalyticsNeverReadOpenCodeAlone`, `TestUsageViewsCountClaudeTranscriptTokens`, `StatsLogTables.test.tsx`, `SubscriptionUsage.test.tsx`; *Analytics run from a platform-neutral store; the mirror is never bypassed once another platform contributes*; *Claude cost is labelled API-equivalent wherever the platform reported $0* |
| Revert / unrevert, session changes | implemented | Capability `revert` hides `/undo`/`/redo` (1.3.56); session changes gated by `fileChanges` |
| Sharing, artifacts | implemented | Generic share handlers; artifacts through the MCP config passed to project supervisors (`TestMCPListenerTellsPlatformsBoundAddress`) |
| OpenCode maintenance, v2 views | implemented | Host capability `opencode` hides Maintenance (`TestClaudeOnlyStartup` capabilities assertion) |

## Non-functional requirements

| Req | Status | Evidence | Departures |
|---|---|---|---|
| NFR-1 Performance | implemented | Claude list from `claude-index.db` (`TestWarmStartParsesNoFiles`). OpenCode with both platforms: `TestDualPlatformKeepsOpenCodeListing` asserts the 300 OpenCode rows of `GET /api/sessions` are identical with Claude enabled and bounds the median; measured on the 1.3.61 machine (macOS arm64, 14 CPUs), 60 interleaved rounds, three runs: OpenCode only 10.57-10.79 ms, OpenCode + Claude Code 10.85-11.11 ms (+0.24 to +0.33 ms, the concurrent fan-out to the Claude index) | - |
| NFR-2 Memory | implemented, departs (helper budget approved, gate 1.3.107) | Cap 4 machine-wide live session children, 600 s idle: `TestMachineWideCapAcrossSupervisors`, `TestSlowExitingChildHoldsItsSlot`, `TestIdleTimeoutResumesSession`; helper calls bounded separately at 2: `TestHelperSlotsAreBoundedAndSeparate`, `TestClaudeJudge` | *The Claude process cap is not configurable*; *Verification 1.5 recovery (1.3.99)* (F7: up to 4 + 2 Claude processes, not 4; approved at gate 1.3.107) |
| NFR-3 Isolation | implemented | `TestProviderInstancesAreIsolated`; per-instance state in `pa/adapter.go` | - |
| NFR-4 No new runtime dependency | implemented | `go.mod`/`package.json` gain no runtime dependency; stream-json in Go | - |
| NFR-5 Compatibility | implemented, departs | `TestedCLI` (`2.1.220`) records protocol fixture evidence, not launch eligibility. `TestCLIAvailability` rejects a missing CLI but permits automatic updates. | *Supported CLI range extends beyond the measured version* (historical exact-version gate removed; recorded evidence remains pinned); *The Claude CLI version is read once at startup* |
| NFR-6 Tests never run the real `claude` | implemented; manual smoke test optional, not done | Fake CLI `cp/testdata/fakeclaude` validating input (`TestFakeCLIRejectsMalformedInput`, `TestFakeClaudeValidatesControlResponses`); fixtures `testdata/claudecode/2.1.220/` | *No real-CLI smoke test and no Claude restart test* (restart test resolved; smoke test optional) |
| NFR-7 Coverage ratchet | main-integrated official comparisons pass; delivery still checks current refs/baseline; intermittent race check R6 deferred | `integration-report.md`, final Go 87.5 vs 86.7, frontend 90.17 vs 89.38, unchanged tolerance | *Main-integrated check run and official coverage comparison (1.3.134)*; earlier check failures remain below |

## Architecture decisions

| AD | Status | Evidence | Departures |
|---|---|---|---|
| AD-1 Seam refactor | implemented, departs | `EnsureRuntime` `internal/hostsvc/local/ensure_runtime.go`, `TestEnsureRuntimeAndLegacyRPC`, `TestEnsureRuntimeFallsBackOnlyForOpenCode`; data-driven platforms `TestStartupWithoutOpenCode`; sinks `TestPlatformIdleSinkDrainsQueue`, `TestPlatformStatusSink`; `limited`; `Judge` `TestOpenCodeJudgeThroughInterface`; remote platforms `TestManagerAdvertisedPlatforms`, `TestManagerLegacyHelloDefaultsToOpenCode` | *EnsureRuntime returns the existing result struct; adapters plug in through RuntimeEnsurer; the RPC carries JSON* (approved at gate 1.3.107) |
| AD-2 Provider protocol | implemented, departs | `internal/providerprotocol/README.md`; `TestSessionContract`, `TestMutationContract`, `TestEventContractAndReplay`, `TestCapabilitiesGateEveryOptionalMethod`, `TestHTTPErrorContract` | *Explicit acknowledgments and leased snapshots extend the provider protocol* (gate 1.3.31); *Hook events reach the provider over a new protocol route*; *Live replies stream as message and part upserts, not token deltas*; *No shell operation*; *Hold-for-next-turn uses ocman's queue*; *Fork at a message is a separate capability*; *Claude rulesets live in the ocman-side adapter* |
| AD-3 One supervisor per project, in tmux | implemented, departs | `ocruntime.ProviderRuntime`, `ProviderManager`; `TestClaudeSupervisorSurvivesOcmanRestart`, `TestProviderRecover`, `TestOwnershipSharedAcrossSupervisors`, `TestMachineWideCapAcrossSupervisors`; launch configuration through a stale tmux environment and a rebound MCP listener `TestProjectSupervisorFollowsDashboardConfiguration`, `TestProviderIdentity` | *Claude project supervisors outlive ocman, not the machine*; *Claude browsing runs in an in-process supervisor that owns the index* |
| AD-4 Replay across restarts | implemented, departs | `ps/spool.go`; late resolutions keep their ask `TestLateResolutionKeepsAskAndDoesNotReopenTurn`; unrecordable Allow-always outcome replayed `TestAllowAlwaysOutcomeSurvivesStoreFailure`; terminal state in snapshots (gate 1.3.31 race) `TestSnapshotCarriesTerminalState`; `TestKilledDashboardProcessReplaysAfterItsLastCursor`, `TestDashboardRestartReplaysEveryEventAndPendingPermission`, `TestClaudeSupervisorSurvivesOcmanRestart`, `TestDashboardKillRestartComposition`; subagent spools truncate `TestLiveSubagentStaysUnderTask` | *Supervisor spool and previous protocol version are nominal* (spool not reloaded by a new supervisor process) |
| AD-5 Version skew | partial, deferred (R4: only the current version is reused); drain implemented | `TestVersionsAndHandshakeErrors`, `TestHandshakeRequiresExplicitVersionAndCLI` (missing version rejected), `TestProviderVersionSkewDrainsWithoutInterruptingTurn`, `TestDrainWaitsForTrulyIdleSessions`; prompts pushed and answerable while an out-of-range supervisor drains `TestOutOfRangeSupervisorDeliversPromptsWhileDraining`, `TestDrainingAdapterUsesOnlyVersionIndependentRoutes`, with the browser connected before discovery `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts` (discovery once browsing and before subscription); draining version-1 supervisor `TestLegacySupervisorPatternsAreExact`, `TestLegacySupervisorLeavesApprovalToUser`; drain after a spool outage `TestSpoolOutageAcrossContinuationAndExitSettles` | *Supervisor spool and previous protocol version are nominal* (`PreviousVersion` 1 at 1.3.108, equal to the current version 3 since 1.3.120); *Verification 1.5 recovery at `251290f6` (1.3.108)* (version 2, version-1 patterns escaped); *Verification 1.5 recovery at `fdcda97c` (1.3.120)* (versions 1 and 2 drain) |
| AD-6 Driving `claude` | implemented, departs | Flags in `cp/pool.go` (`--verbose`, `--forward-subagent-text`, `--permission-prompt-tool stdio`, `--mcp-config`, `--plugin-dir`, never `--bare`); `TestPartialStreamAgreesWithStoredParts` | *Required streaming flags missing from the launch command* (resolved); *Live replies stream as message and part upserts* |
| AD-7 Permissions owned by ocman | partial, deferred (R2); otherwise implemented, departs | See FR-5 | *Claude subagent prompts are attributed by the provider*; *Some Claude "Allow always" approvals are not inherited* |
| AD-7a Push-only prompt flow | implemented, departs | `TestLateResolutionKeepsAskAndDoesNotReopenTurn`, `TestDecisionDeliveredBeforeExitStands`, `TestOutOfRangeSupervisorDeliversPromptsWhileDraining`, `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts`, `TestUnrecordedReplyIsRetriedNotRedelivered`, `TestConflictingReplyRetryIsRefused`, `TestCancellationDuringOutageIsRecorded`, `TestCancelDuringReplyWriteResolvesOnce`, `TestRuleAllowedPromptNeverReachesBrowser`, `TestRuleReplyFailureRetriesWithoutLeaking`, `TestPermissionInboxReconciliationNeverPollsPushOnlyPlatforms`, `TestPendingPromptAnsweredAfterReconnect`, `TestClaudeSupervisorSurvivesOcmanRestart` | *Claude prompts are push-only; rule replies retry*; *Cancelled questions are rejected without a reason* |
| AD-8 Judge without OpenCode | implemented, departs | `TestDelayedAIApprovalKeepsProvenance`, `TestJudgeWithoutBackendNeedsUser`, `TestSafeCommandCachePrecedesBackendSelection`, `TestClaudeJudgeDecidesWithoutOpenCode`, `TestOpenCodeDisabledSkipsOpenCodeJudge` | *The Claude judge is offered only when Claude Code is enabled* |
| AD-9 Transcript index | implemented, departs | `cp/index.go`; `TestWarmStartParsesNoFiles`, `TestPagingReadsOnlyRequestedRange` | *The transcript parser was rewritten*; *Legacy root-level sidechain transcripts are not indexed* |
| AD-10 Status | partial, deferred (R1); otherwise implemented, departs | `TestFailedTurnsSettleAsError`, `TestSpoolOutageAcrossContinuationAndExitSettles`, `TestSettlementRetryOwnsEveryGeneration`, `TestMidTurnInputKeepsTurnOpen`, `TestUnobservedTranscriptIsNotBusyAndRefreshesOnRead`, `TestTerminalSessionHooksDriveStatus` | *Waiting on a prompt is a flag, not a status*; *A failed turn's `error` status is held by the supervisor only*; *Terminal session liveness comes from transcript recency* (resolved) |
| AD-11 The ocman Claude plugin | implemented, departs | `cp/plugin.go`; hook tests under FR-6 | *Installed plugin state is read from settings.json*; *Launched sessions get the hook plugin and an unverified MCP address* |
| AD-12 Analytics | implemented, departs | See FR-8 analytics row | *Analytics run from a platform-neutral store*; *Claude cost is labelled API-equivalent* |
| AD-13 Platform choice | implemented, departs | See FR-2 | *Factory platform is set when the Epic is created*; *Plugin conversation platform is a plugin-declared setting* |

## Factory acceptance criteria

Recovery: restore platform defaults and runtime availability after main integration
(claude-code-provider.1.3.136) restores the additional B1/B2/C1 gaps found at
`e4d4d25`. `TestRESTCreateResolvesDefaultBeforeRuntime`,
`TestRESTCreateAuthoritativeOwner`, the real-composer rendered switch regressions,
custom-selection controls, `TestHostCapsNativeProviderPrerequisites` and
`TestClaudeNativeRuntimeAvailability` are its acceptance evidence. The optional
`RuntimeAvailability` seam and approved catalog split are recorded in
`deviations.md`. Fresh local gates and separate official coverage comparisons are
in `defaults-recovery-report.md`. R1-R6 stay partial and unresolved. Repository CI
on the new pushed checkpoint and fresh verify (claude-code-provider.1.5) remain
required before deliver (claude-code-provider.1.2).

Criteria in each task's order. Recovery tasks 1.3.53-1.3.60 carry their own
evidence in their `deviations.md` entries; their outcomes are folded in here.

| Task | # | Evidence |
|---|---|---|
| 1.3.1 Spike | 1 | `spike-findings.md` §§1-7 |
| | 2 | §1: cap 4, 600 s idle |
| | 3 | `plain.jsonl`, `permission.jsonl`, `subagent.jsonl` under `testdata/claudecode/2.1.220/`; `TestFakeClaudeReplaysFixture` |
| | 4 | "Design effects" table and its `deviations.md` entries |
| 1.3.2 Runtime seam | 1 | `make test` passes (check run below) |
| | 2 | Every create path takes the platform or the enabled default (FR-2 row); directory-bearing REST creation regressed at main integration and is restored by `TestRESTCreateResolvesDefaultBeforeRuntime` and `TestRESTCreateAuthoritativeOwner`. Remaining `opencode` literals are OpenCode adapter, OpenCode-only runtime controls and the legacy RPC alias |
| | 3 | `TestStartupWithoutOpenCode`, `TestClaudeOnlyStartup` |
| | 4 | `TestEnsureRuntimeAndLegacyRPC`, `TestEnsureRuntimeFallsBackOnlyForOpenCode` |
| 1.3.3 Status sinks | 1-2 | `TestPlatformIdleSinkDrainsQueue`, existing queue tests |
| | 3 | `TestLimitedSessionDoesNotDrainSweep`, `TestLimitedRunRemainsRunning` |
| | 4 | `StatusBadge.test.tsx` |
| 1.3.4 Judge seam | 1-3 | `TestOpenCodeJudgeThroughInterface`, `TestJudgeWithoutBackendNeedsUser`, `TestSafeCommandCachePrecedesBackendSelection` |
| 1.3.5 Remote advertisement | 1-2 | `TestManagerAdvertisedPlatforms`, `TestManagerLegacyHelloDefaultsToOpenCode` |
| | 3 | `TestPublishAdapters_RejectsDuplicateRemoteIdentity` |
| 1.3.6 Protocol | 1 | `TestSessionContract`, `TestMutationContract`, `TestEventContractAndReplay` |
| | 2 | `TestProviderInstancesAreIsolated` |
| | 3 | `TestCapabilitiesGateEveryOptionalMethod` |
| | 4 | Partial, deferred (R4): mismatch errors tested by `TestVersionsAndHandshakeErrors`, `TestHandshakeRequiresExplicitVersionAndCLI`; no previous version is spoken |
| 1.3.7 Supervisor runtime | 1 | `TestKilledDashboardProcessReplaysAfterItsLastCursor`; for Claude, `TestClaudeSupervisorSurvivesOcmanRestart` |
| | 2 | `TestDashboardRestartReplaysEveryEventAndPendingPermission`, `TestClaudeSupervisorSurvivesOcmanRestart` |
| | 3 | `TestProviderDeadPersistedRowIsReprobedAndRelaunched`, `TestProviderRecover` |
| | 4 | `TestProviderVersionSkewDrainsWithoutInterruptingTurn`, `TestDrainWaitsForTrulyIdleSessions`; pending and later prompts while draining `TestOutOfRangeSupervisorDeliversPromptsWhileDraining` |
| 1.3.8 Read-only browsing | 1 | `TestClaudeOnlyStartup` |
| | 2 | `TestWarmStartParsesNoFiles` |
| | 3 | `TestPagingReadsOnlyRequestedRange` |
| | 4 | `TestSubagentRendersUnderTaskCall`, live: `TestLiveSubagentStaysUnderTask` |
| | 5 | `TestDualPlatformKeepsOpenCodeListing` (NFR-1 row) |
| 1.3.9 Hooks | 1-4 | `TestHookPluginInstallUninstallKeepsSettings`, `TestRunHookReturnsWithinTimeoutWhenOcmanIsDown`, `TestTerminalSessionHooksDriveStatus`, `TestOwnedSessionIgnoresHooks` |
| 1.3.10 Interactive | 1 | `TestClaudeComposerStart`; session-page follow-ups enabled by the live-connection overlay (1.3.56). Incompatible implicit model/agent defaults found at `e4d4d25` are corrected with real-composer rendered platform/machine regressions in `NewConversation.lifecycle.test.tsx`; explicit supported custom selections remain usable |
| | 2 | `TestIdleTimeoutResumesSession` |
| | 3 | `TestUsageLimitHoldsUntilReset` (synthetic rejection) |
| | 4 | `TestUnsupportedCLIVersion` |
| | 5 | `scripts/check-platform-branching.sh` in `make lint` |
| 1.3.11 Permissions | 1 | `TestYOLOParentChildIsNotPrompted` |
| | 2 | `TestAllowAlwaysIsRecordedAndInherited` |
| | 3 | `TestAskUserQuestionAnsweredFromOcman` |
| | 4 | `TestClaudeJudgeDecidesWithoutOpenCode` |
| | 5 | `TestRuleAllowedPromptNeverReachesBrowser`, `TestRuleReplyFailureRetriesWithoutLeaking`, `TestPermissionInboxReconciliationNeverPollsPushOnlyPlatforms` |
| | 6 | `TestPendingPromptAnsweredAfterReconnect`, `TestClaudeSupervisorSurvivesOcmanRestart` |
| 1.3.12 Entrypoints | 1-4 | `TestClaudeWorktreeCreateAndLaunch`, `TestClaudeRoutineSettlesOnIdle`, `TestClaudeMCPCreate`, `TestClaudePRLaunchWorktree` |
| 1.3.13 Factory and plugins | 1-2 | `TestClaudeFactoryEpicDispatchesToClaude`, `TestClaudePluginConversationCreatesAndResumes` |
| | 3 | Existing Factory and plugin suites pass in `make test` |
| 1.3.14 Analytics | 1 | `TestUsageViewsCountClaudeTranscriptTokens`, `TestClaudeOnlyStartup` (Claude-only `/api/stats`, `/api/metrics`) |
| | 2 | `StatsLogTables.test.tsx` |
| | 3 | `ProviderAccountSettings.test.tsx`, `TestDrivenSessionRecordsRateLimit`, `TestAccountNeverReadsCredentials` (approved contract, gate 1.3.68) |
| 1.3.15 Remote Claude | 1-3 | `TestHubDrivesRemoteClaudeSession`, `TestHubAnswersRemoteClaudePermission`, `TestClaudeRemoteFailsClosedWhenDisconnected` |
| 1.3.16 Docs | 1-4 | `docs/features/claude-code.md`; `AGENTS.md`; `docs/other/architecture.md`; `make docs-build` |
| 1.3.97 Verification 1.5 fixes | 1-9 | *Verification 1.5 blockers fixed (1.3.97)*; criteria 1, 2, 4 and 6 were incomplete at `bf1548e2` and are completed by 1.3.99 |
| 1.3.99 Verification 1.5 recovery | 1 | `TestResultBeforeTicketRemovalSettles` |
| | 2 | `TestUnrecordedReplyIsRetriedNotRedelivered`; incomplete at `f279f45c` (conflicting retry, cancellation, outage through exit), completed by 1.3.105 |
| | 3 | `TestOutOfRangeSupervisorDeliversPromptsWhileDraining`, `TestDrainingAdapterUsesOnlyVersionIndependentRoutes`; browser-first ordering completed by 1.3.105 |
| | 4 | `TestSnapshotCarriesTerminalState` |
| | 5 | `TestStartedPersistenceFailureFailsTurn` |
| | 6 | `TestProjectSupervisorFollowsDashboardConfiguration` (fake `--resume` validation); partial, deferred: rename is not configuration-scoped (R3) |
| | 7 | `TestHelperSlotsAreBoundedAndSeparate`, `TestClaudeJudge`, `TestBrokenSlotStoreFailsVisibly`; separate budget approved at gate 1.3.107 |
| | 8 | `TestFakeCLIRejectsMalformedInput`, `TestAbortInterruptsTurn`, `TestClaudeComposerStart`, `TestDashboardKillRestartComposition` |
| | 9 | *Intermittent check failures at verification 1.5 (1.3.99)* |
| | 10 | This file, `deviations.md`, PR 834 inventory; check run below; corrected by 1.3.105 |
| 1.3.105 Verification 1.5 recovery at `f279f45c` | 1 | `TestAllowAlwaysWithWildcardCharactersIsExact`, `TestExactPatternsDoNotGlob`, `TestExactPatternRoundTrip`, `TestFlatKeysAreExactOnClaude` |
| | 2 | `TestSpoolOutageAcrossContinuationAndExitSettles` (continue and exit) |
| | 3 | `TestCancellationDuringOutageIsRecorded` (interrupt and exit), `TestCancelDuringReplyWriteResolvesOnce` |
| | 4 | `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts` |
| | 5 | `TestConflictingReplyRetryIsRefused` (session service, hooks, rules, provenance), `TestConflictingQuestionRetryIsRefused`, `TestTentativeAllowAlways` |
| | 6 | Gate 1.3.107: both departures approved by the human |
| | 7 | This file, `deviations.md`, PR 834 inventory; check run below; criteria 1-5 and 7 corrected by 1.3.108 |
| 1.3.108 Verification 1.5 recovery at `251290f6` | 1 | `TestLegacySupervisorPatternsAreExact`, `TestLegacyRulesStoreApprovalsBecomeExact`, `TestLegacyClaudeApprovalsInheritExact`, `TestVersionsAndHandshakeErrors` |
| | 2 | `TestSettlementRetryOwnsEveryGeneration` |
| | 3 | `TestLateResolutionKeepsAskAndDoesNotReopenTurn`, `TestLateResolutionAfterIdleAndReconnect` |
| | 4 | `TestDecisionDeliveredBeforeExitStands` |
| | 5 | `TestBrowserConnectedBeforeDrainDiscoveryReceivesPrompts` (`browsing` and `routed`) |
| | 6 | `TestAllowAlwaysOutcomeSurvivesStoreFailure` |
| | 7 | `TestDelayedAIApprovalKeepsProvenance` (judge and cache), `TestAIApprovalIsNotOverwrittenByUser`; partial, deferred: lost response, restart before recording and unreadable rows (R2) |
| | 8 | This file, `deviations.md`, PR 834 inventory; check run below; criteria 1-3 and 7 corrected by 1.3.120 |
| 1.3.120 Verification 1.5 recovery at `fdcda97c` | 1 | `TestLegacyInheritedRulesBecomeExact` |
| | 2 | `TestStaleTurnFailureSparesNextTurn` (the `settle` path only); partial, deferred: a failed terminal message still kills the newer turn (R1) |
| | 3 | `TestRetainedIdleDoesNotDrainQueueIntoNextTurn` |
| | 4 | `TestLegacySupervisorLeavesApprovalToUser`, `TestLegacySupervisorPatternsAreExact`, `TestVersionsAndHandshakeErrors` |
| | 5 | Partial, deferred (R2): `TestDelayedAIApprovalKeepsProvenance` (emitted events, lifecycle), reducer *shows a replayed AI approval as the AI before or after the persisted notice* |
| | 6 | This file, `deviations.md`, PR 834 inventory; check run below; R1-R6 found afterwards at `b03e27cc` and deferred (1.3.124) |
| 1.3.124 Delivery report | 1-5 | `delivery-report.md`; *Delivery deferrals at `b03e27cc` (1.3.124)*; PR 834 links both; *Delivery report check run (1.3.124)* |
| 1.3.17 Review | 1-4 | Superseded by this re-audit: `deviations.md` (every entry checked, the protocolVersion claim corrected, EnsureRuntime shapes logged); this file; PR 834's description lists the current deviation titles |

## Testing section

| Item | Status | Evidence |
|---|---|---|
| Fake `claude` replays fixtures and rejects malformed input | implemented | `TestFakeClaudeReplaysFixture`, `TestFakeCLIRejectsMalformedInput`, `TestFakeClaudeValidatesControlResponses` |
| Contract tests on both sides | implemented | `pa/*_test.go`, `ps/providertest`, `cp/*_test.go` |
| Restart test mid-turn and mid-prompt | implemented | Fake provider `pa/replay_test.go`; Claude `TestClaudeSupervisorSurvivesOcmanRestart` (real `ocman provider claudecode` process, fake CLI); whole dashboard process killed with SIGKILL and restarted `TestDashboardKillRestartComposition` |
| Optional manual real-CLI smoke test | optional, not done | *No real-CLI smoke test and no Claude restart test* |

## Check run (1.3.61)

*Re-audit check run (1.3.61)* in `deviations.md`: lint, tests, build, proto,
docs and Chromium E2E pass; Go coverage passes the current gh-pages baseline;
frontend coverage (86.26) fails the current baseline (88.81) and is deferred to
delivery's main merge by gate 1.3.67, with the branch above its merge-base
(85.25).

## Check run (1.3.97)

*Fix check run (1.3.97)* in `deviations.md`: lint, Go and frontend tests,
race tests on the changed packages, builds, proto, docs and Chromium E2E pass;
Go coverage passes the current gh-pages baseline; frontend coverage (86.27)
fails the current baseline (89.23), above the previous checkpoint (86.26), and
is repeated at delivery after the main merge (gate 1.3.67).

## Check run (1.3.99)

*Recovery check run (1.3.99)* in `deviations.md`: lint, Go and frontend tests,
race tests on the changed packages, builds, tidy, proto, docs and isolated
Chromium E2E pass; Go coverage passes the current gh-pages baseline (86.6 vs
86.7, -0.10); frontend coverage (86.27) still fails the current baseline
(89.23), unchanged from `bf1548e2`, and is repeated at delivery after the main
merge (gate 1.3.67).

## Check run (1.3.105)

*Recovery check run (1.3.105)* in `deviations.md`.

## Check run (1.3.108)

*Recovery check run (1.3.108)* in `deviations.md`.

## Check run (1.3.120)

*Recovery check run (1.3.120)* in `deviations.md`.

## Check run (1.3.124)

*Delivery report check run (1.3.124)* in `deviations.md`.

## Main integration check run (1.3.134)

`integration-report.md` and *Main-integrated check run and official coverage
comparison (1.3.134)* in `deviations.md`. Required checks pass after the recorded
integration failures were corrected. `TestProviderMainMigrationReconcilesBothHistories`
failed without migration reconciliation and passes with it. New MCP creation
tests cover both Claude targets; platform-switch tests retain draft identity and
use main's project-default agent. `TestProviderIdleResolvesOwnerWaits` checks the
common idle sink without resolving another platform's wait. R1-R6 remain deferred.
