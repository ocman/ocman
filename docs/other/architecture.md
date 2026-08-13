---
title: Architecture
weight: 6
---

## Introduction

Ocman is a single Go binary that serves a React SPA and acts as a
control plane for coding-agent sessions. Four diagrams below: (1)
system context — what ocman talks to, (2) backend composition — the Go
packages, (3) the session/event data flow, (4) frontend composition.
Each diagram is capped at ~10 blocks; detail lives in the text.

### Cross-machine conversation sharing

Ocman can publish a shared conversation through the standalone
`ocman-relay` binary. The relay stores only AES-256-GCM ciphertext through
the `share.Store` abstraction (disk today; object storage can implement the
same `Put`/`Get`/`List`/`DeletePrefix` contract). The per-share key remains
in the URL fragment and is therefore never sent to the relay.

```mermaid
flowchart LR
    Owner[Owner ocman] -->|sealed completed turns| Relay[ocman-relay]
    Relay --> Store[(share.Store)]
    Viewer[Browser viewer] -->|ciphertext poll| Relay
    Viewer -->|WebCrypto decrypt| Thread[Shared conversation]
    Thread -->|explicit local fork| Local[Recipient ocman]
```

- Chunk zero is a complete snapshot; later chunks contain the latest
  completed turn and are merged as id-keyed upserts by the viewer.
- The owner allocates sequence numbers. Nonces derive from those sequence
  numbers and the share id plus sequence is authenticated as GCM AAD.
- Revocation deletes the relay prefix. The relay stores only a hash of its
  append/delete token and requires no database.
- Forking fetches and decrypts in the recipient's authenticated local ocman
  UI, lets the recipient choose a local project, and parks the transcript as
  an unsent composer draft. Imported text is never executed automatically.

## 1. System Context

Everything external that the ocman process touches.

```mermaid
flowchart LR
    Browser[Browser SPA<br/>REST + SSE] --> Ocman[ocman<br/>Go binary :8229]
    Agent[AI agents<br/>MCP clients] -->|/mcp| Ocman
    Ocman -->|read-only SQLite| OCDB[(opencode.db)]
    Ocman -->|read/write SQLite| StateDB[(state.db)]
    Ocman -->|artifact payloads| Blobs[(workflow-artifacts)]
    Ocman -->|Authenticated HTTP/SSE proxy| OCInst[Running OpenCode<br/>instances]
    Ocman -->|exec + loopback REST| Shell[git / tmux / lsof / bd / Dagu<br/>host tools]
    Ocman -->|REST| Forges[GitHub / Forgejo]
    Ocman <-->|gRPC + token| Remotes[Remote ocman<br/>instances]
    Ocman -.->|OTLP, optional| Otel[Telemetry collector]
```

- **Browser SPA** — the only UI; talks REST/SSE to the hub, never to
  remotes directly.
- **opencode.db** — foreign data, opened read-only; ocman never writes
  to it.
 - **state.db** — ocman's own state: archive flags, child sessions,
    immutable workflow versions/runs, workflow artifact metadata,
   workflow resource/workspace leases, settings, and remote tokens.
