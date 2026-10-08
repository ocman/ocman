# AGENTS.md

## What is ocman

A web dashboard for viewing coding-agent session data. Ocman supports:

- **OpenCode** — reads OpenCode's SQLite database (read-only) and
  proxies live data from running OpenCode instances via their HTTP
  API.

OpenCode **v1 and v2** are both supported (`internal/ocv2`). The installed
version is read once from `opencode --version` (`ocv2.InstalledV2`; a later
change only logs "restart ocman"), so a hub on v1 and a remote on v2 work side by side; each ocman only
speaks to its own machine's OpenCode. For v2, the rest of ocman still talks v1:
`ocapi.Auth.Transport` wraps every OpenCode client in `ocv2.Wrap`, which
answers the v1 routes and event streams with v2 `/api/*` calls and translates
the shapes. `ocv2.ConvertMessage` is the only v2→v1 message converter. The HTTP
layer, the SSE translator and the database's TEMP views (`internal/db/v2views.go`:
`session`/`message`/`part` over `session_v2`/`session_message`) all go through it,
so part ids (`prt<msg suffix><index>`) agree everywhere. Never add a second
converter. On v2, `EnsureProjectOpencode` runs **one `opencode serve` per machine**
(working directory `~/.local/share/ocman/opencode-v2[-<db hash>]`, keyed by the
effective `OPENCODE_DB` so a server writing another database is never reused). Discovery skips `lsof` and
resolves every directory to that port (`opencode.SetMachineServer`). The server's
password is generated once and stored in `state.db` when none is configured,
because v2 always requires one. Ctrl/Cmd+Enter follow-ups use v2's native session
inbox (`platforms.NativeQueue`, `delivery: "queue"`) unless ocman's own queue
already holds messages for that session. Tests pin the version with
`ocv2.SetInstalledV2` (a `TestMain` per package) and never run the real binary.

Platforms are wired through a common `Platform` adapter interface
(`internal/platforms/`). Adding a new platform (e.g. Codex) is a
new adapter + registry entry; see
`spec/multi-agent-support/architecture.md` for the design.

Project quick settings persist startup defaults under an owner + folded project
root in the hub's `state.db`. The default permission mode selects inherited
permissions, Plan only, Auto-accept edits, or YOLO; saving YOLO requires browser
confirmation. `/api/sessions/start` reads it after resolving the owner and passes
the rules to `sessionsvc.CreateConfigured` or `Host.CreateWorktreeSession`, so
permissions are applied before publication and the first prompt. Existing
sessions keep their current rules.

