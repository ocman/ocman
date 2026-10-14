---
title: Architecture
weight: 6
---

## Introduction

Ocman is a single Go binary that serves a React SPA and acts as a control
plane for coding-agent sessions. The main diagrams cover the system context
(what ocman talks to), the backend composition (the Go packages), the
session/event data flow, frontend composition, and plugin action flow. Detail
lives in the text below each diagram.

### Cross-machine conversation sharing

Ocman can publish a shared conversation through the standalone `ocman-relay`
binary. The relay stores only AES-256-GCM ciphertext through the `share.Store`
abstraction (disk today; object storage can implement the same
`Put`/`Get`/`List`/`DeletePrefix` contract). The per-share key stays in the URL
fragment, so it never reaches the relay.

```mermaid
flowchart LR
    Owner[Owner ocman] -->|sealed completed turns<br/>artifact snapshots| Relay[ocman-relay]
    Relay --> Store[(share.Store)]
    Viewer[Browser viewer] -->|ciphertext poll| Relay
    Viewer -->|WebCrypto decrypt| Thread[Shared conversation]
    Thread -->|explicit local fork| Local[Recipient ocman]
```

- Chunk zero is a complete snapshot; later chunks contain the latest
  completed turn and the viewer merges them as id-keyed upserts.
- The owner allocates sequence numbers. Nonces derive from those sequence
  numbers, and the share id plus sequence is authenticated as GCM AAD.
- Revocation deletes the relay prefix. The relay stores only a hash of its
  append/delete token and needs no database.
- Forking fetches and decrypts in the recipient's authenticated local ocman
  UI, lets the recipient choose a local project, and parks the transcript as
  an unsent composer draft. Imported text never runs automatically.
- Artifact shares reuse the relay but are written once: chunk zero is a sealed
  manifest (title, links, file list) and later chunks are the sealed file
  bytes. Ocman checks the relay's per-share byte and chunk limits before
  uploading, and deletes a half-written share on failure. See
  [Artifacts](../features/artifacts.md).

## 1. System context

Everything external that the ocman process touches.

```mermaid
flowchart LR
    Browser[Browser SPA<br/>REST + SSE] -->|core APIs, project merges, usage heartbeats + plugins| Ocman[ocman<br/>Go binary :8228]
    Agent[AI agents<br/>MCP clients] -->|/mcp| Ocman
    Ocman -->|read-only SQLite<br/>maintenance writes| OCDB[(opencode.db)]
    Ocman -->|read/write SQLite<br/>Inbox + state| StateDB[(state.db)]
    Ocman -->|read/write SQLite<br/>analytics copy of opencode.db| Cache[(analytics-cache.db)]
    Ocman -->|managed inventory + authenticated HTTP/SSE| OCInst[Managed OpenCode<br/>instances]
    Ocman -->|stream-json pipes, user's own login| ClaudeCLI[claude CLI<br/>children]
    Ocman -->|read + index| ClaudeDir[(~/.claude/projects<br/>transcripts + claude-index.db)]
    ClaudeHooks[claude hook plugin<br/>terminal sessions, opt-in] -->|ocman hook → /hook| Ocman
    Ocman -->|exec| Shell[git / tmux<br/>host tools]
    Ocman -->|describe + supervised serve / NDJSON| PluginExec[Trusted native plugin processes]
    PluginExec -->|Beads plugin only| Beads[bd<br/>owner-local CLI]
    Ocman -->|REST| APIs[GitHub / Forgejo<br/>provider usage + reviewer APIs]
    Ocman -->|credential-free HTTP, public addresses only| WebPages[Open Graph pages<br/>and raster preview images]
    Ocman -->|encrypted Web Push + VAPID| Push[Browser push services]
    Push -->|wake service worker + OS notification| Browser
    Ocman <-->|gRPC + token: sessions, upstream identities, hosts, plugins| Remotes[Remote ocman<br/>instances]
    Ocman -->|encrypted webhook poll| Relay[ocman-relay<br/>ciphertext persistence]
    Ocman -.->|OTLP traces, metrics, logs, optional| Otel[Telemetry collector]
```

- **Browser SPA.** The only UI. It talks REST/SSE to the hub, never to
  remotes directly. `:8228` is the production `-addr` default; in dev the
  Vite server on :8228 proxies `/api` to the air backend on :8229.
- **Public web pages.** The hub reads Open Graph metadata on delayed markdown
  link hover and proxies preview images. Provider-specific cards take priority.
- **Browser push services.** Opt-in device subscriptions receive encrypted Web
  Push alerts even with the page closed. The browser service worker displays
  session-context notifications and opens an owner-qualified session route on click.
- **Telemetry collector.** Optional OTLP/HTTP or gRPC export of traces, metrics,
  and batched Logrus logs through `internal/telemetry`. Logs retain structured
  fields and span context, console output stays enabled, and shutdown flushes
  all three signals. The bundled LGTM stack routes logs to Loki.
- **UI usage.** Authenticated browsers send active foreground intervals once
  a minute and on focus/visibility changes. The hub unions overlapping tabs
  and devices in one state.db transaction, retains two minutes of deduplication
  history, and stores UTC daily totals. Newly committed seconds increment
  `ocman.ui.active_time` through the existing OTel exporter without client or
  date attributes. Dashboard graphs read SQLite, so collector outages cannot
  erase their history. Five minutes without interaction stops counting; long
  timer gaps do not count sleep. This is installation-wide, not named-user data.
- **opencode.db.** Foreign data, opened read-only. The one exception is the
  user-started maintenance job (`internal/ocmaint`, Settings → Maintenance).
  It stops the managed instances, refuses while any other process holds the
  file, and moves old `summary.diffs` patches into a restorable dump. Only this
  opt-in maintenance operation uses lsof to check file holders; missing lsof
  makes maintenance fail closed, not application startup.
  On an OpenCode v2 machine each connection gets TEMP views named `session`,
  `message` and `part` over v2's `session_v2`/`session_message` tables, so
  every query keeps its v1 SQL (`internal/db/v2views.go`).
- **state.db.** Ocman's own state: archive flags, routines and run history,
  permission approval provenance, live session commit observations, settings,
  Factory records, Inbox items, artifact metadata, remote tokens, and the last
  local projects-index snapshot. Artifact file bytes sit beside it in a
  SHA-256 content-addressed `artifacts/blobs/` directory. Inbox sends are owner-local and persist until recalled or archived
  by the user. Legacy `workflow_*` rows remain inert for manual recovery.
- **analytics-cache.db.** A disposable copy of opencode.db's message and
  session rows, stored beside state.db, which the analytics queries read.
  User-message attachments are stripped from the copy. A minimal tool-timing
  projection is refreshed in the same sync transaction as its messages, so
  warm analytics reads do not parse historical tool outputs. Tool names let
  hourly agent run minutes exclude question-tool waits. That graph unions
  completed assistant intervals per session, includes other tools, subtracts
  recorded human waits from state.db, and sums subagents separately. Human
  waits are observed after the approval decision, even with autoapproval off;
  resolution and idle edges close them. Each analytics read bounds a wait to
  its originating completed assistant interval before applying the date filter,
  so missed reply/idle events across a restart cannot remove later turns. Timing
  read failures stay errors rather than reconciling against an empty snapshot.
  Older permission lifecycle records
  fill in history when available. Missing historical observations remain unknown.
  TPS divides
  output tokens by completed message time minus the union of tool intervals,
  including recorded permission/question waits. Missing tool timing excludes
  the sample. This is request throughput, including startup/prefill latency,
  rather than provider-side decode speed; elapsed-duration metrics keep tool time.
  Session message metadata, turn summaries, and the composer use the same
  calculation on loaded messages and parts. A stale copy gets a
  quick incremental sync before an analytics read; a read never waits on a
  sync already running. The first build and the 6-hourly full rebuild run in
  the background, and the rebuild time is persisted so restarts cannot
  postpone it. Delete the file and it is rebuilt
  (`internal/db/analytics_mirror.go`).
- **Claude Code** (`-platforms claudecode`). Ocman reads the transcripts under
  `~/.claude/projects` (or `$CLAUDE_CONFIG_DIR`) into the disposable
  `claude-index.db` beside state.db and drives the sessions it starts by
  spawning the user's own `claude` binary with pipes (stream-json and the
  control protocol), so every call runs under the user's own account. The
  opt-in hook plugin, installed with `claude plugin install`, reports terminal
  sessions' status to the MCP listener with a 1 s timeout and always exits 0.
  See [Claude Code](../features/claude-code.md).
- **Provider usage APIs.** The subscription usage page reads OpenCode's local
  OAuth credentials server-side and returns only normalized quota windows;
  provider tokens and account identifiers never reach the browser.
- **Custom reviewer APIs.** `internal/autoapprove` can send permission judgments
  directly to a user-configured OpenAI-compatible chat-completions or
  TypeSafe-compatible System One endpoint instead of a temporary OpenCode
  session. The endpoint's own Bearer key stays in this instance's `state.db`
  and is never returned by settings reads. These calls use a separate HTTP
  client from OpenCode authentication, reject redirects, bound response sizes,
  and fall through to human review on failures. Cached safe verdicts include
  the endpoint configuration used by the original judgment.
