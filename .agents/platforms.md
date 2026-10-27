# Platforms and managed runtimes

## Terminology and adapters

- A platform is the tool producing a session: OpenCode, Claude Code or Codex.
- A backend serves a platform through the internal socket protocol.
- A model provider is an LLM service, such as Anthropic or OpenAI.
- An agent is a composer-level role, such as `build`, `plan` or a user subagent.

Preserve legacy provider names in commands, packages, wire fields and databases.
`Hello.PlatformID` and `BackendRuntime.PlatformID` identify the platform.
Adding a platform means a new `platforms.Integration` (execution, live
observation, `HistoryReader` ingestion and `SessionIdentityProbe`) composed by
  `owned.BuildCore`, with historical cutover in `owned.PrepareBuiltCore` before
  session admission. Integrations never serve browsing reads; the canonical
adapter serves those from statev2.
See `spec/multi-agent-support/architecture.md`.

`-platforms` defaults to `opencode,claudecode`. Only selected adapters register;
without OpenCode its database is not required. Settings persists the local
selection; an explicit flag wins. Saving drains active local turns, prompts and
queues, then reloads on the same port. Completed `waiting` sessions without
pending prompts do not block draining.

## Internal backend protocol and Claude

Read `internal/providerprotocol/README.md` and `spec/multi-agent-support/` before
changing the protocol. It is internal, with no stability promise until a second
provider exists. HTTP and SSE use an owner-local Unix socket, `/hello` versions
and capabilities, and OpenCode v1 event shapes plus `limited` with `resetsAt`.

- `internal/platforms/provider` owns per-adapter known-session, status and prompt
  caches, never package globals. Its headless watcher reconnects and acknowledges
  completed turns; browser streams replay from leased snapshots.
- `providersupervisor.Supervisor` sequences events and syncs them to a per-session
  spool before publication. Reconnect uses `/events?after=<seq>`. Truncate only
  after the headless watcher acknowledges completion; browser snapshots lease
  cursors until SSE attaches. `/drain` stops new turns before replacement.
- Claude browsing uses one in-process supervisor per machine on a private temp
  socket in `startClaudeBrowser`. It lists transcripts and launches nothing.
  External root sessions are adopted on first send with a nonblocking,
  best-effort external-instance warning.
- Launched/adopted sessions use one tmux-hosted supervisor per repository root,
  through `ocruntime.BackendRuntime` and `hostsvc/local.ProviderManager`, persisted
  in `managed_provider`. Children run in their session worktrees.
- Machine-wide flock slots cap children at four under `<data>/claude-slots`.
  Helpers for probes, judging and naming have a separate cap of two through
  `claudeprovider.HelperSlot`. Slot-store I/O errors fail the turn, not wait.
- `claude-sessions.json` ownership uses a shared file lock. Sessions record their
  launching `CLAUDE_CONFIG_DIR` and are read-only under another configuration.
- Restart re-probes persisted endpoints and replays unacknowledged turns and
  prompts. Dead supervisors relaunch on next send. Protocol or CLI/config
  identity mismatch drains and replaces only when idle. While draining, keep
  prompts answerable using `provider.NewDraining`'s version-independent routes;
  new turns return `ErrBusy`.
- Launches pass the resolved CLI, config directory, skills plugin and bound MCP
  address. Keep `claude-launch.json` updated so surviving supervisors' next
  children use a rebound listener.
- Publish idle through `Supervisor.PublishIf`, atomically with `BeginTurn`.
  Never acknowledge failed rules, ownership or spool persistence.

`internal/platforms/claude/provider` owns transcript indexing in disposable `claude-index.db`
and the single Claude-to-v1 converter. Managed children use
`--permission-prompt-tool stdio`; tool controls become permission/question events,
decided by `provider.Rules`, auto-approval and the user. Without OpenCode use
`autoapprove.ClaudeJudge`. Terminal sessions use transcript status.

The hook plugin checks `OCMAN_MANAGED=1`, so standalone Claude skips it.
`ocman hook <event>` posts to MCP `/hook` with a one-second timeout and always
exits zero; the server forwards to provider `/hooks`. Ignore hooks for sessions
ocman drives. Install through `hostsvc.HookPluginManager`, not application flags.
Tests use `claudetest.Binary` and versioned fixtures, never real `claude`.