A new conversation is a client-only route (`/session/new?dir=…&remoteId=…
&platform=…`) until its first prompt: no OpenCode session, worktree or
placeholder exists before that, so the machine and target can still change
freely. Opening it calls `POST /api/sessions/prepare`, which returns the
directory's agent, command and model catalogs via `Platform.DirectoryCatalog`
(a `PlatformJsonReq` RPC for remotes). It never launches OpenCode, so opening
the composer or picking a machine starts nothing: the catalogs come from an
instance that is already running, else the historical models, and the first
submission (`/start`) launches the instance. Until it returns, the prompt renders as the first message
with the server's steps beneath it (OpenCode, worktree, session, prompt; parallel
ones together), sent as `ocman.session.start.progress` SSE events tagged with the
request's `startId` and reported through `hostsvc.WithProgress` on the context
(which does not cross gRPC, so the hub reports a remote's worktree start around the call). Submission waits for that catalog, with visible retry on a
prepare failure; `useWorktreeEligibility` reads the git-info branch, the
owner's worktree list, and its verified default base ref. A directory already
inside a linked worktree, a non-repository, or a repository without a usable
base commit uses the current checkout. Base resolution keeps a remote-tracking
default when its local branch is absent, then falls back to the current branch
or detached HEAD; it never assumes `main` exists. Named bases use fully qualified
refs to distinguish local branches from remote-tracking branches and tags.
Git probe failures propagate as errors so eligibility remains retryable rather
than treating them as missing commits. Factory requires a named delivery target:
a detached default falls back to verified local `main` or `master`, otherwise
preparation asks for an explicit target. Legacy recovery uses the same preparation.
The composer's machine selector only
re-points the route; each draft has its own `draftId` query parameter (`draftKey`
prop) and survives. Browser-local prepared conversations appear in the sidebar's
Drafts section before any session exists. All draft state (composer text,
prepared-conversation metadata, start and first-delivery receipts) lives in one
IndexedDB database (`lib/draftDb`, `ocman.drafts.v1`). Every lifecycle step
(claim, failure, completion with retire-or-relocate, discard) is one readwrite
transaction, so tabs never interleave partial changes. `lib/composerDraft` and
`lib/newConversationDrafts` keep synchronous in-memory snapshots, hydrated
before the app renders (`main.tsx`); BroadcastChannel tells other tabs which keys to
re-read. Text revisions only grow and fence stale autosaves. Never add a second
storage path for draft state. Attachment snapshots use Zustand so a
composer reopened before an image read completes sees its eventual result.
The first submission calls `POST /api/sessions/start`
`{directory, remoteId, platform, worktree, title, prompt, send}`: an explicit
owner is authoritative, an omitted platform is derived from it, and a mismatch
is rejected before launch. A worktree target
creates `session-<id8>` on the owner (`CreateWorktreeSession`, no LLM call on
the first message's path) and a current-checkout target creates in the
directory on the project's instance; a plain prompt is delivered server-side
in the same request (`firstMessageSent` / `firstMessageError`), custom slash
commands and shell submissions are run by the client on the returned session,
and ocman built-ins need an existing conversation. A failed first send keeps
the complete payload (text, images and selections) in a shared in-memory
`failedSends` mirror for the real session's retry controls; the localStorage
copy remains capped and best-effort, so quota errors cannot erase live recovery.
Non-image files stay as browser Files until
creation, then upload before the first send. Upload requests carry the session's
compound platform and stream bytes through `Host.SaveComposerAttachment` to
the owning machine's `internal/composerattachments` cache; remote writes use a
client-streaming gRPC RPC and return an owner-local path. Upload/delivery failures and
command/shell failures live in child-keyed `firstSubmission` state with a
visible retry, without overwriting the child's draft. While first delivery is
pending, the child composer blocks further submissions so follow-ups cannot
overtake it; permission and question controls remain available. Start transport failures
are never automatically replayed. Completion retires only the unchanged
submitted draft revision, including background starts, and only the currently
open initiating draft navigates to the created session. The atomic claim
prevents duplicate first submissions across tabs and reloads. Tests get a fresh
fake IndexedDB per case (`vitest.setup.ts`).
The composer and its machine/target controls stay locked while a start is pending.
The owner then
names the worktree in the background: OpenCode's `title` agent (its `small_model` or Haiku) names the
delimited task under branch-naming instructions and the title is slugged into the branch (`git branch -m`, the path
stays). The session keeps OpenCode's default title so OpenCode titles it from
the first message; the branch name is never used as the title. A naming
failure keeps the provisional name. The naming session is titled
`(worktree-name subagent)` so the session list hides it. `/wt` from an existing
session still uses `create-and-launch` with `parentSessionId` for permission
inheritance; a brand-new conversation has no parent to inherit from.

Ocman also supports **on-demand OpenCode worktree sessions** via the
`/wt` command in the command palette and the per-project Worktrees
view (`/project/<dir>/worktrees`). The feature shells out to
`git worktree add` under `<repo-parent>/.worktrees/<repo>/<slug>/`, then
runs the session in-app: ocman keeps **one** `opencode` instance per
project (rooted at the main checkout, ensured via
`EnsureProjectOpencode`) and creates the worktree (and same-directory)
sessions on that instance with a per-session working directory. There is
no per-worktree tmux window; a single project instance serves every
worktree, so parallel sessions still get isolated files, rebuilds, and
staging area without spawning an opencode/tmux process each.

The managed instance lifecycle is **runtime-neutral** (#376,
`internal/ocruntime`): an `ocruntime.Runtime` interface
(`Launch`/`Probe`/`Stop`) abstracts how the instance is hosted, with a
native-tmux implementation today and a container runtime planned (epic
#375). `EnsureProjectOpencode` (on `hostsvc.Host`, owner-routed via
`Router.ForDir`) is the only path that launches opencode for a project.
It **allocates a free loopback port itself**
(`ocruntime.AllocateLoopbackPort`, bind→close) and launches
`opencode --port N` — the managed path no longer relies on `lsof` port
discovery. Health is an **API probe**: `GET {endpoint}/config` returning
200, not mere listener presence. Concurrent ensure calls for one repo
root collapse via `singleflight` (at-most-one launch); the call probes
the current instance and reuses it when healthy, else relaunches.
`EnsureProjectOpencodeResult` exposes the full `Endpoint` URL (plus a
`Port()` accessor), the resolved `RepoRoot`, the opaque
`ocruntime.Instance` (whose `ID` is the tmux session name, kept for
observability), and `Launched`. Each managed instance is persisted in
`state.db`'s `managed_opencode` table (migration v35) keyed by canonical
repo root, so it survives an ocman restart; on recovery a persisted row
is **always re-probed** before being trusted — a dead row is discarded
and the project relaunches. `RestartProjectOpencode` (same owner
routing, works local + remote over the gRPC seam) stops the tracked
instance and relaunches under the same `singleflight` key.

Permission approval provenance is durable: AI and user approvals, including
one-time user approvals, are recorded for command footnotes with the original
permission metadata. Only user `always` decisions and judge-safe approvals feed
child permission inheritance; user `once` decisions remain audit-only.

When `EnsureProjectOpencode` launches the instance it seeds the pane with
a scoped `external_directory` `OPENCODE_PERMISSION` rule for the
project's `.worktrees/<repo>` root so worktree paths are pre-approved.
**Limitation:** an opencode instance that was *already running* before
the /wt launch was not seeded with that rule; the runtime autoapprove
pipeline covers the gap for those pre-existing instances.

A worktree session launched from a parent also **inherits the parent's
accumulated "Allow always" permissions AND its live permission posture**
at split time (#101): ocman records every user-clicked "Allow always"
reply (plus judge-safe approvals) in `auto_approved_permission`, and
`permissions.BuildInheritedRulesWithLive` replays them into the child's
per-session ruleset via `LaunchRequest.PermissionRules` /
`SetPermissionRules`. Crucially it *also* reads the parent's current
ruleset (`Platform.PermissionRules`) and merges it last (so a live rule
wins on conflict) — this is what propagates a **YOLO / custom permission
mode**, which is written straight to the session ruleset and never
recorded as an approval. Without this a YOLO parent's children would
launch under default permissions and get stuck on prompts. Controlled by
the default-on `worktree.inherit_permissions` setting (GET/POST
`/api/settings/worktree-inherit-permissions`); snapshot at split time,
soft-fail (never blocks a launch).

Pending permission/question prompts from **subagent sessions** — even
deeply nested ones, and ones launched *outside* ocman's control (native
OpenCode Task subagents) — bubble up to the nearest visible top-level
session row so the user can see and answer them.
`db.GetSessionParentIDs` resolves each prompted session to its
**top-level ancestor** (recursive walk over `session.parent_id`), not
just its immediate parent, so a prompt on a grandchild subagent still
surfaces on the row the user is watching.

Ocman also embeds an **MCP (Model Context Protocol) server** on its own
loopback-only listener, `http://127.0.0.1:8227/mcp` (`-mcp-addr`), plus
the same endpoint on the web UI's port (`:8229`, or `:8228` via the Vite
dev proxy). This exposes action-based `factory`, `routines`, `artifacts`, and
`sessions` (inspect + create) tools plus `embed_file`. See the **MCP server** section
below for setup and available tools.

**Routines** save one prompt for a project and run it manually or on a timeout,
one-time, or five-field cron schedule. An occurrence can create a fresh managed
OpenCode session, reuse the routine's first session, or continue a selected
project session, and remains running until that session settles. `done` is a
successful run; `error` is a failure, while a session lost across restart is
`interrupted`. Missed timeout schedules expire on startup instead of running
late. Definitions and run history live in `state.db`; successful runs may soft-delete their routine.
The `/routines` composer command only inserts a saved prompt for review and
does not start a routine run. Webhook inboxes (`/api/webhook-inboxes`) only
capture deliveries; routines subscribe to one with header/JSON-pointer
filters by choosing the inbox as the routine form's Trigger. See
`docs/features/routines.md`.

**Artifacts** are immutable files and links an agent publishes for a project
(and optionally its session) through the `artifacts` MCP tool
(`help`/`create`/`list`/`get`, no delete). Metadata lives in `state.db`; file
bytes are content-addressed by SHA-256 under `<state dir>/artifacts/blobs/`,
50 MB per file. The `/artifacts` pages and the sidebar Artifacts tab browse
them, updated live by `ocman.artifact.created`. Sharing uploads a write-once
encrypted snapshot (manifest chunk + file chunks) to the share relay, checked
against the relay's size/chunk limits before upload; revoke and artifact
delete remove the relay copy. HTML files preview inert (`sandbox` CSP); the
artifact page's opt-in **Run scripts** uses `/files/<n>/interactive`, whose
CSP and the iframe both grant only `allow-scripts` (never `allow-same-origin`),
pinned for e2e by `frontend/e2e/artifact-html/headers.json`. Shared views stay
inert. Artifacts are local to one ocman instance and
are not routed to remotes. See `docs/features/artifacts.md`.

Ocman also surfaces **PRs and Issues** from the active project's
upstream forge (GitHub or Forgejo) in a sidebar pane next to Session
Info / Session Changes / Working Tree. The pane remains available and
shows an empty state when no supported remote is detected. Forgejo
classification uses the hub's configured hosts. Auth uses env-var
tokens (`GITHUB_TOKEN`, `FORGEJO_TOKEN` / `GITEA_TOKEN`) with a fallback
to the `gh auth token` / `tea login` configuration. Clicking a row
expands it to show the body (markdown-rendered) and a split-button:
default action launches a new OpenCode session in the project
directory; the menu offers "new worktree" instead, which checks out
the PR's source branch into a fresh worktree (or fetches the PR head
ref into `ocman/pr-<n>` for cross-fork PRs after explicit
confirmation). A PR row fetches its CI checks once it is visible and polls
every 5s until every check has finished. Empty results settle after three
successful responses spaced 30s apart; errors back off from 5s to 60s,
respecting rate-limit retry times across viewport changes. Polling pauses while
the document is hidden.
A rate-limited response never counts as settled. The settled result is cached
in localStorage by `host/repo@sha`
(newest 1000, `frontend/src/lib/prChecksCache.ts`) and the pane's refresh
button clears it. The prompt sent to the new session is rendered from a
user-customizable template under Settings → "PR & Issue templates",
persisted in the `setting` table of `state.db` (migration v12). See
`spec/pr-issue-sidebar/` for the full spec.