- **Native plugin executables.** Startup and explicit rescans describe direct
  executables from the local plugin directory. Each describe has a fresh token,
  minimal environment, bounded output, and a fifteen-second deadline. Duplicate
  identities conflict; changed binaries lose approval. Discovery itself executes
  trusted code. Grants minimize brokered context, not operating-system access.
  See [Plugins](../features/plugins.md) for installation and operations.
- **Remote ocman instances.** The hub dials remotes over gRPC and re-exposes
  their sessions and hosts transparently. The owning remote enriches session
  detail with its persisted approvals and tees synthetic approval events into
  the gRPC event stream before the hub forwards them to the browser.
  Plugin catalog, management, health, actions and artifact reads use the same
  authenticated connection. Each machine discovers its own binaries and stores
  its own configuration and secrets. The hub never installs remote binaries.
- **Project identity.** Each owner enriches its project snapshot with Git fetch
  remotes inside the existing singleflight refresh. Loaded snapshots return
  immediately while background discovery runs; inventory projection reuses the
  enriched rows without running Git. Per-directory discovery also coalesces
  concurrent callers and caches successful results for five minutes. A failed
  refresh preserves the previous keys and snapshot and can retry immediately.
  Credential-free upstream keys normalize SSH and HTTP
  transport syntax, `.git` suffixes, host casing and default ports. The hub assigns
  a shared `projectKey` to checkouts connected by any common upstream, including
  chains through a checkout with multiple remotes. `/api/projects` keeps each
  owner's checkout row, counts and archive state. The browser groups those rows
  and their sessions by `projectKey`; project detail includes sessions from every
  matching host. Actions still target a specific owner and directory. Repositories
  without network remotes fall back to owner-scoped directories, with the existing
  worktree folding. Local filesystem remotes never identify cross-host projects.
  The dashboard's merge action saves owner-qualified source-checkout to target-checkout mappings in
  the hub's `state.db` setting `projects.merges`. `/api/projects` applies it after
  upstream grouping, resolving the checkouts' current keys so Git remote changes
  do not undo saved merges, and includes the selected target checkout for display.
  Dashboard totals, sidebar groups and project session lists share this identity.
  Merge chains follow the final target, and a transaction rejects cycles.
  Upstream memberships and saved checkout edges form connected components, so
  crossed upstream groups keep all saved merge members together. If a component
  has multiple saved destinations, the lowest stable checkout key selects its
  display representative independently of inventory order.
  Session directories, owners and transcripts stay intact. Successful merges
  publish `ocman.projects.changed` so other browsers refresh their project list.
- **Encrypted webhooks.** Providers submit plaintext to the relay's ingestion
  URL, where ocman encrypts a versioned age X25519 envelope. The relay
  persists ciphertext plus visible size/timing metadata; the owning local or
  remote ocman decrypts it, durably accepts it in `state.db`, claims routine
  dispatches, then acknowledges the relay. A lost acknowledgment is retried
  without duplicate scheduling.

Remotes advertise enabled local platform ids in `Hello`. The hub registers
one adapter per platform under `r-<instanceId>:<platform>`, sharing one host
adapter and connection. Older remotes without the list remain OpenCode-only.
Duplicate instance ids are rejected for the whole remote, even when the
advertised platforms differ.

## 2. Backend composition

Server shutdown cancels and joins its owned background loops before returning,
so closing the databases cannot race a pending refresh.

The Go package graph, collapsed to the seams that matter.

```mermaid
flowchart TD
    Main[main.go<br/>embedded skills + build version] --> App[internal/app<br/>startup + composition]
    App --> Server
    App --> Registry
    Server[internal/server<br/>HTTP, SSE, handlers] --> Registry[platforms.Registry<br/>session seam]
    Server --> Router[hostsvc.Router<br/>host/dir + runtime seam]
    Server --> AutoApprove[internal/autoapprove<br/>safe-command cache + Judge seam]
    AutoApprove -->|per-permission backend selection| Judge[OpenCode judge, else<br/>claude -p haiku judge]
    Server -->|"modules(): routes, Start, Launch, Run, Close, tools, Bus"| Modules[feature modules<br/>autoapprove, queuemod, factorymod, pluginsmod, routines, sessiontitles, webhook, artifacts, inbox, sharing, notifications, snooze, tmuxmod, filesystem, projectforge, previews, analytics, termmod, ocmaint, gitmod, remotesmod]
    Modules --> Lifecycle[internal/sessionlifecycle<br/>prepare, start + direct actions]
    Lifecycle -->|mutations + first delivery callback| Sessions[internal/sessionsvc<br/>shared agent mutations]
    Modules -->|"queuemod: bounded preparation, then admitted delivery"| Sessions
    Lifecycle -->|creation, streamed attachments, restart + reload| Router
    Server --> Recovery[internal/sessionrecovery<br/>interruption history + replacement fences]
    Recovery -->|owner-local lifecycle reads| Registry
    Recovery --> State
    Modules --> Routines[internal/routines<br/>saved prompts + schedules]
    Modules -->|"factorymod: routes, tools, launchers, control sessions"| Factory[internal/factory<br/>YAML workflows + Issue dispatch]
    Factory --> FactoryModel[internal/factory/model<br/>shared persistence records]
    Factory -->|graph + action Inbox reconciliation| State
    State --> FactoryModel
    Factory --> Registry
    Factory --> Router
    Factory -->|Project Delivery validation + merge observation| Forge
    Routines --> Registry
    Routines --> Router
    Modules -->|"tmuxmod, termmod, gitmod: tmux, terminals, git + worktrees via Host"| Router
    Routines --> State
    Server --> MCP[internal/mcp<br/>MCP tools]
    Modules -->|module tools| MCP
    Modules --> Inbox[internal/inbox<br/>state + owner routing]
    Modules -->|notifications: subscriptions + durable delivery receipts| State
    Modules -->|snooze: archive deadlines + atomic wake-up| State
    MCP --> Registry
    Registry --> OC[platforms/opencode + internal/db<br/>adapter and read-only queries]
    Registry --> ClaudePlatform[platforms/claudecode<br/>browsing + project routing]
    ClaudePlatform --> Provider[platforms/provider<br/>per-instance socket adapter]
    Provider -->|providerprotocol: HTTP + SSE over unix socket, leased snapshot + replay| Supervisor[providersupervisor<br/>sequenced spool, ack, drain]
    Local -.->|ocruntime.BackendRuntime in tmux, one per project| Supervisor
    Modules -->|idleprojects: idle timeout through hostsvc.IdleProjectRuntime| ClaudePlatform
    Supervisor --> Claude[claudeprovider<br/>index, converter, pooled claude children]
    Registry -->|SessionEventSink: status + idle| Server
    Modules -->|Factory + routine usage via platforms.UsageReader| OC
    Modules -->|"analytics: Stats/Usage reads, size samples, OTel gauges"| OC
    Registry -->|session detail, summary, lifecycle + usage reads| RP[internal/remote<br/>platform adapter + owner RPCs]
    Router --> Local[hostsvc/local + composerattachments<br/>host operations, canonical roots + attachments]
    Router -->|streamed attachment writes on owner| RP
    Server --> State[internal/state<br/>state.db]
    Inbox --> State
    Inbox -.->|remote RPC| Router
    Server --> Forge[forge + integrations<br/>GitHub/Forgejo clients]
    Modules -->|"projectforge: PR/issue lists, checks, handle via owner Host"| Forge
    Modules -->|"previews: forge credentials only (Deps.Forges)"| Forge
    State -.->|registration types| Plugins[internal/plugins<br/>protocol, supervision, action broker]
    Modules -->|"pluginsmod: routes, supervision, conversation outbox"| Plugins
    Plugins -->|token-bound NDJSON + private configuration fd| PluginExec[External plugin process]
    Modules -->|owner-routed PluginOperation RPC| RP
    Modules -->|"remotesmod: /api/remotes CRUD, remote-access settings, inventory loop"| RP
    PluginSDK[sdk/plugin<br/>optional public Go SDK] -->|canonical DTO aliases + validators| Plugins
```

- **internal/app.** Application startup owns flags, database selection,
  authentication, logging, platform registration and HTTP/desktop composition.
  The root entrypoint only supplies embedded skills and the build version.
- **platforms/claudecode.** The Claude adapter composes the browsing and
  per-project supervisors, routes session operations, and recovers/drains
  project runtimes. Its constructor receives skills and the MCP address
  explicitly. Provider and hook subcommands live in the same module.
- **internal/server.** The HTTP mux, SSE broadcast and fanout, around 60
  handler files, plus tmux launcher and terminal host wiring, and whisper. Its session paths (SSE tee,
  notify, permission replies) still drive the auto-approve pipeline. It is the
  composition root for feature modules.
