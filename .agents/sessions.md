# Sessions, worktrees and turn lifecycle

## Creation and drafts

Read `internal/sessionlifecycle` for prepare/start and first delivery, and
`internal/sessionsvc` for shared REST/gRPC session mutations.

- A new conversation is only `/session/new?dir=…&remoteId=…&platform=…` until
  first submission. Do not create a session, worktree or placeholder earlier.
- `/api/sessions/prepare` gets catalogs through `Platform.DirectoryCatalog`,
  owner-routed through the remote `Call` RPC. It never launches OpenCode:
  use an existing instance or historical models. Submission waits for catalog
  preparation; failures have a visible retry.
- Worktree eligibility reads git info, the owner's worktrees and verified base
  ref. Linked worktrees, non-repositories and repositories without a usable
  commit use the current checkout. Prefer a remote-tracking default when its
  local branch is missing, then current branch or detached HEAD. Never assume
  `main`. Fully qualify named refs. Git probe errors remain retryable errors,
  not missing commits. Factory requires a named delivery target, falling back
  from detached defaults to verified local `main`/`master` or explicit selection.
  Legacy recovery uses the same preparation.
- Machine selection changes the route only. Each draft retains its `draftId`
  query parameter and `draftKey`. Prepared local conversations appear in the
  sidebar, first within their project in grouped view, before sessions exist.
- All composer text, prepared metadata, start and first-delivery receipts live
  in `lib/draftDb`, IndexedDB `ocman.drafts.v1`. Claims, failure, completion with
  retirement/relocation and discard each use one readwrite transaction. Never
  add another draft storage path.
- `composerDraft` and `newConversationDrafts` keep synchronous snapshots hydrated
  before render in `main.tsx`. BroadcastChannel triggers re-reads across tabs.
  Text revisions only grow and reject stale autosaves. Attachment snapshots use
  Zustand so pending image reads survive reopening. Tests use fresh fake
  IndexedDB per case through `vitest.setup.ts`.
- `/api/sessions/start` receives directory, owner, platform, worktree, title,
  prompt and send data. Explicit owner is authoritative. Derive omitted platform
  from it and reject mismatches before launch. Apply owner/project quick-setting
  permission defaults before publication and first prompt. Saving YOLO requires
  browser confirmation; existing sessions keep their rules.
- Worktree starts create owner-local `session-<id8>` without an LLM in the first
  message path. Current-checkout starts use the directory's project instance.
  Plain prompts deliver server-side in the same request, returning
  `firstMessageSent`/`firstMessageError`. Custom slash commands and shell inputs
  run client-side on the created session; ocman built-ins need an existing one.
- Show the first prompt and server progress while starting. Progress SSE events
  are `ocman.session.start.progress`, tagged with `startId`, through
  `hostsvc.WithProgress`. Context progress does not cross gRPC; the hub reports
  remote worktree progress around the call. Show parallel steps together.
- Lock the composer and machine/target controls during start. Claims prevent
  duplicate submission across tabs/reloads. Never automatically replay a start
  transport failure. Retire only the unchanged submitted revision, including
  background starts. Navigate only the currently open initiating draft.
- Failed first sends retain the whole payload in shared in-memory `failedSends`.
  The capped localStorage copy is best-effort; quota errors cannot erase live
  recovery. Non-image attachments remain browser Files until creation, then
  upload before send. Upload through `Host.SaveComposerAttachment` to the owner's
  cache; remote uploads stream over gRPC and return owner-local paths.
- Upload, command, shell and delivery failures use child-keyed `firstSubmission`
  with visible retry, never overwrite the child's draft. Block follow-ups until
  first delivery finishes; keep permission/question controls available.

## Worktrees and permissions

`/wt` and the project Worktrees view use `<repo-parent>/.worktrees/<repo>/<slug>/`.
Do not create a per-worktree tmux instance. See [platforms.md](platforms.md) for
runtime sharing and `spec/worktree-sessions/` for design history.

- Name a new worktree in the background through OpenCode's `title` agent using
  `small_model` or Haiku and delimited branch-naming instructions. Slug the title
  into `git branch -m`; keep the path. Failure preserves the provisional name.
  Keep the session's default title for native first-message naming, not the
  branch name. Hide the helper session titled `(worktree-name subagent)`.
- `/wt` from an existing session passes `parentSessionId` to create-and-launch.
  New conversations have no parent to inherit from.
- Record AI and user approval provenance, including one-time decisions and
  original metadata, for command footnotes. Only user `always` and judge-safe
  approvals contribute to inheritance; user `once` is audit-only.