- **workflow-artifacts/** — content-addressed store (under the ocman
  data dir, next to state.db) holding large, deduplicated, immutable
  artifact payloads out of SQLite; metadata rows in state.db reference
  payloads by content hash and expire them on a retention policy while
  keeping the audit metadata.
- **Remote ocman instances** — the hub dials remotes over gRPC and
  re-exposes their sessions/hosts transparently.
- **Dagu** — a separately installed 2.x CLI, and the workflow runner. On
  the first workflow action the owning ocman host starts one private
  loopback-only Dagu server under `~/.local/share/ocman/dagu`. Ocman owns
  authoring, versioning, triggering, and history; Dagu only executes the
  graph. Ocman compiles a pinned version to an inline spec, posts it under
  ocman's own run id, and polls run state back. Compiled specs carry no
  schedule and no auto-retry, and the process receives no LLM credentials.

## 2. Backend Composition

The Go package graph, collapsed to the seams that matter.

```mermaid
flowchart TD
    Server[internal/server<br/>HTTP, SSE, handlers] --> Registry[platforms.Registry<br/>session seam]
    Server --> Router[hostsvc.Router<br/>host/dir seam]
    Server --> Workflows[automation services<br/>workflows + prompt schedules]
    Workflows --> Registry
    Workflows --> Router
    Server --> MCP[internal/mcp<br/>MCP tools]
    MCP --> Registry
    MCP --> Router
    Registry --> OC[platforms/opencode<br/>adapter]
    Registry --> RP[remote.Platform<br/>gRPC-backed]
    OC --> DB[internal/db<br/>read-only queries]
    Router --> Local[hostsvc/local<br/>git, tmux, worktree, Beads]
    Local --> HostTools[host integrations<br/>ocruntime + Dagu manager]
    Server --> State[internal/state<br/>state.db]
    Server --> Forge[forge + integrations<br/>GitHub/Forgejo clients]
```

- **internal/server** — HTTP mux, SSE broadcast/fanout, ~60 handler
   files, plus tmux, terminal, whisper, auto-approve, and workflow ticks.
- **platforms.Registry** — session-scoped seam. One adapter per
  platform; remotes register as compound-ID platforms so handlers
  can't tell local from remote.
- **hostsvc.Router** — directory-scoped seam (git, worktrees, tmux, Beads,
  projects); resolves the owning host and delegates. Same transparency
  trick as the registry. Worktree sessions run **in-app on the
  project's single opencode instance** (one per project, ensured via
  `EnsureProjectOpencode`) with a per-session working directory — there
  is no per-worktree opencode/tmux process. `EnsureProjectOpencodeResult`
  is runtime-neutral: callers use the full `Endpoint` URL (or its
  `Port()`) plus an opaque `ocruntime.Instance`; the owning host may use
  discovery once to adopt a healthy instance started before its managed
  registry entry existed.
  `RestartProjectOpencode` stops and relaunches the tracked instance.
- **internal/ocruntime** — the runtime abstraction behind the managed
  launch path. A `Runtime` interface (`Launch`/`Probe`/`Stop`) hides
  *how* a project's opencode is hosted; the native-tmux implementation
  runs `opencode --port N` on an ocman-allocated loopback port and
  probes authenticated `GET {endpoint}/config` for health. It is the plug point for
  the container runtime (epic #375) as a second implementation.
- **internal/dagu** — detects the executable, supervises one lazy private
  Dagu server, and compiles a workflow version to a Dagu spec. The compiler
  covers command, agent, approval, map, and join nodes plus conditional
  edges; a map fans out through `dag.run` over a pinned child DAG written to
  the Dagu DAGs directory. `hostsvc.Host` and the remote gRPC seam keep
  launch, observation, logs, and cancellation on the owning machine.
- **internal/workflowstep** — `ocman workflow-step`, the command Dagu runs
  for node types it cannot express. Agent, approval, and conditional steps
  call back into ocman over loopback; a join applies its policy locally.
  The real node configuration stays in ocman, so prompts and credentials
  never reach a spec or a Dagu step log.
- **platforms/opencode** — wraps the read-only DB queries
  (`internal/db`) plus an HTTP client that attaches to live instances,
  with `lsof`-based discovery for instances started outside ocman. One
  process-wide `/global/event` stream per instance keeps pending permission
  and question state in memory across all session directories.
- **workflows.Service** — shared validation, durable trigger, and
  run-lifecycle seam for immutable workflow versions. Durable
  manual/interval/cron/PR/completion triggers create version-pinned runs
   (overlap skip/queue/parallel); a 5 s server tick evaluates triggers
   through narrow forge and session-status adapters. It schedules approval,
  permission-scoped command, and agent nodes from persisted dependency
   state. The command executor owns directory/environment policy, bounded
   logs, JSON stdout validation, and process-tree cancellation; agent attempts
   create/send/abort through the session service and poll platform-neutral
   session status. Agents with an `outputSchema` receive that JSON Schema in
   their prompt and have their final value validated by `jsonschema`; agents
   without one only need to complete successfully. REST, MCP, and SSE
  never implement independent transitions. Every node exposes one canonical
  Node Result; dependency-scoped interpolation passes its JSON output to
  commands, environments, prompts, maps, and CEL policies. The auxiliary
  content-addressed `BlobStore` remains for internal map-item payloads and
  historical artifact downloads, not as a second node-output channel. Referenced
   secrets are resolved from host env at execution time and redacted from
   logs and artifact payloads; a background sweep drops payloads past
   their retention window (30-day default, per-workflow override) but
   keeps the metadata. A run may own a bounded pool of worktree shards
   (created host-locally through the git worktree service); mutating nodes
   acquire a durable workspace lease in the same transaction as pool
   capacity — exclusive by default, or path-scoped so disjoint declared
   scopes share a shard while overlapping (ancestor/exact) scopes cannot.
   Path-leased nodes are denied repository-wide git mutation
   (stash/reset/checkout/commit/push …) so a commit coordinator owns
   serialized per-shard git state. Leases carry an optional owning-host
   identity, release only after the attempt settles, and are visible in
   the run UI.
 - **server prompt-schedule service** — the native prompt scheduler. It calculates
   one-time, interval, and five-field cron occurrences with `robfig/cron`, then
   atomically claims due `state.db` rows before sending the stored prompt through
   `sessionsvc`. Each occurrence either creates a fresh OpenCode session or queues
   into the schedule-owned session. On restart, persisted `running` claims become
   failed rather than being replayed, preventing duplicate external dispatch when
   completion is unknown.
- **Legacy loop migration (#325)** — migration v28 copies persisted legacy
  loops into ordinary one-node workflow definitions and turns each iteration
  into a historical workflow run + node attempt. `loop_workflow_map` makes
  the transaction-safe copy idempotent. The legacy tables are retained only
  as non-destructive upgrade input and historical data; no API, MCP, UI, or
  runtime scheduler reads them.
- **internal/mcp** — prompt composer + session launcher + tool
  handlers; all side effects go through the same `Platform` interface
  the HTTP layer uses, and every *host* operation (worktree creation,
  git prompt context, tmux kill) through owner-routed adapters the
  server injects over `hostsvc.Router.ForDir` — the package imports
  neither `git` nor `tmux`.
- **internal/opencodeskills** — extracts binary-embedded ocman skills into
  XDG data and installs only ocman-owned symlinks for OpenCode discovery.
- **internal/state** — the only writable store; migrations, settings,
  workflows, prompt schedules, child sessions.
- **forge / integrations** — forge-agnostic types in `forge`,
  per-forge HTTP clients in `integrations/{github,forgejo}`.

## 3. Session & Event Data Flow

How a session read and a live update travel through the system. Workflow runs
use the same SSE channel: discovery publishes a JSON Node Result, map creates
pinned child runs from it, item phases fan out to independent reviews, then fix,
validation, and the serialized commit coordinator settle before the run view
refetches phases, Node Results, historical artifacts, pools, and leases.

```mermaid
sequenceDiagram
    participant B as Browser
    participant S as server handlers
    participant R as Registry/Router
    participant A as opencode adapter
    participant D as opencode.db / OC HTTP
    participant E as SSE broadcast

    B->>S: GET /api/sessions
    S->>R: resolve platform/host
    R->>A: ListSessions()
    D-->>A: background /global/event prompt updates
    A->>D: SQL json_extract
    A->>A: overlay pending prompt registry
    D-->>B: JSON (status inferred at query time)
     Note over S,E: background: workflow + prompt-schedule ticks,<br/>child-session watcher, remote gRPC streams
     E-->>B: SSE (session.updated, workflow.run.updated)
```

Key property: status is *inferred*, not stored — ocman derives session
state from the last message row on every query, so there is no sync
problem with OpenCode's DB.

Workflow state takes the opposite path because it is ocman-owned: publish,
trigger/manual start, approval, command completion, agent supervision,
pause, and cancel delegate to `workflows.Service`, which commits
trigger/version/run/node/attempt state and canonical JSON Node Results to
`state.db` before broadcasting a run ID. A newly created run is offered to
the `workflows.ExternalRunner` seam first: Dagu takes every definition the
compiler can express, and the native dispatcher keeps the rest. Runs Dagu
executes reach the UI through a one-way mirror that polls Dagu and projects
run, node, and attempt rows back onto `state.db`, so the API and run view
are identical either way. The browser then refetches the
authoritative trigger state and run graph, including linked agent sessions and
map/join aggregate outputs.

One-time prompt schedules are also ocman-owned. The project page creates and
lists them over REST; the scheduler first commits a durable `running` claim,
then creates and prompts a session through the shared session mutation service.
The same row stores completion/error state and the session link shown by the UI.

## 4. Frontend Composition

```mermaid
flowchart TD
    Pages[pages/<br/>routes] --> Comp[components/<br/>~80 components]
     Pages --> Stores[Client state<br/>sessions, workflows]
    Comp --> Stores
    Stores --> API[lib/ API client]
    Stores --> SSE[SSE subscription]
    API -->|/api| Hub[ocman backend]
    SSE -->|events| Hub
    Comp --> Caps[useCapabilities<br/>capability gating]
```

- **Capability gating** — the UI never branches on platform identity;
  features toggle via `/api/capabilities` (enforced by a lint script).
- **Client state** — shared Zustand stores hold broad session/workflow state;
  the bounded workflow page keeps its selected run locally and reconciles
  it from REST whenever the shared SSE stream reports a run change. Its run
  graph labels ordered phases, stable map items, attempts, Node Results,
  historical artifacts, resource pools, and workspace ownership without
   inferring platform identity.
- **Beads status** — the right panel queries the repository owner's
  `hostsvc.Host` through `/api/project/beads-status`; remote owners proxy the
  same operation over gRPC. Ticket data stays in the repository and is polled
  only while the available pane is open.