- **Feature modules** (`internal/module`). Auto-approve (`internal/autoapprove`:
  per-session toggle, approved-permission notices, judge settings, judge-delay
  seed and the headless watcher), follow-up delivery (`internal/queuemod`:
  queue routes, native inbox coordination, idle flush and sweep workers,
  model quota cooldown, continuation notices and fallthrough settings), Factory (`internal/factorymod`),
  plugins (`internal/pluginsmod`), the `/api/tmux` controls (`internal/tmuxmod`:
  sessions and launches through the owner's `hostsvc.Host`, client list and
  switch on this machine's tmux), the browser terminals (`internal/termmod`:
  window controls and the WebSocket bridge, attached through the owner's
  `hostsvc.Host`), OpenCode database maintenance
  (`internal/ocmaint`: `/api/maintenance/opencode-db` routes and the job
  runner; the server's launch gate asks it `LaunchBlocked`, and its `Run`
  holds the worker join until a running job finishes), remote management
  (`internal/remotesmod`: `/api/remotes` CRUD + reconnect, this instance's
  `/api/settings/remote-access` status and token reveal, and the inventory
  refresh loop among the joined workers; the `remote.Manager`, its gRPC
  transport and `Start`/`Stop`, the registry, the host router and owner
  resolution stay core), routines, webhooks,
  artifacts, the Inbox, conversation sharing and read-only file browsing
  (`internal/filesystem`: directory picker, signed `/api/file/` embeds,
  owner-routed repository file reads), link previews (`internal/previews`:
  `/api/previews/*`, link rules, sign-in apps, `/api/integrations/status`; it
  sees the hub forge clients only as credentials through `Deps.Forges`, and
  app access through `Deps.Access`), git and worktrees
  (`internal/gitmod`: status, branches, checkout, diffs and worktree
  list/base ref/remove/create-and-launch through the owner's `hostsvc.Host`;
  permission inheritance and the first send are injected server paths) each own
  their routes, background loops, MCP tools and event subscriptions. The
  server lists them once in `modules()` and hands each narrow `Deps` (store,
  guards, lookups); modules never import the server or each other. Webhooks
  dispatch to routines through an interface. Sharing and Factory's dispatch
  wake-up react to the typed, synchronous `module.Bus` `SessionIdle` event.
  `sessionsvc` publishes `FirstPromptAdded` once after a new conversation's
  first successful input. `sessiontitles` handles it only on the owning machine:
  Claude's adapter runs a tool-free, non-persisted Haiku helper, then a guarded
  rename updates the title without adding a turn or overwriting a manual name.
  Factory launches (`module.Launcher`) once the MCP listener is up, because
  its sessions verify their ocman MCP connection, and closes (`module.Closer`)
  after the HTTP and MCP listeners stop. Analytics (`internal/analytics`) owns
  the Stats/Usage endpoints, UI-usage recording, subscription usage, and the
  database-size sampler, stats gauges and LLM-turn scanner, all joined in its
  `Run`. It reads OpenCode only through `internal/db`; the analytics mirror,
  session snapshot, client-activity demand leases, `/api/system/stats` and the
  server's own OTel instruments stay in core.
- **Idle projects** (`internal/idleprojects`). An owner-local module holds the
  configurable idle timeout and minute sweep. It checks archive state, live
  sessions, queued messages, and active background work before calling
  `hostsvc.IdleProjectRuntime` on the provider adapter. Membership uses the
  launch resolver, including arbitrary linked worktrees and subdirectories.
  Deleted session directories use historical identity in the existing
  `claude-sessions.json` ownership snapshot; unmanaged retained transcripts
  cannot disable cleanup for unrelated projects.
  Only Claude supervisors are stopped. OpenCode v1 lacks atomic idle draining;
  v2 is machine-wide, so both are excluded. Claude fences new turns with
  `/drain-idle` before stopping its supervisor. Token-scoped rollback and
  recovery preserve replacement drains if cleanup is cancelled.
  Routine dispatch already ensures its runtime before creating or reusing a
  session, so future schedules do not keep idle projects running.
- **Snooze** (`internal/snooze`). Owns `POST /api/session/snooze` and a joined
  minute-interval worker that wakes expired conversations immediately on startup
  and during operation. Validation and SSE broadcasting are injected from core.
  Archive rows, snooze deadlines and auto-archive protection remain in
  `internal/state`, shared with manual archive and unarchive operations.
- **Project forge** (`internal/projectforge`). The PRs & Issues pane:
  `/api/project/{upstreams,prs,issues,pr-checks,pr-mergeability,forge-user,
  handle}` and `/api/settings/prompt-templates`. Every `/api/project/*` route
  takes an explicit owner, resolved by the server's `resolveOwner` (503 `remote_not_connected`
  for a disconnected remote); the owner's `hostsvc.Host` detects upstreams and
  fetches cross-fork heads, while the hub classifies remotes and calls the
  forge with its own clients (`ResolveForge`, `KnowsForgejo`, read at call
  time). The module keeps the bounded, collapsed upstream-detection cache and
  launches through the shared session path on the owner's compound platform.
  Prompt templates stay hub-wide settings, not owner-scoped. Project archive,
  quick settings stay in the server. Session start belongs to `internal/sessionlifecycle`.
- **Session lifecycle** (`internal/sessionlifecycle`). Owns prepare/start,
  progress broadcasts, first delivery, attachment uploads, direct mutations,
  reload and confirmed restart. Preparation reads catalogs without launching;
  creation validates the owner and backend before launch and applies permission
  defaults before publication or delivery. Host operations use the owning
  `hostsvc.Host`; shared mutations stay in `sessionsvc`. Direct send is an
  injected callback, shared with the queue. The server retains list/detail/SSE
  transport, auth, owner resolution, permission/question audit orchestration and
  queue/cooldown/idle ordering. Compatibility handler wrappers delegate to the
  module, whose routes are registered through `modules()`.
- **Session recovery** (`internal/sessionrecovery`). A workerless service owns
  interruption history, replacement membership, admission and stop fences, and
  confirmed-stop reconciliation. Runtime hooks and history reads call it through
  thin server adapters. Persistence stays in `internal/state`; callbacks resolve
  the current local host and platform. OpenCode v1/v2 replacement uses this
  service; Claude supervisor recovery stays in `platforms/claudecode`.
- **autoapprove.Judge.** The safe-command cache runs before backend selection.
  Each uncached permission selects a judge through `Deps.SelectJudge`, defaulting
  to OpenCode port discovery. When no OpenCode instance is reachable and
  `claudecode` is enabled, `autoapprove.ClaudeJudge` runs `claude -p --model
  haiku` with no tools. With neither, an explicit `needs-user` verdict leaves the
  prompt unanswered.
- **Session summary reads.** Pinned sessions outside the recent window use
  `platforms.SummaryReader`, backed by `db.GetSessionSummary`. Remotes
  return the owner-local row through `SessionSummary` and stamp its compound
  platform on the hub. Older owners fall back to session detail. Session-list
  SSE invalidations trail by 150 ms with a 500 ms maximum wait, finish existing
  reads before refreshing, and retain one follow-up when another event arrives
  during the refresh.
- **Factory usage.** `/api/factory/epics/{id}/usage` joins durable attempt
  identities to `platforms.UsageReader`. The local OpenCode adapter reads
  descendant message metadata and reuses token and pricing calculations.
  The Factory module deduplicates owner-qualified session IDs for Epic phase totals,
  retains retry history per Attempt, and flags unavailable reads as incomplete.
  Epic and Queue views poll these totals every ten seconds.
- **Owner-local session usage.** Both Factory and routine statistics read
  `platforms.UsageReader`. Remote adapters forward to `Usage.SessionUsage` over
  the existing token-authenticated connection, returning the owner's recorded
  and estimated costs keyed by session ID, including descendants. The additive
  service lives in `internal/remote/proto/usage.proto`; older owners report
  usage unsupported without falling back to hub-local sessions. Routine stats
  retain all-run counts and average duration and refresh billing at most every
  30 seconds while their tab is open.
- **Follow-up queue.** `internal/queuemod` owns delivery coordination over
  `internal/queuesvc`, which drains one held message per turn. Queueing remains
  an explicit composer gesture. Native backend inboxes are preferred only when
  no durable hub backlog exists; uncertain native sends are surfaced without
  replaying into the hub queue. Direct send and runtime recovery are injected
  server functions shared with the other session paths.
  Each decision uses `platforms.LifecycleReader` for settled status and the
  latest message identity. The local adapter reads one session and its newest
  message, without parts, tree or costs. Remotes settle on the owner and return
  that same bounded DTO through `SessionLifecycle`. Older owners without the
  RPC fall back to session detail. A guarded sweep reads lifecycle under the
  session lock and checks guard generation before reusing that read.
  Idle-edge flushes still trust the edge regardless of the status read.
  The server records model-provider cooldown through the module before admitting
  a flush and publishing `module.SessionIdle` to ordered bus subscribers. A
  continuation consumes that edge so held follow-ups wait for its turn to end.
  Retry resets and notices use compound platform/session identities. The module's
  `Run` stops admission on cancellation and joins accepted sends and the worker
  dispatcher, allowing durable dequeue receipts to finish before state.db closes.
  Owner-side cooldown reads run outside the admission mutex with a lifetime-bound
  deadline. Shutdown joins admitted idle handlers before closing the dispatcher;
  a cancelled read leaves its queued payload held for recovery.
  Sweep iteration and pre-send lifecycle/model reads stay cancellable and bounded.
  Only actual admitted delivery and durable dequeue/failure receipts detach
  cancellation, so a stalled owner read cannot hold shutdown open indefinitely.
  `sessionsvc.PrepareSendMessage` resolves the adapter, model and first-prompt
  metadata before returning a mutation-only delivery callback. Shared runtime
  recovery still reads through the preparation lifetime, including before retry.
  Project model settings and provider cooldown persistence retain their existing
  state tables; the combined project-defaults route invalidates the module cache.