PR/Issue backend operations are owner-routed. Every project forge endpoint
requires `remoteId` (`local` for the hub); named remotes fail closed when
disconnected. Repository detection and cross-fork fetches run through the
owner's `hostsvc.Host`, while the hub uses its forge clients for PR/Issue
metadata and launches sessions on the owner's compound platform.
The frontend carries that owner through upstream lists, checks, forge-user
identity, git branch highlighting, and optimistic session seeding. Successful
forge-user identities survive PR/Issue tab remounts; failures remain retryable.
The pane's lists are keyed on the session's owner + OpenCode `projectId`
(`useProjectTarget`), so switching between sibling worktrees keeps lists,
filters and loaded CI checks; only the current-branch highlight and the
launch directory follow the session, and launches stay disabled until the
active session has resolved.

Ocman also supports **multi-remote**: one "hub" ocman attaches to other
ocman instances over a long-lived gRPC channel and manages every
machine's sessions from one unified, host-agnostic UI. A remote opts in
by starting with `-remote-listen <addr>` (off by default → NFR-6); each
instance has a stable random instance ID + remote-access token persisted
in `state.db` (migration v14), revealed from its own Settings → Remotes
page. The hub dials each saved remote (token auth, optional TLS via
`-remote-tls-cert`/`-remote-tls-key` or a `grpcs://` address), registering
one `remotePlatform` adapter (compound platform id `r-<remoteID>:opencode`,
AD-2) and one `remoteHost` per connected remote. Two adapter seams keep
this transparent: session-scoped work goes through `platforms.Platform` +
`Registry`, directory-scoped work (git/worktree/tmux/projects) through the
new `hostsvc.Host` + `hostsvc.Router` (`LookupRemote`/`ForDir`) — handlers
resolve an owner and delegate, so the HTTP layer is unchanged. An
explicitly client-supplied `remoteId` is always resolved through
`Server.resolveOwner`, which fails closed with 503 when that remote is not
registered; only *inferred* ownership (`ForDir`) may degrade to the hub.
An instance ID must be unique across connected remotes: the compound
platform id and the router key are both derived from it, so a second
remote claiming a live ID would rebind the first's sessions and host
actions to the wrong machine. `publishAdapters` refuses it, publishes
nothing, and marks that connection `duplicate-identity` so Settings shows
why it never came up (the usual cause is a cloned `state.db`).
Host-local
actions (tmux, worktrees) execute on the owning host. The browser still
talks REST/SSE to the hub only; the hub re-emits remote gRPC event streams
as SSE. New-session creation is machine-aware via
`POST /api/sessions/resolve-targets` + the new-conversation composer's
machine selector (sessions start on the local machine by default). The
frontend stays host-agnostic (host badge + capability flags, no
remote-identity branching; `scripts/check-host-helpers.sh` enforces that
handlers don't bypass the `Host` seam). User-facing docs:
`docs/features/multi-remote.md`. Full design: `spec/multi-remote-support/`.

## Repository layout

- `main.go` — entrypoint; parses the CLI flags (run `ocman -h`, or read
  the `flag.` block in `main.go` for the authoritative list), opens
  databases, registers platform adapters, starts the server. Note
  `-gui` / `-gui-addr`: with `-gui` the process opens a native Wails
  desktop window (`internal/gui`) around the same HTTP server instead
  of only serving HTTP.
- `internal/platforms/` — `Platform` interface, `Registry`, common
  types/errors.
- `internal/plugins/` — native executable discovery, versioned NDJSON protocol,
  process supervision, `action.v1` / `conversation.v1` brokers, and `pane.v1`
  read-only sidebar trees. Trusted
  executables live in
  `~/.local/share/ocman/plugins` or `OCMAN_PLUGIN_DIR`; even discovery runs code.
  Build the bundled ones with `make install-plugin PLUGIN=slack|beads|fixture`
  (`-trimpath`, so the checksum an operator approves is reproducible).
  Settings manages owner-local enablement, grants, configuration, and diagnostics.
  Remote projection uses authenticated `PluginOperation` RPCs; each owner keeps
  its binaries, secrets, and action receipts. The browser receives typed palette
  actions/results, never plugin JavaScript. See `docs/features/plugins.md` and
  `internal/plugins/README.md`. Platform-provider, iframe UI,
  relay inbox capability, registry/updates, signatures, sandboxing, and Codex
  are future work.

  `pane.v1` uses owner-local declarations and the `pane.project` grant.
  `/api/plugins/panes` lists enabled declarations without invoking plugins;
  `/api/plugins/panes/read` sends the directory through the owner's existing
  `PluginOperation` RPC after authorization. Core validates bounded acyclic
  tree data and renders it without plugin HTML, scripts or CSS. The query is
  mounted only while its pane is open, consuming AbortSignal for cancellation.
  The Beads executable and CLI parsers live in `examples/ocman-plugin-beads`,
  no longer on `hostsvc.Host`. See `docs/features/beads.md`.

  **conversation.v1** is the chat-provider seam (`internal/plugins/conversation.go`,
  `conversation_broker.go`, wired in `internal/server/plugin_conversation.go`).
  A plugin emits an unsolicited normalized `message` event; the broker authorizes
  it against enablement, the `conversation.session` grant, and the plugin's one
  required `project` setting (all read from one row under one lock), then core
  creates or resumes a managed session for the conversation and sends the text.
  The `(pluginId, accountId, threadId)` → `(platformId, sessionId)` mapping is
  durable (`state.db`'s `plugin_conversation`, migration v96) and claimed by an
  atomic insert-if-absent, so a restart continues the same session, concurrent
  first messages cannot map two, and two workspaces reusing a thread identity
  stay isolated; `platformId` owner-qualifies the session. A connector is
  **owner-scoped by declaration** (`Description.Validate` refuses `hub`/`global`
  with the capability): it holds one owner's provider credentials and drives that
  owner's one project, so two machines sharing a provider app would compete for
  one Socket Mode stream. `createConversationSession` therefore uses
  `Router.Local()`, never `ForDir` — inferred ownership could run the session on
  a machine that approved nothing, and its permissive fallback would silently
  degrade a disconnected remote back to the hub. The hub only projects Settings
  for a remote owner, and fails visibly (503) when that owner is disconnected.
  Each inbound message
  carries the provider's stable `eventId`, reserved as
  `conv-in:<accountId>:<eventId>` in the durable operation-receipt table *before*
  any work, making a redelivery at-most-once rather than a duplicate prompt.
  Delivery goes through `queuesvc.Enqueue` (not `sendNow`): an idle session
  answers now, a mid-turn message is held for the next `session.idle` edge in
  arrival order, because an external message must never interleave into a turn
  nobody in the thread can see. On the `session.idle` edge the host resolves the
  thread from the session and appends the newest assistant message's text to a
  **durable reply outbox** (`plugin_conversation_outbox`, migration v97) keyed by
  `<sessionId>:<messageId>`, so a repeated edge appends nothing.

  Delivery is a separate, replayable step (`plugin_conversation_outbox.go`): each
  row's AUTOINCREMENT id is both its immutable identity and its sequence, and is
  passed as the wire `operationId` (`conv-out:<id>`), stable across retries. The
  pump claims only the *head* of each ordering group — one provider conversation
  — so replies in a thread never overtake each other while unrelated threads are
  delivered concurrently. Acknowledgment follows the call, making delivery
  **at-least-once**: a crash in that window replays the same operation id, and
  the provider adapter is responsible for not posting twice (an uncertain post is
  never repeated blindly). Failures retry with bounded backoff (5s → 5m); after 6
  attempts a delivery becomes a visible dead letter that blocks only its own
  conversation until a localhost-only `conversations/retry` or
  `conversations/discard` decides, surfaced in Settings → Plugins → Reply
  delivery via `conversations`. The pump re-asks `plugins.ConversationAllowed`
  *before* scheduling a row, so disabling an installation or revoking its grant
  stops delivery at once without spending the reply's retry budget — the row
  stays pending and unattempted, keeping its mapping and text, and re-enabling
  delivers exactly the work already owed. Per-plugin count and byte caps pause *inbound*
  admission before the event receipt is reserved, so pressure stops new work
  visibly instead of dropping owed replies, and a redelivery is still accepted
  once the backlog drains. Provider details stay in the plugin:
  `examples/ocman-plugin-slack` speaks Slack Socket Mode
  (`apps.connections.open` → wss → `app_mention` → `chat.postMessage`), drops
  anything bot-originated so a reply cannot loop, and honours `Retry-After`.
  `internal/server/plugin_conversation_slack_test.go` is the acceptance
  walkthrough for that executable — the host's describe/discovery, the SDK
  conversation conformance suite, and one end-to-end path (first mention,
  mid-turn follow-up, completed reply, restart recovery) against a mock
  workspace. The plugin's Web API base is redirected there with
  `OCMAN_SLACK_API`, a test-wrapper hook only: ocman launches plugins with a
  fixed environment, so it is unreachable in production. Mock coverage is not
  live validation; a real Slack app, tokens and channel invite still need the
  manual walkthrough in `docs/features/plugins.md`.
  Plugin events are drained by `Server.consumePluginEvents`; an unread event
  flood fails the process.

  **Attention notices** (`plugin_conversation_notice.go`) tell a thread its
  session cannot proceed: a permission or question awaiting the user, a turn
  that errored, or a message accepted from the provider that reached no session.
  They ride the same outbox, so dedup is the `(plugin, operation_id)` uniqueness
  and needs no new state. Three invariants: a notice carries a fixed sentence
  from `conversationNoticeText` plus a link and **never** session content
  (permission text, patterns, metadata, error, transcript); it is a pointer, not
  a prompt, so decisions stay in ocman under existing provenance/autoapproval
  rules; and it **resolves rather than retracts** — keyed on the prompt's request
  id, so a re-observed prompt appends nothing, and answering it lets the
  continued turn's reply land under the notice. The trigger is the
  `autoapprove.Deps.PromptNeedsUser` hook, fired *downstream of the approval
  decision* (`handleUnsafeVerdict` and the `!enabled` branch of
  `backgroundAutoApprove`, plus `ObserveQuestionPrompt` since a question is never
  auto-answered) — reporting on the raw `permission.asked` edge would page the
  user for every command the judge is about to approve. Links come from
  `Server.publicURL`, i.e. `-public-base-url` / `OCMAN_PUBLIC_BASE_URL`; without
  it they degrade to the loopback listen address, which is why the plugins doc
  tells operators to set it before pointing a plugin at a remote provider.
- `internal/platforms/opencode/` — OpenCode adapter wrapping the DB
  + HTTP proxy client.
- `internal/ocv2/` — OpenCode v2 compatibility: version detection, the
  v1-on-v2 RoundTripper (`compat.go`, `routes_*.go`), the event translator
  (`events*.go`) and the v2→v1 converters (`message.go`, `tools.go`,
  `catalog.go`).
- `internal/sessionsvc/` — session mutation service (validation,
  adapter selection, side-effect hooks). REST handlers and the remote
  gRPC server delegate session mutations to it
  (one shared mutation path).
- `internal/queuesvc/` — follow-up message queue (#58). Queueing is an
  **explicit user gesture**: plain **Enter** in the composer sends
  immediately (mid-turn included — OpenCode interleaves the prompt into
  the running turn), while **Ctrl/Cmd+Enter** holds it in `state.db`
  (shared across every client, survives a client moving machines) to be
  drained one-per-turn on the `session.idle` edge. The `queue` flag on
  `POST /api/session/{id}/message` selects the path; when it is false the
  handler calls `Server.sendNow` and never touches the queue. The flag is
  never derived from inferred status, which lags the SSE stream. Held
  messages are drained only by flush (serialized by a drainable worker), so
  there's no check-then-act race. The idle-edge flush trusts the edge and does
  **not** re-check the (lagging) inferred status, so a genuine turn-end
  never leaves the head stranded. A periodic one-minute `Sweep`
  (`runQueueSweep`) is only a recovery backstop: it drains one message from
  each idle session with a standing backlog after a missed edge or crash.
  Each drain decision makes one owner-routed `platforms.LifecycleReader`
  read (settled status + latest message; no parts, tree or costs; the
  `SessionLifecycle` RPC for remotes), never a transcript-sized `Session`.
  Wired in `internal/server/queue.go`.
- `internal/db/` — read-only SQLite queries against OpenCode's
  `session`, `message`, `part` tables; uses `json_extract` heavily.
- `internal/state/` — writable SQLite database
  (`~/.local/share/ocman/state.db`) for ocman's own state (archived
  / seen sessions). Primary key is `(platform, session_id)` so it
  can scope state per platform.
- `internal/mcp/` — MCP server implementation. Tool handlers implement the
  action-based Factory, routine, artifact, session (read-only actions plus `create`), and file tools. Mounted at `/mcp` by the server
  package.
- `internal/server/` — HTTP server, API handlers, static file serving
  with SPA fallback, OpenCode port discovery via `lsof`.
- `internal/tmux/` — tmux process control: session/window listing,
  name derivation/validation, opencode launchers (runner seams for
  tests). HTTP handlers stay in `internal/server`.
- `internal/ocruntime/` — runtime-neutral managed-opencode lifecycle
  (#376). A `Runtime` interface (`Launch`/`Probe`/`Stop`) abstracts how a
  project's opencode instance is hosted; the native-tmux implementation
  runs `opencode --port N` on an ocman-allocated loopback port and probes
  `GET {endpoint}/config` for health. Plug point for the container
  runtime (epic #375). Driven by `hostsvc/local`'s
  `EnsureProjectOpencode` / `RestartProjectOpencode`, which persist each
  instance in `state.db`'s `managed_opencode` table (v35, probe-on-
  recovery). Launch capability is surfaced as the `HostCaps.OpencodeLaunch`
  (`opencodeLaunch`) flag, distinct from `Tmux`; the frontend gates
  managed-launch UI on it.
- `internal/term/` — in-app browser terminals: window naming/hashing,
  window management in the dedicated `ocman-term` tmux session on its own
  named server (`tmux -L ocman-term`), and
  the PTY bridge. WebSocket/REST layer stays in `internal/server`.
- `internal/whisper/` — self-contained voice transcription via a local
  whisper-cpp binary (+ ffmpeg conversion).
- `internal/autoapprove/` — the LLM-judged permission auto-approve
  pipeline (judge, per-permission state machine, safe-command cache,
  SSE tee/sinks, headless watcher). Wired into the server through an
   `autoapprove.Deps` seam by `internal/server/autoapprove_engine.go`.
- `internal/hostsvc/` — the directory/host-scoped adapter seam (`Host`,
  `Router`), the directory analogue of `platforms.Platform`.
  `internal/hostsvc/local/` is the only package that reaches for
  git/tmux/whisper directly; handlers resolve an owner and delegate.
- `internal/remote/` — multi-remote gRPC client/server plus the
  generated `proto` stubs (regenerate with `make proto`).
- `internal/gitexec/` — hardened `git` subprocess construction (strips
  `GIT_DIR`/`GIT_INDEX_FILE`, disables terminal prompts and optional
  locks). Every git invocation must go through it.
- `internal/git/` — repository queries built on `gitexec` (branches,
  status, worktrees, diffs).
- `internal/forge/` — GitHub/Forgejo PR + issue clients behind one
  interface; feeds the PRs & Issues sidebar.
- `internal/routines/` — validation, scheduling, fresh-session dispatch, and
  session-settled run completion for saved prompts. Definitions and immutable
  run snapshots are stored by `internal/state`. See
  `docs/features/routines.md`.
- Artifacts have no package of their own: `internal/state/artifacts*.go`
  holds the tables and blob store, `internal/server/artifacts*.go` /
  `handlers_artifacts.go` / `artifact_share.go` the service, REST API and
  relay sharing, and `internal/mcp/tools_artifacts.go` the `artifacts` tool.
  See `docs/features/artifacts.md`.
- `internal/permissions/` — builds the inherited permission ruleset for
  a worktree session (#101).
- `internal/pricing/` — LiteLLM model-pricing fetch/cache + cost
  calculation for the usage metrics views.
- `internal/ocapi/` — host-local auth shared by every ocman client that
  talks to OpenCode's HTTP API.
- `internal/opencodeskills/` — installs ocman-owned embedded skills into
  OpenCode's global skill directory.
- `internal/opencodeconfig/` — reads/writes the `mcp.ocman` entry in
  OpenCode's global config (`~/.config/opencode/opencode.json`, honouring
  `$XDG_CONFIG_HOME`/`OPENCODE_CONFIG`). Backs the original up to
  `opencode.<timestamp>-backup.json`, writes atomically, and refuses any
  config it can't round-trip losslessly (`.jsonc`, or comments in a
  `.json`) so hand-written files are never mangled. Drives the
  `McpConfigPrompt` toast via `GET /api/mcp/config` +
  `POST /api/mcp/config/install`.
- `internal/srvtiming/` — per-request phase timing rendered into the
  `Server-Timing` response header; no-op outside an HTTP request.
- `internal/toolpath/` — `Ensure()` merges the login shell's PATH into
  the process PATH at startup, so a launchd/login-item start still finds
  homebrew and mise/asdf shims (`tmux`, `opencode`, `git`).
- `internal/share/` — share snapshot format + relay client.
  `ResolveRelayURL` picks the relay endpoint (`-relay-url` flag >
  `OCMAN_RELAY_URL` > `DefaultRelayURL` baked into the build) and fails
  startup on an invalid value rather than silently disabling sharing.
- `internal/telemetry/` — OpenTelemetry wiring (see Key details).
- `internal/gui/` — Wails desktop shell used by `-gui`; wraps the same
  HTTP server in a native WebView window.
- `frontend/` — React + TypeScript + Vite SPA (port 8228 in dev).
- `internal/webui/static/` — Vite build output; embedded into the Go
  binary via `//go:embed`. Gitignored except for `robots.txt`, which
  is kept as a permanent placeholder so `go:embed static/*` always has
   at least one file to embed (avoiding churn from build-hashed assets
   like `index.html`).
- `site/` — Hugo (Hextra theme) marketing/docs site. Holds only the
  landing page, templates and config; **all documentation content lives
  in `docs/`**, mounted read-only via `hugo.toml`. `docs/` mirrors the
  site's five chapters — `introduction/`, `features/`,
  `configuration/`, `faq/`, `other/` — with a `_index.md` per chapter
  and `title:`/`weight:` front matter driving nav order. Serve it with
  `make docs`, build with `make docs-build`.
- `deploy/` — deployment and local-proxy config: `Caddyfile` (HTTPS via
  `tailscale cert`, host from `OCMAN_CADDY_HOST`), `Dockerfile.relay`
  (build context is the repo root), `docker-compose.otel.yml` (the
  `make otel-*` LGTM stack; volume paths point at `../observability/`).

## Development database isolation

Every development run that can write to a database or apply migrations must use a disposable database isolated to the project AND worktree or feature branch. This includes application startup, dev servers, migration commands, scripts, and integration tests. Startup may apply migrations automatically; inspect the resolved database target before launching it.

- Never point in-development code at the user's live database, a shared development database, or another checkout's database. A different HTTP port does not isolate a database.
- Prefer the project's existing database-path or connection-string override. For file databases, use an ignored checkout-local directory with a separate path per feature branch, such as `.dev/<branch-key>/state.db`. For database servers, use a separate database or schema keyed by project, checkout, and branch. Avoid colliding sanitized branch names. Tests should use temporary databases.
- Start empty and apply migrations plus test fixtures. If realistic data is necessary, use a database-consistent backup or export into the isolated target. Never blindly copy a running database file, and never copy experimental state back into the live database.
- Verify the effective path or connection target, including subprocesses and background services. If no isolation override exists, add the smallest explicit override before running development code; do not fall back to the shared default.
- Keep local databases and credentials out of version control. Only apply migrations to a live or shared database when the user explicitly requests that deployment or migration against that specific target.

## Dev commands

```sh
make dev              # backend (air :8229) + frontend (vite dev :8228) with HMR
make dev-prod         # backend (air :8229) + frontend (vite preview :8228, manual rebuild)
make dev-prod-watch   # backend (air :8229) + frontend (vite preview :8228, auto-rebuild)
make dev-backend      # air only (Go on :8229)
make dev-frontend     # vite only (React on :8228, proxies /api to :8229)
make dev-remote       # backend with the remote-access gRPC server on :8230
make kill-dev         # kill orphans squatting on 8228/8229/8230

make test             # go test ./... + vitest run
make test-backend     # go test ./...
make test-frontend    # vitest run
make test-race        # go test -race ./internal/...
make test-fuzz        # run every Fuzz* target for 10s
make test-e2e         # build frontend, then Playwright
make test-all-fast    # backend + frontend + e2e in parallel, fail fast
make coverage         # collect coverage/*.json (SUITE=go|frontend|all)
make coverage-check   # ratchet coverage/*.json against $(BASELINE_DIR)

make lint             # go vet, golangci-lint, tsc -b, eslint + the three guards
make build            # production: pnpm install + pnpm build, then go build -o ocman .
make build-desktop    # Wails desktop app (the -gui path) into build/bin/
make proto            # regenerate internal/remote/proto stubs (needs protoc)
make install-hooks    # pre-commit + pre-push hooks
make clean            # removes ocman binary, tmp/, and static/assets/

make docs             # Hugo docs/marketing site with live reload (:1313, DOCS_HOST/DOCS_BIND/DOCS_PORT)
make docs-build       # static site into site/public

make otel-up          # start Grafana LGTM stack (Loki/Tempo/Mimir + OTLP) at :3000/:4317/:4318
make otel-down        # stop the LGTM stack
make otel-logs        # tail LGTM container logs
make otel-reset       # stop + wipe persisted telemetry data
```

`make help` lists every target carrying a `## ` doc comment; the
Makefile is the authoritative list.

- `mise` provides `air` (Go live-reload), `node`, and `pnpm`. Run
  `mise install` if any of them are missing. **Use `pnpm`** for all
  Node-side commands — `npm` is no longer the supported package
  manager. The version is pinned via the `packageManager` field in
  `frontend/package.json` and via `mise.toml`.
- Both dev and dev-prod modes use port **8228** for frontend, **8229** for backend.
- The frontend (Vite dev or preview) proxies `/api` requests to `localhost:8229`.
- Air rebuilds Go on source changes but does **not** re-embed the
  frontend bundle. After editing frontend code, either (a) use the
  Vite dev server on :8228 instead of the embedded build, or (b)
  run `cd frontend && pnpm build` and touch a `.go` file to
  trigger Air.

## Repository hosting

This repository is self-hosted on **Forgejo** (not GitHub). Use the
`tea` CLI for all issue and pull-request operations — `gh` is not
configured for this remote.

```sh
# Issues
tea issues ls                          # list open issues
tea issues create -t "title" -d "body" # open a new issue
tea issues close <id>                  # close an issue

# Pull requests
tea pulls ls                           # list open PRs
tea pulls create -t "title" -d "body"  # open a PR (branch must be pushed first)
tea pulls merge <id>                   # merge a PR

# Always pass --repo dries/ocman when running outside the repo root,
# or when the remote is not auto-detected.
```

## Build pipeline

1. `cd frontend && pnpm install --frozen-lockfile && pnpm build` —
   builds frontend into `internal/webui/static/`.
2. `go build -o ocman .` — embeds `internal/webui/static/` via
   `//go:embed`.

Order matters: frontend must be built before `go build` so static
assets are embedded.

## Verification

CI is defined in `.forgejo/workflows/ci.yml` and runs only on Forgejo
Actions. Keeping workflows under `.forgejo/workflows/` prevents the GitHub
mirror from discovering and running them. The workflow remains Forgejo-aware
(see the pnpm-install comment near the top and the
`FORGEJO_API_URL: ${{ github.api_url }}` env).

Jobs and the checks they run:

```sh
# Frontend job
cd frontend && pnpm lint            # ESLint
cd frontend && pnpm exec tsc -b     # TypeScript typecheck
./scripts/check-platform-branching.sh  # AD-12a: no `platform === '...'`
./scripts/check-settings-rows.sh       # no hand-rolled settings-row markup
make coverage SUITE=frontend        # vitest with coverage
make coverage-check SUITE=frontend  # coverage ratchet vs the gh-pages baseline

# Backend job
golangci-lint run                   # config in .golangci.yml (pinned version, includes govet)
go build -o /dev/null .             # production binary links
go test .                           # root package
make coverage SUITE=go              # internal/... with coverage
make coverage-check SUITE=go        # coverage ratchet

# Other jobs
pnpm exec vite build && pnpm test:e2e   # Playwright e2e (chromium) against the built bundle
```

Locally, `make test` and `make lint` cover the same ground.
`make lint` runs `go vet`, `golangci-lint run`, `tsc -b`, `pnpm lint`,
and the three guard scripts (platform-branching, host-helpers,
settings-rows). Install `golangci-lint` locally — CI enforces it and
`.golangci.yml` enables linters beyond the standard set. Keep diffs
minimal and match the surrounding code.

## Key details

- **`-platforms` flag**: comma-separated list of platforms to enable
  (default `"opencode"`). Currently the only valid value is `opencode`.
  Only the listed adapters are registered; the OpenCode database is
  not required when `opencode` is omitted from the list.
- **Pure-Go SQLite**: uses `modernc.org/sqlite` (no CGo, no C compiler required).
- **Two databases**: OpenCode's DB is opened read-only
  (`?mode=ro&_journal_mode=WAL`, default `~/.local/share/opencode/opencode.db`).
  The only writer is `internal/ocmaint` (Settings → Maintenance), a
  user-started job that stops the managed instances, blocks launches, refuses
  while any other process holds the file, and moves `summary.diffs` from
  sessions older than 30 days into `<db>.ocman-diffs` so they can be restored.
  Ocman's own state DB is writable (`~/.local/share/ocman/state.db`),
  auto-creates its schema, and runs a versioned migration on startup.
  A third, disposable file, `analytics-cache.db` beside state.db, is a slim
  copy of OpenCode's message/session rows and minimal tool timings that the analytics queries read
  (`internal/db/analytics_mirror.go`). It uses OpenCode's table names, so the
  same SQL runs against either database, and it falls back to opencode.db
  until the first build finishes. It has no migrations: a schema bump or a
  different `-db` wipes and rebuilds it.
- **OpenCode port discovery** uses `lsof` to find processes named
  `opencode` listening on TCP, then resolves their cwd. macOS/Linux
  only. Cached with a 10-second TTL.
- **Session reads go through one snapshot**
  (`internal/platforms/opencode/models_cache.go`). The full aggregate
  query in `db.GetSessions` is expensive (~4.3 s on a 12 GB OpenCode
  DB), so it runs as rarely as correctness allows:
  - There is **one global snapshot**, not one per directory. A
    directory-scoped listing filters that slice in Go; `since` is
    filtered on read for the same reason (a rolling `since` would
    otherwise leak one cache entry per poll). Filtering always copies,
    so read-time overlays can never mutate the snapshot.
  - A **TTL-expired** snapshot is served immediately and revalidated in
    the background; a slow scan must not become endpoint latency. An
    upstream session creation refreshes that exact row before broadcasting
    `ocman.session.changed`, so externally-created sessions appear immediately
    without a full scan when the snapshot is warm. **Explicit invalidation**
    (`InvalidateSessionsCache`) remains the synchronous fallback when the
    snapshot is cold or that row refresh fails.
  - The refresher recomputes **only sessions the event stream marked
    dirty**, via `db.GetSessionSummary`. That single-session read
    reuses the list query's projection and scan so it cannot drift;
    if you change one, you change both.
  - Events are a hint, never the source of truth. An event with no
    resolvable session id marks the whole snapshot dirty, and a full
    reconciliation runs every 5 minutes regardless, so deletions and
    missed events are always corrected.
  - The cache is package-global and assumes **one local OpenCode
    database** per process (see "Two databases" above).
- **Session status** is a closed, typed set — `db.SessionStatus`
  (`busy`, `waiting`, `done`, `error`, `interrupted`), mirrored by the
  exported TS `SessionStatus` union. It is **settled from the agent's own
  turn lifecycle**, not guessed from stored messages (#488). One function
  decides it, `db.SettleSessionStatus(turn, live, inferred)`:
  - The live signal comes from OpenCode itself —
    `GET /session/status` (`{sessionID: {type: "busy"|"retry"|"idle"}}`),
    seeded per instance when the autoapprove watcher connects (OpenCode
    scopes it per directory, so the seed also reads each existing worktree
    with a session updated in the last 24h or whose last message left the
    turn open (`finish` `tool-calls`/`unknown`, which OpenCode's loop treats
    as non-terminal); one failed read leaves the port unseeded) and kept
    current from `session.status` events on `/global/event`. It lives in
    `internal/platforms/opencode/live_status.go`, keyed by instance port,
    and is dropped wholesale when a port disappears. Nothing is
    persisted: OpenCode owns this state, so a restart re-seeds instead of
    trusting a stale copy.
  - `db.InferSessionStatus` (last message's `role`/`finish`/`error`) is
    demoted to answering one question: *which* terminal state a settled
    session is in. It is never read as "running".
  - No live view + an unfinished turn = `interrupted`: the process that
    owned the turn is gone, so it can never finish.
  Consequently there is no `STATUS_GRACE_MS` debounce and no sticky-busy
  merge in the sidebar — the value no longer lags, so compensating for
  lag would only add staleness. The queue's idle-edge flush still trusts
  the edge, but only for ordering independence, not because the status
  can't be trusted.
- **Auto-archive**: background goroutine archives sessions inactive
  for 7+ days (checked every 24 h). Runs against all registered
  platforms.
- **OpenTelemetry (optional)**: `--otel=<endpoint>` /
  `OTEL_EXPORTER_OTLP_ENDPOINT` ships traces + metrics + logs to an OTLP
  collector; empty = no-op (zero overhead). Implementation in
  `internal/telemetry`: `otelhttp` on the mux + outbound clients,
  `otelsql` on both SQLite handles, custom spans/metrics around the
  auto-archive loop, projects-index refresh, SSE streams, and
  `srvtiming` boundaries; a logrus hook stamps `trace_id`/`span_id`, and
  an OTLP log hook exports batched records while preserving console output.
  `make otel-up` runs the bundled Grafana LGTM stack and the `make dev*`
  targets auto-export the dev endpoint. User-facing config (URL scheme →
  transport, `OTEL_*` vars, dashboard) is in `docs/configuration/_index.md`;
  dashboard provisioning in `observability/`.
- **Tmux session name character limitations**: `tmux.SessionNameForPath`
  derives a session name from the worktree directory. tmux itself
  replaces dots with underscores when displaying session names, so a
  path like `/home/u/src/github.com/foo` becomes the session name
  `~/src/github_com/foo` in `tmux list-sessions` output. Two
  character sets are enforced in `internal/tmux/sessions.go`:
  - `tmux.ValidName` (`[a-zA-Z0-9._/~:-]+`) — used for user-supplied
    target identifiers such as `session:window` pairs.
  - `tmux.ValidComponent` (`[a-zA-Z0-9._/~-]+`) — used for names
    *derived from filesystem paths* (session names, window names).
    The colon is **excluded** because tmux uses `:` as the
    session/window separator in target identifiers; an embedded `:`
    would silently mis-target the wrong pane.
  If a derived session name contains any character outside
  `tmux.ValidComponent`, `tmux.LaunchOpencodeWith` /
  `tmux.LaunchOpencodeEnvWith` return an error. In practice this is
  rare: only atypical *project* directory names (e.g. those containing
  `:` or spaces) can trigger it.
- **Optional password auth** (`internal/server/auth.go`): off by
  default (binds `127.0.0.1:8228`, unauthenticated). When configured it
  applies to every client including localhost; password is
  bcrypt-hashed, cookies are HMAC-signed (stateless) with a key in
  `state.db`'s `auth_secret` table, logins rate-limited 5/min/IP. The
  precedence (`OCMAN_AUTH_PASSWORD` > `-auth-password-file` >
  `-auth-password`), the `-auth-trust-localhost` escape hatch, and full
  setup are documented in `docs/configuration/_index.md`.

## MCP server

Ocman embeds a localhost-only MCP server (`internal/mcp/`, mounted at
`/mcp` by the server package) exposing `factory`, `routines`, `artifacts`,
`sessions` (inspect + create), and `embed_file`.
The authoritative tool list is the table in
[`docs/features/mcp.md`](docs/features/mcp.md#tools) — don't duplicate it here.

Implementation notes:

- Registration is self-service: `McpConfigPrompt` (mounted at the app
  root) polls `GET /api/mcp/config` once per load and offers an Install
  button that POSTs `/api/mcp/config/install`, writing the entry through
  `internal/opencodeconfig` (backup first, refuses non-round-trippable
  configs). OpenCode must be restarted to pick it up.
- Two mounts, one handler (`Server.mcpHandler`, built once): the main mux
  at `/mcp` under `requireLocalhost` (password auth applies), and a
  dedicated loopback-bound listener (`-mcp-addr`, default
  `127.0.0.1:8227`, `startMCPListener`) under `requireLoopbackPeer`,
  which treats the loopback peer as the credential. The second listener
  exists because native MCP clients can't send an auth cookie; binding it
  separately keeps it unreachable through a reverse proxy pointed at
  `-addr`. Non-loopback `-mcp-addr` values are refused (fails closed).

User-facing setup and the full tool table are documented in
`docs/features/mcp.md`.

The owner-local `webhooks` MCP tool creates and inspects inboxes, saves or
removes routine subscriptions, reads recent deliveries, and redelivers a
stored delivery by ID (including ones past the recent list). It invokes the
existing inbox HTTP handler in-process through `Deps.WebhookHandler`, using
only fixed routes after MCP authentication. Relay registration and polling,
predicate validation, and local-routine checks stay in that handler. It uses
saved relay settings and omits shared secrets from inbox results. Subscription
actions do not alter routine schedules or enabled state; agents use `routines`
to set those explicitly.

## Architecture doc

`docs/other/architecture.md` holds the Mermaid architecture diagrams (system
context, backend composition, session/event data flow, frontend
composition). When a change alters what those diagrams show — a new or
merged `internal/` package, a new external dependency (database, forge,
protocol), a new seam like `platforms.Platform`/`hostsvc.Host`, or a
change to the browser↔backend data flow — update the affected diagram
and its bullet list in the same PR. Keep diagrams at ~10 blocks; push
detail into the prose below each one.

## Conventions

- Application Go packages live under `internal/`. The optional public plugin SDK
  is `sdk/plugin`, with reusable tests in `sdk/plugin/conformance` and a deterministic
  executable example in `examples/ocman-plugin-fixture`. SDK DTOs alias the canonical
  types in `internal/plugins`; do not duplicate the wire definitions.
- **Platform-agnostic frontend.** The UI must not branch on
  `session.platform === '...'`; capability gating goes through
  `/api/capabilities` + `useCapabilities()`. Enforced by
  `scripts/check-platform-branching.sh`, which `make lint` runs.
  The pragma `// ocman:allow-platform-branch` can suppress false
  positives when the comparison is part of a generic helper.
- **Consistent UI: reuse shared components before writing markup or CSS.**
  New pages and panes must look like the existing ones. Before writing any
  markup, look for a shared component and use it:
  - controls: `Button` / `AnchorButton` (variants `accent`, `danger`,
    `ghost`, …), `ButtonGroup`, `TextField`, `SearchField`, `SelectField`,
    `TextareaField` (`components/Control.tsx`), `CopyButton`, `IconButton`
  - dialogs: `Modal` (themed by default; leave the class props unset),
    `ModalHeader`, `ModalFooter`
  - layout and state: `DataTable` (`framed` for a bordered, scrollable card),
    `DataTableGroup`/`DataTableRow`, `EmptyState`, `LoadingState`, `Tabs`,
    `SegmentedControl`, `SettingRow`, `HeaderPortal` for header actions
  - markdown: wrap `MarkdownContent` in `.oc-md`

  Don't hand-roll raw `<button>`/`<input>`/`<select>` styling, danger
  colours, or table frames. If the same pattern shows up a second time,
  extract it into a component instead of copying it. Colours and fonts come
  only from tokens defined in `frontend/src/tokens.css` (`--bg`, `--bg-card`,
  `--border`, `--text`, `--text-dim`, `--accent`, `--danger`,
  `--font-conversation-mono`, …). A `var()` naming a token that doesn't
  exist (such as `--text-muted`, `--surface`, `--font-mono`) silently falls
  back to inherited styles, which is how off-theme UI gets in. Add a new page
  title to `lib/routeTitle.ts` so the app header labels it.
- **Terminology**: *platform* = the tool that produced the session
  (OpenCode). *Agent* = a composer-level role within a session
  (OpenCode's `build` / `plan` / user-defined subagent).
- API routes use `requireGET` / `requirePOST` wrappers for method
  enforcement. Privileged host-control routes additionally use
  `requireLocalhost`, which checks both the loopback peer and browser origin.
- Frontend state management uses Zustand. Routing uses
  react-router-dom.
- Tests live alongside code as `*_test.go` / `*.test.ts(x)`. Prefer
  table-driven tests in Go. The server package has a shared
  `fakePlatform` for integration tests.
- **Test coverage must not drop.** CI runs a coverage ratchet
  (`make coverage` / `make coverage-check`, see
  `spec/ci-coverage-ratchet/`) that fails any PR lowering Go or
  frontend total line coverage beyond a 0.1% slack. Treat this as a
  hard requirement, not a suggestion:
  - **New code ships with tests.** Every new function, branch, loop,
    parser, or money/security path gets a test in the same PR. Bug
    fixes get a regression test. Don't rely on existing tests to cover
    new behaviour.
  - **Prove bugs red/green.** When fixing a bug, first write a test
    that reproduces it and *fails* on the unpatched code (red), then
    apply the fix so it *passes* (green). State in the PR that you saw
    it fail before the fix. A fix without a failing-first test is
    incomplete.
  - **Verify before committing.** Run `make coverage-check
    BASELINE_DIR=<baseline>` (or at minimum `go test -cover ./...`
    and `cd frontend && pnpm test -- --coverage`) and confirm the
    delta is ≥ 0 for the side you touched. If coverage drops, add
    tests until it doesn't — do not lower the baseline.
- **E2e test locators.** Playwright e2e tests (`frontend/e2e/`) must
  prefer stable locators over CSS class selectors. Priority order:
  1. **ARIA roles / labels** — `getByRole`, `getByLabel`,
     `getByText`. Preferred when the element has a natural accessible
     name; doubles as an accessibility check.
  2. **`data-testid`** — `getByTestId('loading-spinner')`. Use for
     structural / state elements that lack a meaningful accessible
     name (loading states, layout containers, backdrops).
  3. **CSS class selectors** — `.oc-foo` — last resort only. Fragile
     across refactors and styling changes.
  When fixing a broken e2e test, replace the CSS-class locator with
  an ARIA or `data-testid` locator rather than patching the class
  name. New components should add `data-testid` attributes for any
  element that e2e tests need to target.