- `permissions.BuildInheritedRulesWithLive` merges accumulated approvals and the
  parent's live `Platform.PermissionRules`, with live rules last. This preserves
  YOLO/custom modes that are not stored as approvals. Apply the child rules
  before first send. Inheritance snapshots at split, defaults on through
  `worktree.inherit_permissions`, and soft-fails without blocking launch.
- Launch seeds `external_directory` in `OPENCODE_PERMISSION` for the project's
  worktree root. Already-running instances lack that seed; runtime auto-approval
  covers them.
- Nested permission/question prompts resolve to the top-level ancestor through
  recursive `db.GetSessionParentIDs`, including native or deeply nested subagents,
  so the visible session row remains actionable.

## Status and snapshots

`db.SessionStatus` and the TypeScript union are closed sets: `busy`, `waiting`,
`done`, `error`, `interrupted`. `db.SettleSessionStatus(turn, live, inferred)` is
the single decision point. Running status comes from the agent lifecycle, never
stored-message guesses.

- OpenCode live status comes from `/session/status` and `/global/event`, cached
  by instance port in `live_status.go`. Drop state when a port disappears and
  re-seed on restart, never persist it.
- Seed each instance's directory and existing worktrees with sessions updated
  within 24 hours or open `tool-calls`/`unknown` turns. One failed read leaves
  the port unseeded. `InferSessionStatus` only chooses a terminal state; it never
  means running. No live view with an unfinished turn means `interrupted`.
- V2 waits for foreground execution, session-owned shells and active native child
  agents. Child activity bubbles through native parent links, never application
  worktree links; the main agent's foreground state stays separate. Rebuild shell
  ownership from loaded locations on reconnect; track shell creation, exit and
  deletion. Unrelated ownerless shells cannot hold sessions busy. Complete stream
  ownership bootstrap before headers, then replay buffered lifecycle events so
  status seeding cannot lose a shell's final idle edge.
- Do not reintroduce `STATUS_GRACE_MS` or sticky-busy sidebar merging.
- Canonical adapters read persisted session data only from statev2;
  `owned.New` requires the store. Native transcript/database reads belong to
  ingestion, identity probes or explicit runtime-safety probes, never a browsing
   fallback. Do not add browsing methods to native integrations. Sidebar lists use the compact `session_sidebar` projection and `internal/sidebar` reader, not accounting summaries. Event-cache
  enrichment may change only live observation fields, not persisted facts.
- Acknowledge stored refreshes before broadcasting a new row or title. Refresh
  tickets must be captured before the source snapshot starts; unrelated receipts
  and older in-flight reads cannot satisfy them. Creation metadata is persisted
  before publication, and partial runtime bootstraps never authorize deletion.
- Keep native hierarchy separate from application worktree links. Retained
  sources remain readable without borrowing another physical source's native
  IDs for live status or execution. There is no native session-list cache;
  native HTTP caches hold only live configuration. Discovery stays per adapter.
- Auto-archive sessions inactive for seven days, checked daily on all platforms.

## Sending and queues

Plain Enter sends immediately, including mid-turn. Ctrl/Cmd+Enter explicitly
queues. Never derive the `queue` flag from inferred status.

- With `queue: false`, call shared `sendNow` without touching the held queue.
- With `queue: true` on v2, use `platforms.NativeQueue`, `delivery: "queue"`,
  unless ocman's queue already holds messages for that session.
- `queuesvc` owns the queue domain; `queuemod` owns routes, native coordination,
  idle dispatch, sweep recovery, quota/model fallthrough and settings. Keep
  tables and cooldown persistence in `internal/state`.
- A drainable serialized flush sends one held message per `session.idle` edge.
  Trust the edge; do not re-check lagging inferred status. A one-minute sweep
  recovers missed edges/crashes, using one owner-routed `LifecycleReader` call
  for settled status and latest message, never the full transcript.
- Bind workers in `Start`; stop idle admission and join admitted sends and the
  keyed dispatcher in `Run` before state.db closes. Accepted deliveries finish
  durable receipts during shutdown, never cancel between admission and dequeue.
- Release admission locks before owner cooldown reads. Those reads have lifetime
  deadlines, and cancelled reads leave payloads held. Sweep, lifecycle and model
  reads remain cancellable; only admitted delivery and durable receipts detach
  cancellation. `sessionsvc.PrepareSendMessage` prepares owner/model/first-prompt
  reads before mutation admission. Runtime recovery reads also retain deadlines.
- `Server.onSessionIdle` records cooldown and admits flush before synchronous bus
  publication. Direct send and owner-routed recovery remain in `session_send.go`.