- **platforms.SessionEventSink.** The registry installs status and idle callbacks
  on lifecycle-producing adapters, including adapters registered later. OpenCode's
  watcher feeds the same server callbacks. Adapters update their live view before
  calling the sink; idle means a completed turn. A `limited` session carries
  `resetsAt` in Unix milliseconds and stays non-idle for queue sweeps, routine
  completion and plugin replies. The status patch updates the browser's list and
  header without a new protocol or polling loop.
- **platforms/provider.** The generic adapter negotiates capabilities and protocol
  versions through `/hello`. Shared DTOs in `internal/providerprotocol` define the
  socket contract. Each adapter owns its session, live-status and pending-prompt
  caches; `WatchEvents` replays sequenced v1 events and feeds the shared status and
  idle sinks even without a browser. Runtime management owns the watch context.
- **providersupervisor / ocruntime.BackendRuntime.** A supervisor wraps a
  platform backend and serves it on a private unix socket. Claude browsing
  runs inside the ocman process, one per machine. Sessions ocman launches run
  in `ocman provider claudecode`, which `ocruntime.BackendRuntime` hosts in
  tmux, one per repository root, persisted in `managed_provider` and re-probed
  with `/hello` on recovery, so turns and prompts survive an ocman restart.
  Out-of-range versions stop new turns through `/drain` and are replaced only
  after all sessions settle. Each outbound event is appended and synced to its
  session spool before publication. The headless watcher acknowledges completed
  turns; browser snapshots pin replay cursors until their stream attaches, so
  truncation cannot erase the REST-to-SSE gap.
- **claudeprovider.** The `claudecode` platform's backend. The in-process
  browsing supervisor indexes `<CLAUDE_CONFIG_DIR or ~/.claude>/projects`
  into the disposable `claude-index.db` beside `state.db` (path, mtime and size
  keys; list projection, per-message byte offsets, subagent links), kept current
  by directory watches plus a 5 s mtime sweep. Detail pages read only their byte
  range through the single Claude to v1 converter; subagents are child sessions
  linked from their Task/Agent part. Sessions ocman launches are driven by pooled
  `claude` children in the project supervisors (pipes, cap 4 across the
  machine, 10 min idle, `--resume` on the next send)
  started with `--permission-prompt-tool stdio`: each `can_use_tool` becomes a v1
  `permission.asked`/`question.asked`, decided first by the adapter's ocman-side
  `provider.Rules` store, then by the autoapprove pipeline and the user. Their
  status comes from the process; terminal sessions use hook events when the
  plugin is installed, else the transcript tail settled by
  `db.SettleSessionStatus`.
- **internal/plugins.** External wire DTOs, bounded NDJSON, version negotiation,
  handshake and stream-order validation, executable discovery, and description
  validation. `internal/pluginsmod` mounts it: its `Start` scans once (right
  after auto-approve seeds the judge delay, before the other modules); `RescanPlugins` repeats the scan and records identities, checksums,
  and conflicts in state. Its `Run` drives the conversation reply pump and
  reconciliation; the server stops its processes after the listeners and
  launched modules close, before workers are joined. The module supervises
  one serve process per enabled local plugin from its private data directory, with
  bounded calls/output, cancellation, restart cutoff and process-group shutdown.
  Process health is durable; sensitive stderr stays bounded in local memory.
  Authenticated management routes expose catalog, health, grant approval and
  revocation, configuration validation/update, restart/retry, and data removal.
  Mutations and redacted stderr reads require localhost and safe browser origins.
  Configuration reaches the child on a private inherited descriptor before
  readiness; failed activation restores the last working public and secret values.
  Authenticated action list/invoke/download endpoints use the action broker. It
  filters context by each action's approved grants, requires host-issued confirmation
  tokens when declared, and returns only typed results. Grant changes serialize with
  dispatch and are checked again before results or artifact bytes are returned. See the
  [wire contract](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/internal/plugins/README.md).
  The remote `PluginOperation` RPC accepts a closed set of host operations, never
  raw stdio messages. Both transports use the same lifecycle and action broker.
  Catalog identities are `(ownerId, description.id)`; nested capabilities and
  instances inherit that owner. Management and artifact endpoints take `ownerId`.
  Action requests distinguish the installation `ownerId` from `context.ownerId`.
  Project/session listings combine the owner's owner-scoped actions with the hub's
  hub-scoped actions. Owner-scoped actions run on the owner; hub-scoped actions run
  only on the hub. Global placements always resolve to the hub. Explicit disconnected
  project owners fail closed. Remote secret updates pass through without hub
  persistence, and responses contain secret presence only.
  Configuration writes are capped at 1 MiB of canonical JSON; catalog responses
  are capped at 16 MiB on both transports.
- **sdk/plugin.** Optional public Go lifecycle and action helpers reuse the canonical
  protocol DTOs and validators. `sdk/plugin/conformance` runs black-box executable
  tests, including host-broker grant checks. `examples/ocman-plugin-fixture` is the
  deterministic executable used by the supervisor integration tests. Plugins in
  other languages can implement the same NDJSON contract directly.
- **Plugin persistence.** `internal/state` stores discovered identities, capability
  instances, enablement, grants, health and public configuration in schema v91.
  Schema v92 adds operation receipts committed before action dispatch, preventing
  replay after a host restart even when the original outcome is unknown.
  Secret snapshots use separate `0600` files beside the configured database;
  plugin-owned data uses its own directory. Configuration checkpoints restore
  public values and secret references together. Disable and removal retain data;
  permanent deletion is explicit. Catalog reads expose secret-presence flags only.
- **internal/factory.** The independent Software Factory boundary. It stores
   Epics, Mols, typed Issues, dependencies, attempts, Formula revisions,
   Plan revisions, approvals, and materialization provenance in `state.db`.
   YAML Formulas declare named steps with prompts, configuration, and `needs`.
   They compile to canonical JSON; historical TOML revisions remain readable.
   Poured steps keep their frozen definitions in `factory_workflow_step`.
   Implementation is a group whose required children must succeed before checks
   or later approvals can proceed. Verification and delivery expand per changed
   project; dependent steps wait for every project instance of each prerequisite.
   A Plan session is instructed not to
   modify files, but its configurable permission rules may allow shell commands
   for research; approval of an exact revision automatically materializes the
   proposed Implementation Issues and dependencies atomically. As defined by
   [ADR 0008](../adr/0008-coordinate-factory-work-across-projects.md), an Epic
   owns an admitted local project set, each executable Issue targets one project,
   and each project has an independent shared branch, checkpoint, and Delivery
   lineage. Ready Issues launch configured worktree sessions in their target
   project. Each implementation handoff records the clean, pushed commit in its
   Attempt result; the next Attempt for that project freezes the checkpoint and
   target branch in its policy. Workflow Project Deliveries follow the declared
   checks and approvals before publishing each project's PR. Legacy revisions
   create deliveries progressively as their required project work completes.
   Forge observations satisfy merge-gated cross-project dependencies; a recorded
   merge makes later work create a successor instead of refreshing the Delivery.
   Delivery retries preserve completed implementation Issues.
   Each Epic has one read-only control session that receives every new
   decision Factory needs. Its `factory_control` MCP tool only records proposals
    for planning, plan decisions, recovery, permission and project gates, graph
    mutation, pause, resume, delivery, and stop. The user's Confirm on the
    rendered card runs them over REST. Stop aborts workers before atomically
    cancelling unfinished work and closing the Epic, preserving partial files.
    Confirmed controls reconcile the action Inbox and retire stale alternatives.
    Existing control sessions receive new protocol instructions once on their
    next opening or Factory update. Implementation agents can also pause behind a
   durable project-admission gate; human REST approval adopts the local project,
   records execution acknowledgement, and launches an additive scoped Plan,
   while rejection resumes the same session with feedback. The browser uses REST
   while agents use MCP.
   Routines are not involved.
- **Factory persistence.** Native `factory_*` tables own the graph and its
   provenance in `state.db`; they do not reference or alter routine tables.
- **internal/factory/model.** Dependency-neutral persistence records shared by
  `internal/factory` and `internal/state`. Factory owns their meaning; state
  only stores them, which avoids making Factory depend on its SQLite adapter.
- **platforms.Registry.** The session-scoped seam. One adapter per platform;
  remotes register as compound-ID platforms so handlers can't tell local from
  remote. `platforms/identity` holds built-in IDs without depending on adapters
  or databases.
