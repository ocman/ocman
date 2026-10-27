# Platform implementations

Platform-specific code lives under its platform directory. The public platform
IDs are `claudecode`, `codex` and `opencode`; directory names do not change
persisted identities, commands or wire formats.

## Claude

- `claude/transport` composes the browsing and project supervisors, routes native
  sessions to their owning socket, and handles runtime recovery and history
  ingestion. It uses the shared `platforms/provider` socket adapter.
- `claude/provider` implements the Claude backend: child processes, transcript
  indexing, native controls and conversion into ocman's event format.
- `claude/launch` shares the child environment between the backend and helper
  callers such as the auto-approval judge.

The transport still owns existing routing and recovery orchestration. It is not
just a forwarding client. The generic socket client, protocol and event replay
remain in `platforms/provider`, `providerprotocol` and `providersupervisor`.

## Codex

- `codex/appserver` is the JSON-RPC client for `codex app-server` over stdio,
  with an in-process fake (`codextest`) and a manual fixture recorder behind the
  `codexrecord` build tag.
- `codex/provider` implements the Codex backend: the single Codex-to-v1
  converter shared by stored history and live items, the history reader, and
  the supervisor that drives threads on one app-server.
- `codex/transport` composes the platform: one machine-wide supervisor under
  tmux (`ocman provider codex`), launched on first use and replaced when it
  dies, plus a separate read-only app-server for history and the model catalog.

Codex history is read through `thread/list`, `thread/turns/list` and
`thread/items/list`. Only per-turn token usage, which the app-server does not
serve, is read from the thread's rollout file (`token_usage_record` lines,
plain or zstd-compressed).

## OpenCode

- `opencode/provider` implements platform operations, native history ingestion,
  runtime discovery and live observation.
- `opencode/api` owns shared authentication and HTTP transport composition.
- `opencode/apiv1` implements bounded HTTP operations and error decoding for the
  v1-shaped requests used internally.
- `opencode/apiv2` translates those requests and their responses/events for v2
  servers. It also owns v2 version detection and registered-service discovery.

The v2 implementation remains a compatibility transport, not a second standalone
client. The provider's authenticated clients select it transparently.

Shared runtime launch/probe/stop operations stay in `ocruntime` and the owning
`hostsvc.Host`. Application browsing reads stay in the canonical history store;
moving packages does not introduce native-storage fallbacks.

Go package identifiers are retained during the directory reorganization. For
example, `opencode/api` declares `package ocapi` and `claude/provider` declares
`package claudeprovider`. See the
[architecture diagram](../../docs/other/architecture.md#platform-package-dependencies)
for dependency directions.
