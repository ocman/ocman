# Repository map

Use this map to find owners, then read the relevant topic guide. It is not an
exhaustive file inventory. Architecture diagrams live in
`docs/other/architecture.md`; update them with changes to packages, dependencies,
adapter boundaries or browser/backend data flows.

## Entry points and application composition

| Path | Responsibility |
| --- | --- |
| `main.go`, `internal/app` | Embedded skills, build version, startup, flags, DB selection, auth and composition. `internal/app/run.go` and `ocman -h` define flags. |
| `internal/server` | HTTP/static serving, platform/Host wiring and feature registry in `modules.go`. |
| `internal/module`, `internal/httpx` | Module lifetime and typed bus; shared HTTP helpers. Read [backend.md](backend.md). |
| `internal/gui` | Wails window around the same HTTP server with `-gui`/`-gui-addr`. |
| `internal/toolpath` | Merge login-shell PATH at startup for launchd and tool shims. |
| `internal/webui/static` | Generated embedded frontend; keep tracked `robots.txt`. |

## Platforms, runtimes and host operations

| Path | Responsibility |
| --- | --- |
| `internal/platforms` | Platform interface, registry, capabilities and compound owner IDs. |
| `internal/platforms/opencode`, `internal/ocapi`, `internal/ocv2` | OpenCode DB/API adapter, host-local auth and v1/v2 compatibility. |
| `internal/platforms/claudecode`, `internal/claudeprovider`, `internal/claudecli` | Claude adapter, indexing/backend and shared child transport. |
| `internal/platforms/provider`, `internal/providerprotocol`, `internal/providersupervisor` | Generic socket adapter, internal DTOs and sequenced replay. Read [platforms.md](platforms.md). |
| `internal/hostsvc`, `internal/hostsvc/local`, `internal/remote` | Host router, local execution and remote gRPC. Regenerate proto stubs with `make proto`. |
| `internal/ocruntime`, `internal/tmux` | Runtime-neutral managed agents and tmux process control. |
| `internal/gitexec`, `internal/git`, `internal/gitmod` | Hardened git commands, queries and owner-routed git/worktree HTTP. |
| `internal/term`, `internal/termmod`, `internal/tmuxmod` | Owner-side terminal control and guarded HTTP/WebSocket routes. |
| `internal/whisper` | Local whisper-cpp transcription with ffmpeg conversion. |

Read [remotes-and-forges.md](remotes-and-forges.md) for ownership rules.

## Session and data services

| Path | Responsibility |
| --- | --- |
| `internal/sessionsvc`, `internal/sessionlifecycle` | Shared mutations, prepare/start, progress, attachments, direct commands, reload/restart. |
| `internal/sessionrecovery` | Workerless owner-local interruption history, replacement membership and admission/stop reconciliation. Queries stay in state; Claude supervisor recovery stays in its adapter. |
| `internal/sessiontitles` | Owner-local `FirstPromptAdded` naming. Claude uses a tool-free nonpersisted Haiku helper with native 2.1.289 title prompt and helper slots; `/auto-title` preserves custom/manual names atomically. No naming command enters the conversation. |
| `internal/queuesvc`, `internal/queuemod` | Queue domain and route/worker/native inbox coordination. |
| `internal/permissions`, `internal/autoapprove` | Inherited rules, judging, provenance and pending-prompt/watch lifetime. |
| `internal/snooze` | Snooze route and joined wake worker, checked on startup and every minute independently of auto-archive. Archive persistence stays in state. |
| `internal/db`, `internal/state` | Read-only OpenCode queries and writable ocman state. |
| `internal/statev2`, `internal/conversationingest` | Durable owner/instance-scoped session reads, conversation history, indexed accounting, ingestion readiness and read-your-writes acknowledgements. Canonical adapters never fall back to native history for persisted reads. |
| `internal/sidebar` | Compact session-list queries over the history projection, read-only catalogue/user-state joins and generation-scoped event-cache observations. No request-time accounting or platform RPCs. |
| `internal/analyticsdata` | Shared accounting metadata and tool-timing rules used by legacy queries and ingestion-maintained analytics summaries. |
| `internal/analytics`, `internal/pricing`, `internal/srvtiming`, `internal/telemetry` | Analytics, LiteLLM price cache/costs, request timing and optional OTLP. |
| `internal/ocmaint` | User-started OpenCode DB maintenance and launch gating. |
| `internal/tasksvc`, `internal/taskmod` | Bounded owner-local background work, durable task progress in state.db and read-only task APIs. History ingestion is the first producer. |

Read [sessions.md](sessions.md) and [integrations.md](integrations.md).

## Feature modules and plugins

| Path | Responsibility |
| --- | --- |
| `internal/projectforge`, `internal/forge` | Owner-routed PR/issue operations and GitHub/Forgejo clients. |
| `internal/factorymod`, `internal/factory` | Factory routes/tools/session launchers and domain dispatch. `factory_control` records proposals only; user confirmation executes them. Tables stay in state. State tests importing factory use `package state_test`. |
| `internal/plugins`, `internal/pluginsmod`, `sdk/plugin`, `examples/ocman-plugin-*` | Protocol, supervision, owner management, SDK, examples, durable conversation/session delivery, launch catalogs and permission profiles. Read [plugins.md](plugins.md) for table ownership. |
| `internal/routines`, `internal/webhook` | Scheduling/session settlement and webhook capture/subscriptions. Remote receives the narrow webhook interface, not a module import. |
| `internal/artifacts`, `internal/inbox`, `internal/sharing`, `internal/share` | Published files, cross-machine attention, exports and encrypted relay sharing. |
| `internal/filesystem`, `internal/previews`, `internal/linkpreview`, `internal/previewauth` | Read-only browsing, inert signed files, preview routes/service and grants. |
| `internal/mcp`, `internal/opencodeskills`, `internal/opencodeconfig` | MCP composition, embedded skill installation and safe config registration. |

Auto-approval owns its routes, judge settings, delay seeding, prompt resumption
and headless watcher. The server composes its dependencies in
`autoapprove_engine.go` and drives it from session paths. Inbox owns permission
notifications, resolution and reconciliation. See [integrations.md](integrations.md)
for feature safety constraints and [backend.md](backend.md) for module ownership.

## Frontend, documentation and deployment

- `frontend/` is the React/TypeScript/Vite SPA. Read [frontend.md](frontend.md).
- `docs/` contains all published documentation, organized as `introduction/`,
  `features/`, `configuration/`, `faq/` and `other/`. Chapters have `_index.md`;
  `title` and `weight` front matter drive navigation.
- `site/` contains Hugo/Hextra config, templates and landing page. `hugo.toml`
  mounts `docs/` read-only; do not create a second copy of its content.
- `spec/` holds detailed designs and acceptance criteria. Link to it rather
  than copying specs into agent instructions.
- `deploy/` holds Caddy, the relay Dockerfile and OTEL Compose config. Relay
  Docker builds use the repository root as context. Caddy uses `tailscale cert`
  and `OCMAN_CADDY_HOST`; telemetry volumes point to `observability/`.