- **hostsvc.Router.** The directory-scoped seam (git, worktrees, tmux,
  projects and forge-repository detection/fetch). It resolves the owning host
  and delegates, the same transparency
  trick as the registry. Create requests carry a platform, defaulted from
  `platform.default` independently of the model. Routines and Factory Epics
  persist that choice. `EnsureRuntime(ctx, platform, dir)` selects the
  owner's runtime; adapters can implement `hostsvc.RuntimeEnsurer` for
  non-OpenCode runtimes. Optional `hostsvc.RuntimeAvailability` reports native
  launch prerequisites separately from browsing. Claude requires its resolved
  CLI and tmux; missing either leaves transcript browsing available.
  `EnsureRuntimeResult` carries an endpoint and an
  opaque `ocruntime.Instance`; `Port()` is empty for non-TCP endpoints.
  OpenCode worktrees still share one project instance, or the v2 machine
  server, with a per-session working directory. There is no process per
  worktree. The old `EnsureProjectOpencode` gRPC method remains an OpenCode
  alias for one release. Only tracked instances are reused; persisted entries
  must pass an API identity probe after restart. External instances are never
  adopted. `RestartProjectOpencode` stops and relaunches the tracked
  instance. Owner-local replacement callbacks use the read-only
  `ManagedRootReader.ManagedOpencodeRoot` view of that same main-checkout
  identity, so external linked worktrees share membership and nested independent
  repositories do not. This view neither launches a server nor requires a new RPC.
  `ReloadOpencode` requires a readable, matching server-default v2
  location directory before it calls the owning machine's v2
  `/api/location/reload` endpoint through the same Host/gRPC seam, retaining
  the process and running turns while refreshing configuration and the owner's
  cached catalogs. Pending permission and question prompts are cancelled.
  Terminal hook installation and removal target the local Host through the
  optional `HookPluginManager` seam; hook status events still reach the platform.
- **internal/composerattachments.** Owner-local attachment cache storage and
  seven-day cleanup. HTTP uploads stream through `Host.SaveComposerAttachment`;
  remote owners receive a metadata packet followed by bounded byte chunks over
  gRPC, avoiding the unary message limit. Only the owner writes the file, and
  its returned path is referenced in the prompt. Interrupted/oversize uploads
  remove their partial file; multipart bodies are not spooled on the hub.