`internal/platforms/claude/launch` shares the Agent SDK-compatible child environment for
sessions and helpers. Its transport mirrors Python Agent SDK 0.2.166, initializes
  live children before prompting, acknowledges controls and settles foreground
  results from session state independently of background-task events. Preserve
  the CLI's native prompt/settings preset.

## Codex

`internal/platforms/codex` follows the same backend protocol as Claude, with
one machine-wide supervisor (keyed by `<data>/codex-supervisor`) hosting one
`codex app-server` for every thread. History, the model and command catalog
and the account use a separate read-only app-server, so browsing never
launches the supervisor. Token usage is the one value read from rollout files.
`codexprovider.Converter` is the only Codex-to-v1 converter. Tests use
`codextest` fakes and the recorded fixtures under
`codex/provider/testdata/<version>`, never the real `codex`; re-record with the
`codexrecord`-tagged recorder. Codex is opt-in: `-platforms` defaults exclude it.

## OpenCode v1 and v2

`internal/platforms/opencode/apiv2` reads `opencode --version` once. A later version change logs a
restart requirement. Each machine speaks only to its local OpenCode, allowing
mixed hub/remote versions. Tests pin `ocv2.SetInstalledV2` in package `TestMain`
and never run the real binary.

- The rest of ocman speaks v1. `ocapi.Auth.Transport` wraps clients with
  `ocv2.Wrap`, translating v1 routes/events into v2 `/api/*` calls.
- `ocv2.ConvertMessage` is the only v2-to-v1 message converter. HTTP, SSE and DB
  TEMP views in `internal/db/v2views.go` all use it. Preserve matching synthetic
  part IDs, `prt<msg suffix><index>`. Never introduce another converter.
- V2 uses one `opencode serve` per machine, keyed by effective `OPENCODE_DB`,
  under `~/.local/share/ocman/opencode-v2[-<db hash>]`. Discovery resolves all
  directories to that machine port.
- When `app.sharesServiceDB`, use OpenCode's registered background service via
  `Host.useService` and `ocv2.RunningService`, not another server migrating the
  same database. Read registration from `~/.local/state/opencode/service.json`
  and credentials from `~/.config/opencode/service.json`.
- Track it as `ocruntime.KindService`. Never persist, stop or restart it;
  return `ErrServiceNotManaged`. Wait for a busy service, never duplicate it.
  Re-read registration after failed probes. Keep an inconclusively probed
  launched server instead of killing and relaunching it.
- Otherwise generate the required password once and store it in state.db when
  none is configured. V2 follow-ups use the native inbox unless ocman's queue
  already contains messages. See [sessions.md](sessions.md).

## Runtime lifecycle

`EnsureProjectOpencode` on the owner's Host is the only project launch path.
`ocruntime.Runtime` abstracts `Launch`, `Probe` and `Stop`. Native hosting uses
`tmux -L ocman-agents -f /dev/null`, isolated from user tmux configuration.
V1 shares one project-rooted instance across worktrees with per-session working
directories; v2 uses the machine-wide instance described above.

- Allocate a free loopback port with `ocruntime.AllocateLoopbackPort`, then
  launch `opencode serve --port N`. Do not discover managed ports with `lsof`.
- Probe `GET {endpoint}/config` for 200, not merely an open listener. Collapse
  concurrent ensures per root with singleflight. Reuse healthy instances.
- Results expose `Endpoint`, `Port()`, `RepoRoot`, opaque `Instance` and
  `Launched`. Persist managed instances by canonical root in `managed_opencode`.
  Always re-probe on recovery. Discard dead rows; restart stops and relaunches
  under the same singleflight key, owner-routed locally or over gRPC.
- Gate managed launch UI on `HostCaps.OpencodeLaunch`, not `Tmux`.
- Runtime lookup uses only managed inventory, except v2's registered service.
  Never adopt external instances. Cache for ten seconds. Reserve `lsof` for
  opt-in maintenance and development port cleanup.
- `opencode.Discovery` is owned by each adapter, not globally. The server wires
  Host hooks to that adapter so backend reload starts with empty discovery.

Tmux path-derived names must pass `tmux.ValidComponent`, `[a-zA-Z0-9._/~-]+`.
It excludes `:` to prevent target misrouting. User targets use `ValidName`,
`[a-zA-Z0-9._/~:-]+`. Tmux displays dots as underscores. Invalid derived paths
fail launch; do not silently sanitize them into ambiguous targets.