- **internal/ocruntime.** The runtime abstraction behind the managed launch
  path. A `Runtime` interface (`Launch`/`Probe`/`Stop`) hides how a project's
  opencode is hosted; the native-tmux implementation runs headless
  `opencode serve --port N` on `tmux -L ocman-agents -f /dev/null`,
  isolated from the user's tmux server and configuration. V1 runs one server
  per project; v2 runs one per machine, kept up by `RunMachineSupervisor`.
  The runtime uses an ocman-allocated loopback port and
  probes authenticated `GET {endpoint}/config` for health. It is where the container runtime (epic
  #375) plugs in as a second implementation.
- **internal/ocv2.** OpenCode v2 compatibility. `ocapi.Auth.Transport` wraps
  every OpenCode HTTP client in `ocv2.Wrap`. On a machine whose `opencode` is v2,
  the wrapper answers v1 routes (sessions, messages, prompts, permissions,
  questions as v2 forms, catalogs, `/event` and `/global/event`) with v2
  `/api/*` calls. The SSE translator re-projects v2's typed events into v1
  `message.*`/`session.*` events. `ConvertMessage` is the single v2→v1 message
  converter: the HTTP layer, the event translator and `internal/db`'s TEMP
  views over `session_v2`/`session_message` all use it, so part ids agree
  everywhere. v1 servers are never probed.
- **platforms/opencode.** Wraps the read-only DB queries (`internal/db`) plus
  an HTTP client that attaches only to managed instances. The local Host
  supplies the tracked runtime inventory after API identity validation;
  external processes are never scanned or adopted. One process-wide `/global/event` stream
  per instance keeps pending permission and question state in memory across
  all session directories. The same live stream captures completed bash parts
  containing Git commit summaries and stores immutable observations in
  `state.db`; transcript reads never trigger capture.
- **internal/routines.** Validates and stores manual, timeout, one-time and
  cron routines. Manual and scheduled dispatch share the durable occurrence
  claim, launch a fresh managed session through `hostsvc.Router` and
  `sessionsvc`, and leave the run active until platform-neutral session status
  reports success or failure. Startup resumes observation of linked runs.
  Timeout schedules become an absolute due time when saved. Cron schedules
  use a five-field expression and IANA timezone. Webhook polling is an
  independent owner-local/remote delivery loop.
- **internal/notifications.** Authenticated, same-origin push subscription routes
  and joined observation/delivery workers. Slow sends do not stall snapshots;
  failed or stale owner reads preserve the baseline. It reuses the server's notification projection,
  identifies completions by the bounded platform lifecycle read, and persists
  VAPID keys, subscriptions and delivery receipts under its module schema.
  `webpush-go` handles encryption and signing. Known browser push-service hosts
  are allowlisted, redirects are refused, and transient sends have bounded retries.
  The page confirms server subscription state before suppressing local alerts.
  Shared notification snapshots and push payloads update the installed app's
  numeric attention badge. Notification routes carry an explicit owner and reveal
  the conversation even when a mobile overlay was already open.
  The owner's `ListPermissions` RPC annotates cloned prompts with its judge
  deferral decision, so the hub does not guess remote notification eligibility.
  See [Notifications](../features/notifications.md).
- **Legacy Workflow storage.** The `workflow_*` tables remain in `state.db` as
  inert historical data. No API, MCP tool, UI, or scheduler reads them. A DAG
  cannot be converted losslessly to one routine prompt, so recovery is a
  manual read-only SQLite export.
- **internal/mcp.** The MCP server exposes read-only session inspection,
  session creation and `embed_file`, plus the tools modules contribute:
  action-based `factory` and proposal-only `factory_control` (Factory),
  `inbox`, `webhooks`, `routines` and `artifacts`. File embedding uses signed tokens
  whose key is persisted in `state.db`; `internal/filesystem` mints and serves
  them.
- **Inbox.** The `inbox` MCP tool is deliberately limited to `help`, `send`, and
  `recall`. Sends write the owning host's `state.db`; remote sends and recalls
  cross the owner-routed gRPC seam. The browser alone lists, reads, and
  archives Inbox state through REST. Messages carry Permission, Factory, Routine,
  or Primary categories. The headless permission watcher creates owner-local
  actionable messages even when auto-approval is disabled. Successful replies,
  including TUI and automatic replies, archive them; a periodic reconciliation
  handles missed events and aborted requests. Permission replies use the existing
  session service with an explicit platform, including remote compound IDs.
  Routine completion writes its notification in the same transaction as the run
  outcome, and Factory deliveries explicitly use the Factory category.
  After dispatch, Factory reconciles outstanding human actions into durable,
  deduplicated Inbox items and archives resolved ones. The browser's shared
  Inbox query polls in the background for new Factory actions, displaying
  in-app toasts and opt-in system notifications without replaying its initial
  snapshot.
- **internal/opencodeskills.** Extracts binary-embedded ocman skills into
  XDG data and installs only ocman-owned symlinks for OpenCode discovery.
  Retirement unlinks only the exact verified symlink and preserves extracted data.
- **internal/state.** Ocman's writable SQLite store: migrations, settings,
  routines and their immutable run snapshots, legacy inert Workflow rows,
  and the independent native Factory Issue graph. `Open` runs two batches.
  The core sequence (`schema_version`, append-only, v1 onwards) covers every
  table whose queries live in this package, including the tables of today's
  feature modules. Each module's own tables use a `state.ModuleSchema` and the
  `module_schema_version (module, version)` ledger. The server lists these
  schemas in `ModuleSchemas()`. Each batch is transactional and fails closed.
  An existing database is backed up once per `Open` before anything changes:
  module migrations reuse the core batch's backup when it ran and take their
  own only when the core schema was already current. A fresh database gets
  none.
- **forge and integrations.** Forge-agnostic types in `internal/forge`, per-forge
  HTTP clients in `internal/forge/{github,forgejo}`. PR/Issue handlers obtain repository
  identity from the owner Host, then use the hub clients for metadata.
- **internal/previewauth.** Machine-level credentials for private link
  previews. ocman is a personal tool, so every token and grant belongs to the
  hub machine (one fixed viewer ID, `machine`, keyed by this instance's ID),
  usable by any request with app access (auth cookie, or a direct un-proxied
  loopback client when auth is off). Personal tokens are pasted in Settings
  (`POST /api/previews/token`), checked with the provider's identify call and
  stored without expiry; OAuth sign-in (Slack, Jira, or any provider with an
  app) uses a one-time state, PKCE when supported, one exact redirect URI
  (`<public base>/api/previews/oauth/callback`) and same-origin return paths.
  Client secrets stay server-side; tokens are AES-GCM sealed per
  owner/provider/workspace with the row key as associated data, refreshed
  under a per-grant singleflight, and deleted on `invalid_grant`, a 401 or
  removal.
- **Preview ownership across remotes.** The hub holds every credential and
  makes every provider call, also for a remote's sessions; nothing is routed
  to the remote. An explicit `remoteId` that is not connected fails closed
  with a bare 503 before any credential or cache is touched.
- **internal/linkpreview.** Normalized previews behind
  `POST /api/previews/resolve` (text in, `Preview` list out; TS mirror in
  `frontend/src/lib/previews.ts`). Only *configured* providers register a
  `Resolver`; resolvers recognize known direct URLs and custom link rules
  carrying a `provider` (the rule's replacement link stays the client-side
  fallback), deduped by resource and capped at 20 per text. Metadata fetches go
  through `linkpreview.API`: the resolver's fixed API hosts, validated path
  segments, the stored token, no redirects, 1 MiB bodies, 10 s timeouts.
  Results are cached per owner/provider/workspace/resource with in-flight
  dedup, 4 concurrent fetches, a 60/min per-grant budget and `Retry-After`
  backoff. With several workspaces for one provider the service tries each
  and returns the first that previews. `GET /api/previews/providers` is the
  browser's whole view: each supported provider, whether it is configured,
  its link hosts, account display names, and the rule patterns routed to a
  configured provider. `useProviderPreviews` only posts text with a link on
  a configured host or a routed-rule match, so unconfigured links never cost
  a request. Explicit markdown-link hover requests can also resolve public
  Open Graph metadata, as described below. Cards carry no connect flow; Settings → Link previews manages
  tokens and sign-ins, and `ocman:preview-auth-changed` reloads previews.
- **Open Graph hover previews.** Markdown links wait 500 ms on hover or keyboard
  focus, then resolve through the same endpoint with `openGraph: true`. A rich
  provider result wins; otherwise one public HTTP(S) page supplies `og:title`,
  `og:description`, `og:site_name` and `og:image`. This explicit fallback requires
  app access and shares the metadata cache, fetch budget and concurrency limit.
  Its credential-free client validates DNS addresses before dialing and on every
  redirect, rejects private networks and nonstandard ports, and bounds HTML to
  1 MiB. Images go through `/api/previews/image`, with the same address checks,
  a 2 MiB limit and raster-only responses. The browser never fetches the image
  host directly. Missing metadata leaves the original link alone.
- **Forge previews** (`linkpreview.Forge`, `internal/previews/forges.go`).
  GitHub, every https Forgejo host (tea logins, saved-token hosts and app
  hosts) and GitLab hosts (`gitlab.com`, saved-token and app hosts) resolve
  PR/issue/commit links. A saved token or sign-in wins; without one the
  resolver is a `linkpreview.Fallback` with the machine's env/CLI token
  (anonymous for GitLab), which previews private repositories for any
  request with app access (`WithOwnerAccess`) and public ones otherwise
  (repository visibility is checked before the resource is read).
  GitHub/Forgejo PR metadata includes `headSha`. A resolve request with
  `checksSha` accepts exactly one recognized PR URL and a validated SHA,
  then uses the same forge checks clients as the sidebar with the preview's
  selected credential and redirect-disabled HTTP client. These checks calls
  use the clients' 8 MiB response bound instead of `linkpreview.API`'s 1 MiB.
  The preview service caches checks for 15 seconds and retains its per-grant
  budget, concurrency limit and rate-limit backoff.
  Visible unfinished checks poll every 5 seconds with a cache bypass.
  Explicit refresh reloads PR metadata and bypasses the checks snapshot for
  its current head while retaining grant and
  rate controls. Checks HTTP 401 and ordinary 403 retain their status for token revocation
  and denial classification, and provider retry deadlines reach the backoff.
  Quota/secondary-limit 403 responses are classified as rate limits instead.
- **Slack previews** (`linkpreview.Slack`, opt-in via
  `OCMAN_SLACK_PREVIEW_CLIENT_ID/_SECRET`). A dedicated OAuth app yields a
  machine-wide *user* token (`user_scope`, bot tokens refused); conversation.v1
  bot tokens and grants are never used. `/archives/{channel}/p{ts}` links
  (plus `thread_ts` replies) resolve only when `auth.test`'s workspace URL
  matches the link host, and channels Slack reports private/DM/MPIM are
  denied before any message is read. Slack `ok:false` codes map onto the
  shared 401 (grant forgotten) / 403 / 404 / 429 handling.
- **Notion previews** (`linkpreview.Notion`, opt-in via
  `OCMAN_NOTION_PREVIEW_CLIENT_ID/_SECRET`). A public connection
  (`owner=user`, JSON token/revoke bodies with Basic auth) stores one grant
  per Notion workspace (or an internal integration token, identified via
  `GET /v1/users/me`); the shared pages are the whole permission model. Page links on notion.so / notion.com / app.notion.com /
  `*.notion.site` resolve by page ID to the API-returned URL; routed ticket
  identifiers run `POST /v1/search` and match titles by whole token. Several
  matches become `Choices` (state `ambiguous`), none is `not_found`. An
  unshared page is a 404 once the cache expires; a removed connection is a
  401 and forgets the grant.
- **Linear previews** (`linkpreview.Linear`, opt-in via
  `OCMAN_LINEAR_PREVIEW_CLIENT_ID`). Authorization code + PKCE with the
  `read` scope; the rotating refresh token is stored per Linear organization; a
  personal API key (`lin_api_…`, sent without `Bearer`) works without an app. One GraphQL query returns the organization and the issue;
  a link whose workspace `urlKey` differs from the grant's is `not_found`.
  GraphQL error codes (`AUTHENTICATION_ERROR`, `FORBIDDEN`, `RATELIMITED`,
  often on HTTP 400) map onto the shared 401 / 403 / 429 handling.
- **Jira previews** (`linkpreview.Jira`, opt-in via
  `OCMAN_JIRA_PREVIEW_CLIENT_ID`). Atlassian 3LO authorization code with
  `read:jira-work read:me offline_access`; one grant per
  Atlassian account (one rotating refresh token spans its sites). Each fetch
  lists `oauth/token/accessible-resources` and calls
  `api.atlassian.com/ex/jira/{cloudid}` for the site matching the link host,
  which is never contacted itself. An identifier found on several sites
  becomes `Choices`.
- **GitLab previews** (`linkpreview.GitLab`; `gitlab.com` by default, other
  hosts once they have a token or an `OCMAN_GITLAB_PREVIEW_APPS` app). One provider `gitlab:<host>` per instance
  (authorization code + PKCE, `read_api`), so grants never cross instances.
  Project paths are sent as one `%2F`-escaped segment
  (`API.WithEncodedSlashes`). Public projects preview anonymously after a
  visibility check; every call dials through a resolve-once, address-checked
  dialer (no loopback/link-local; no private ranges for gitlab.com).

## 3. Session and event data flow

See [Session and event data flow](../architecture-events/) for the sequence
diagram covering session reads, SSE activity updates, remote streams, and
routine dispatch. Sidebar activity updates arrive over global SSE without
changing row order. Terminal status events fetch the owner-qualified session's
durable completion timestamp, which ranks rows newest first.
Session list/detail responses carry `factoryAttemptId` from a single state.db
attempt lookup. The sidebar filters these tagged rows and their descendants
without requesting `/api/factory/epics`. Only tagged conversations mount Factory
recovery queries. On mobile, closed drawers disable sidebar list/git/stats reads
and unmount the closed mobile details drawer. Document visibility pauses reads
without unmounting open panes or recovery controls, preserving fullscreen diffs,
pagination, expanded previews and unsent guidance. Resuming refreshes the visible
content, including the artifact pages already opened by the user.
Conversation archive and keyboard navigation load their candidates on demand
when the mobile sidebar is closed, without restarting sidebar polling. Archive
changes broadcast to every client and close the affected open conversation;
browser reloads preserve the archive instead of treating it as a reopen. Artifact
pagination and resume refreshes share a serialized read chain.
Settled agent bash commands also produce `ocman.git.command` refresh hints for
the owning project's PR/Issue pane.

## 4. Frontend composition

```mermaid
flowchart TD
    Shell[App routes, MainNav, routeTitle, Settings] -->|iterate| Features[features/<br/>static registry]
    Features --> Pages
    Shell --> Pages
    Pages[pages/<br/>routes] --> Comp[components/<br/>shared controls + feature UI]
    Pages --> Stores[Client state<br/>TanStack Query + Zustand]
    Stores --> Persistence[Browser persistence<br/>IndexedDB drafts, text + receipts]
    Comp -->|PR rows + conversation previews share repository/SHA checks cache| Stores
    Comp -->|plugin Settings + palette actions: explicit ownerId| API
    Comp -->|delayed markdown hover: provider or Open Graph card, proxied image| API
    Comp -->|first execution: resolve workspace, then dispatch on same owner| API
    Stores -->|visible pane reads; session rows carry Factory tags| API[lib/ API client]
    Stores --> SSE[SSE subscription]
    Pages --> Scopes[Ref-counted activity scopes]
    Comp --> Scopes
    Scopes --> Reporter[Client activity lease reporter]
    Reporter --> API
    API -->|/api| Hub[ocman backend]
    SSE -->|events| Hub
    Hub -->|ocman.settings.changed: clear settings cache| Stores
    Comp --> Caps[useCapabilities<br/>capability gating]
    Comp --> Speech[Browser speech synthesis<br/>local or online voice]
```

- **Feature registry.** `src/features/index.tsx` is a static list in which
  Factory, routines and artifacts contribute their routes, with layout tier and
  header titles, plus their main-nav link. Plugins, auto-approve, sharing and
  webhooks contribute Settings groups, each mounted either always or only while
  active. `App`'s routes, `MainNav`, `routeTitle` and `SettingsTab` iterate it at
  fixed slots between their core entries. Everything is still imported eagerly,
  with no runtime discovery. Backend plugins remain separate and reach the UI
  only through plugin panes and palette actions. The command palette and the
  session right-panel tabs still keep their own lists.
- **Shared controls.** `Control` provides buttons, button groups, text inputs,
  textareas, search inputs, and selects with native form semantics and shared
  focus, disabled, and invalid styles. `IconButton`, `RefreshButton`, and
  `CopyButton` add action feedback; copying is confirmed only after success.
  `Pagination` shares previous/next controls while callers own indexing, page
  boundaries, totals, and requests.
- **New-conversation settings.** Settings → Sessions → Default agent starts at
  `build` and uses the shared searchable agent picker. Options come from agent-only
  reads of running local instances, scoped to their discovered directories and
  known local session directories. This includes project-defined agents on v2's
  single machine server without launching an instance. Hidden helpers and
  subagent-only agents are excluded. With no reachable instance, `build`, `plan`,
  and the saved preference remain selectable; remote-only catalogs are not read.
  Catalog discovery uses up to eight concurrent waiters within a five-second overall
  deadline, with a four-second per-target wait, and stops scheduling directories on
  cancellation. A stalled target
  does not block healthy catalogs or fallback choices. Shared port scans have an
  independent ten-second deadline;
  canceling a caller only cancels its wait, preserving other clients' live status.
  Shared agent fetches acquire one of eight global slots inside the singleflight
  leader and hold it through the HTTP response. Their two-second fetch deadline
  is independent of callers; slot admission waits at most ten seconds. Leaving
  Settings cannot cancel a composer's joined catalog read. Direct catalog callers
  also cancel only their own singleflight wait, without waiting for admission or
  the shared fetch to finish.
  A failed scan preserves the last successful snapshot for liveness readers and
  the auto-approval watcher for at most one minute without renewing the cache TTL.
  Sustained failure then expires old membership so stopped sessions lose stale
  busy status. A successful empty scan still removes disappeared instances.
  An explicit composer selection wins.
  `projectSettingsCache` shares the project settings fetch across conversations
  and sibling worktrees, keyed by owner and project root. Successful global or
  project setting saves broadcast `ocman.settings.changed`, which clears every
  cached project setting and refreshes mounted composers. Reconnecting SSE also
  clears the cache to recover missed saves; in-flight reads cannot restore stale
  settings after invalidation. The preference is stored in the hub's `state.db`.
  The header's project button opens `ProjectQuickSettings`, which reads the
  directory catalog without launching an instance and saves model, agent, and
  worktree defaults through `/api/project/settings`. These defaults use a
  separate owner-and-project-root setting key, preserving the ordered model
  fallback list. New conversations apply them after preparation; explicit
  composer selections win, including selections made while preparation waits.
- **Selection and overlays.** `SegmentedControl` uses native radios for filters.
  `Popover` shares the usage popover's compact, square-cornered shell with
  project quick settings. It portals to the document body, focuses the panel,
  handles outside-click and Escape dismissal, and restores trigger focus on
  Escape. Each caller positions the shell beside its trigger.
  `Tabs` and `DropdownMenu` wrap Radix UI for keyboard navigation, focus, and
  accessible associations, styled with the app's CSS. Tabs activate on click,
  Enter, or Space and unmount inactive content by default. Menu actions that open
  dialogs wait for the menu to restore focus so the dialog records a stable
  opener. `ModalHeader` and `ModalFooter` compose the existing `Modal`,
  `IconButton`, and `ButtonGroup`; `Modal` retains dismissal and focus handling.
- **Loading and errors.** `LoadingState` pairs a decorative `Spinner` with one
  status announcement and respects reduced-motion preferences. Structured
  skeletons remain separate. `InlineAlert` announces errors and can show a
  busy-aware retry button; `EmptyState` renders empty messages. Callers own
  loading, stale-data, error, and empty-state decisions.
- **Capability gating.** The UI never branches on platform identity. Features
  toggle via `/api/capabilities`, enforced by a lint script.
- **PR checks.** `lib/usePRChecks` polls only visible rows/cards until every
  check settles, or three empty reads spaced 30s apart confirm no CI. Errors
  back off from 5s to 60s across viewport changes. Sidebar polls respect forge
  rate-limit deadlines; cards rely on resolver-side upstream backoff. Hidden
  documents pause polling. Both views use `lib/prChecksCache`'s
  bounded localStorage cache, keyed by host/repository/SHA. Sidebar refresh also restarts checks
  on mounted conversation cards. Preview checks use the existing preview
  resolver endpoint so the conversation owner and preview credentials remain
  authoritative, including PR links outside the active project's repository.
- **New session target.** A new conversation is a client-only route,
  `/session/new?dir=…&remoteId=…&platform=…&draftId=…`, until its first prompt: no
  session, worktree or placeholder exists before that. `NewConversation`
  calls `POST /api/sessions/prepare`, which reads the directory's agent, command and model
  catalogs (`Platform.DirectoryCatalog`, routed over gRPC for remotes), and
  `useWorktreeEligibility` reads the git-info branch, the owner's worktree
  list, and its verified default base ref to decide whether "New worktree"
  is offered. A repository without a usable base commit uses the current
  checkout, and automatic creation rejects it before launching OpenCode.
  Named bases use fully qualified refs to avoid branch/tag name collisions.
  Git probe failures propagate to the eligibility retry controls.
  Factory removes the exact namespace when recording a forge target branch.
  A detached default uses verified local `main` or `master` for delivery,
  otherwise preparation requires an explicit named target. Legacy recovery
  uses the same preparation path.
  The machine selector
  re-points the route; the target selector changes client state. Neither
  creates a session. All browser-local draft state lives in one IndexedDB
  database, `ocman.drafts.v1` (`lib/draftDb`), with three stores: composer
  text per session or draft id (`lib/composerDraft`), prepared-conversation
  metadata (owner, directory, target, selections; `lib/newConversationDrafts`),
  and start/first-delivery receipts (`lib/draftStartClaims`). Every lifecycle
  step is one readwrite transaction over the stores it touches. IndexedDB runs
  overlapping readwrite transactions one at a time in creation order, so each
  check and its write are atomic across tabs, and an aborted transaction leaves
  nothing half-applied. Each tab keeps a synchronous in-memory snapshot (a Map for
  text, Zustand for drafts and receipts), hydrated in one transaction before the
  app renders. After a commit, BroadcastChannel tells other tabs which keys
  changed and they re-read them; a per-key local edit counter stops a database
  read from overwriting a newer local edit. Hydration imports the pre-IndexedDB
  `ocman.composerDrafts.v1` localStorage map once without overwriting stored text.
  Failed autosaves retain the unsaved live text across refreshes and report the
  failure in the composer; another edit retries persistence. Synchronizing an
  untouched composer updates presentation without dispatching input or saving.
  Without IndexedDB, drafts live in memory only and starts fail closed.
  Text records carry a revision that only grows. An autosave writes only if the
  stored revision is not newer, so a stale autosave cannot land after a discard
  in another tab. Clearing sent text clears only the exact text and revision this
  tab saw. Discard increments the revision, and emptying the composer applies it
  at the input gesture. A composer that is still empty shows text restored later,
  such as a failed start recovered by another tab; a user edit always wins.
  `SidebarConversationDrafts` lists these prepared
  conversations in both sidebar views, including empty drafts, and lets the user
  reopen or discard one. Opening another new conversation allocates another
  draft id; switching machines retains the current id.
  The first submission claims the draft in one transaction: a pending or
  completed receipt refuses a second claim in any tab, and a committed metadata
  tombstone refuses a start on a discarded identity. The pending receipt holds
  the prompt so reopened composers show it; terminal receipts drop it. A failure
  this tab knows but could not store is repaired inside the next claim.
  Completion is one transaction. It records the created session and retires the
  draft when its owner, route, revision, text and selections still match what the
  initiating composer submitted. Otherwise it moves the newer edits (text, owner,
  target or selections) to a fresh identity. The submitted selections are captured
  from that composer, so peer changes made before submission are kept. The retired
  identity becomes a tombstone, so a late metadata write cannot resurrect it.
  If that transaction fails (for example at quota), a receipt-only write records
  the session with a relocation error. The draft stays untouched and a retry
  re-runs the same idempotent transaction. If even that write fails, the outcome
  stays visible in this tab and reconciliation repairs it.
  A failure is recorded in one transaction that restores the prompt only when no
  newer edit or discard happened. A created session is never downgraded to a failure.
  Sidebar discard waits for its transaction to commit before navigating or
  releasing live attachments; failure keeps the draft and exposes a retry.
  Identity-less bookmarked URLs get a fresh canonical draft id before mounting,
  with a transactional move of the legacy `new` text.
  Pending images and browser Files live in a shared draft-keyed Zustand snapshot,
  restored after navigation and a failed start. Mounted composers read it directly,
  including outstanding processing counts, so a remount cannot unlock submission
  before accepted files are ready. Attachment cancellation has its own identity,
  separate from text revisions. A completion or peer retirement transfers the
  snapshot and its pending batch owner to the replacement before clearing the
  retired identity. Catalog defaults are derived for display and submission, not saved as user edits.
  `PreparedDraftLifecycle` follows completed starts to their session or
  replacement and replaces an externally discarded identity. Its navigation
  guard remounts per draft identity. Lifecycle redirects replace history
  entries, so browser Back reaches the page before the retired draft.
  The first submission calls
  `POST /api/sessions/start`, which creates the session at the chosen target
  (an automatically named `session-<suffix>` worktree, or the current
  checkout), delivers a plain prompt server-side in the same request, and
  returns the real id; the client then navigates once. Until then the prompt
  renders as the conversation's first message with the server-reported start
  steps beneath it (`ocman.session.start.progress`, see the event flow); a
  failure removes only that request's prompt. Custom slash commands
  and shell submissions create the session the same way and run on it from
  the client; ocman built-ins need an existing conversation. A failed first
  send retains text, images, selections and error in the existing failed-send
  recovery path. Complete failed payloads stay in shared memory and notify
  mounted session views; only the best-effort localStorage copy is size-capped.
  Start locks are scoped to the draft generation so an older request cannot
  discard or unlock a newer draft's submission. Non-image files are retained
  until creation, uploaded to
  the real session, then sent; file/command/shell execution lives in
  child-keyed retry state, independently of the child's draft. First-delivery
  lifecycle is owned by `firstSubmission`: an atomic reservation in the shared
  draft database's receipt store must
  succeed before upload/command execution or completion publication. Child composers
  remain locked until they read that record. BroadcastChannel carries notification
  hints, never authority; a missed notification cannot unlock peers.
  Known live terminal outcomes override stale pending records and reconciliation
  repairs their persistence. Retry execution remains in the originating tab, which
  repairs its known failure before claiming another attempt and retains the
  execution by attempt identity when a reservation is refused. Every write adopts
  the authoritative transaction result, fenced against newer live attempts.
  Equivalent durable terminal values are adopted without another write or
  notification, so IndexedDB's cloned objects cannot cause cross-tab ping-pong.
  Receipt-read failures preserve the current payload/execution and cannot expose
  release controls for an active delivery. Rejections are fenced against newer
  lifecycle and view state.
  The originating tab
  owns the browser Files or command closure. An owner-presence probe detects a
  closed/reloaded tab. The unknown outcome remains blocked until the user explicitly
  releases the first-delivery lock; neither reload nor recovery resends the payload.
  Uncertain
  creation is never automatically replayed, and a late completion cannot
  override a newer route or clear its draft. Prepare/start preserve the
  localhost host-control guard. Afterwards the host has the
  `title` agent (its `small_model` or Haiku, in a temporary tool-denied
  session) name the delimited task under branch-naming instructions and
  renames the branch to that slug in place; the session title is left to
  OpenCode's own first-message titling.
  Naming failures keep `session-<suffix>`. Non-repository directories and
  directories already inside a linked worktree use the current checkout.
  Existing linked worktrees are also selectable. New conversations started
  from a worktree session open on the owner's main checkout, so every target
  stays available.
  Composer layout, dialogs, and props live in separate modules.
- **Read aloud.** Turn-end controls select original final-answer text parts and
  use browser speech synthesis. Opt-in autoplay waits for the idle reconciliation
  in the focused session tab. Voice preferences stay in browser storage; audio
  does not pass through the ocman backend.
- **Plugin Settings.** Settings uses shared setting rows and each host's
  `pluginManagement` capability. The hub probes remote plugin support with a
  bounded `available` RPC. Catalog, lifecycle, configuration, and stderr calls
  carry the selected `ownerId`; switching owners clears the previous view.
  Enabling requires checksum and grant review. Secret inputs are write-only,
  and failed configuration activation reloads persisted configuration and health.
- **Client state.** Shared Zustand stores hold broad session state. The
  session detail mirrors fresh REST/SSE status into the owner-qualified sidebar
  row, so navigation keeps the same status. Cached metadata stays unconfirmed after
  a failed refresh until a successful read or status-bearing live event arrives.
  List reconciliation fences against owner-qualified status-update revisions,
  including transitions returning to the starting status; subsequent fresh reads remain authoritative.
  The Routines page polls definitions (each with its latest run) and webhook
  inboxes over REST, pages history only for the open drawer, and keeps its form
  and selected edits locally.
- **Plugin actions.** The command palette merges `action.v1` contributions for
  global, project, and session contexts. It sends opaque IDs and core route names
  through the authenticated backend, resolving older contexts independently of
  the recent-session search cache. Host dialogs render confirmation text and
  typed results, including owner-routed artifact downloads. Confirmation reuses
  the operation ID; failures and timeouts never trigger automatic invocation retries.
  Plugins supply no browser JavaScript or HTML.
- **Activity leases.** Mounted data subscriptions ref-count their scopes. One
  authenticated reporter renews the visible tab's lease, allowing the backend
  to skip view-serving session, project and metrics refreshes
  when every tab is hidden or gone. Scheduled routines and auto-approve do not
  consult these leases.
- **Plugin panes.** The right panel lists the active owner's enabled `pane.v1`
  declarations through `/api/plugins/panes`. Catalog reads never invoke the
  plugin. An open pane mounts its data query, calling `/api/plugins/panes/read`
  through the owner-routed `PluginOperation` RPC and the owner's grant checks.
  Closing it unmounts the query and cancels an outstanding read. Core renders
  bounded tree nodes as plain text. The optional Beads plugin owns all `bd`
  execution and parsing; no Beads operation remains on `hostsvc.Host`.
- **Factory.** `/factory` presents actionable approval Gates, Epics, Issues,
   Queue, and Configuration through TanStack Query. Browser mutations create
   native Epics, pour Mols, decide exact Plan revisions, and explicitly close
   completed containers. Each Epic links to its control session, where
   approved changes return through the scoped `factory_control` tool. The dispatcher records
   attempts before launching the prompt-constrained planning or configured
   implementation session.
   Factory MCP results can carry `[[ocman:card ...]]` markers. The tool-call
   renderer (and the markdown renderer, for markers in assistant text) turns
   them into creation or human-action cards using
   the same TanStack Query state and REST mutations as the Factory pages.
   Resolved actions disappear on refresh; rendering a marker never executes it.

## 5. Plugin action and remote projection flow

```mermaid
sequenceDiagram
    participant B as Browser palette / Settings / sidebar
    participant H as Hub HTTP handlers
    participant O as Installation owner
    participant A as internal/plugins action broker
    participant D as Owner state.db
    participant P as External plugin process

    B->>H: Catalog / management with ownerId
    H->>O: Local call or authenticated PluginOperation RPC
    O-->>B: Via hub: owner-qualified catalog, safe health, secret presence
    B->>H: List actions for placement and context owner
    H->>O: Owner-scoped actions; merge hub-scoped actions
    H-->>B: Typed action declarations
    B->>H: Invoke with installation ownerId and stable operationId
    H->>O: Route to installation owner
    O->>A: Validate enablement, scope, grants, confirmation
    opt Confirmation required
        A-->>B: Via owner and hub: confirmation text and bound token
        B->>H: Same invocation and operationId plus confirmed token
        H->>O: Route confirmed invocation
        O->>A: Revalidate and admit
    end
    A->>D: Commit operation receipt before dispatch
    A->>P: NDJSON action.v1 invoke with minimized context
    P-->>A: One typed terminal result
    A-->>B: Via owner and hub: authorized results / artifact handles
    B->>H: Download artifact with installation ownerId
    H->>O: Recheck grants and read owner-local artifact
    O-->>B: Via hub: attachment bytes
    B->>H: List enabled pane declarations with ownerId
    H->>O: Read approved pane metadata, no plugin invocation
    O-->>B: Via hub: owner-qualified pane declarations
    opt Sidebar pane open
        B->>H: Read pane with ownerId and directory
        H->>O: Route read, verify enablement and pane.project grant
        O->>P: NDJSON pane.v1 read
        P-->>O: Typed tree, no markup
        O-->>B: Via hub: validated and reauthorized tree
    end
```

- The installation owner supervises the process and owns its configuration,
  secret files, operation receipts, and in-memory results. The hub projects
  remote operations through a closed RPC, never raw plugin stdio.
- The browser can target a hub installation while carrying a remote project's
  context. These are separate owner identities; disconnected explicit project
  owners fail closed. Global placements resolve to the hub.
- Confirmation and result rendering belong to ocman. Calls are not replayed after
  crashes, and durable receipts prevent uncertain side effects from being repeated
  after host restart. Downloads recheck the installation's current grants.
- Only `action.v1` is shipped. Conversation-provider, platform-provider, iframe UI,
  relay inbox capability, registry/updates, signatures, sandboxing, Slack, and
  Codex remain future work. Existing core Inbox and webhook services do not imply
  a plugin capability for either.

See [Plugins](../features/plugins.md) for the operator and authoring guide.
