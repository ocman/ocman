# Ocman Architecture

## Introduction

Ocman is a single Go binary that serves a React SPA and acts as a
control plane for coding-agent sessions. Four diagrams below: (1)
system context — what ocman talks to, (2) backend composition — the Go
packages, (3) the session/event data flow, (4) frontend composition.
Each diagram is capped at ~10 blocks; detail lives in the text.

## 1. System Context

Everything external that the ocman process touches.

```mermaid
flowchart LR
    Browser[Browser SPA<br/>REST + SSE] --> Ocman[ocman<br/>Go binary :8229]
    Agent[AI agents<br/>MCP clients] -->|/mcp| Ocman
    Ocman -->|read-only SQLite| OCDB[(opencode.db)]
    Ocman -->|read/write SQLite| StateDB[(state.db)]
    Ocman -->|HTTP proxy| OCInst[Running OpenCode<br/>instances]
    Ocman -->|exec| Shell[git / tmux / lsof<br/>worktrees]
    Ocman -->|REST| Forges[GitHub / Forgejo]
    Ocman <-->|gRPC + token| Remotes[Remote ocman<br/>instances]
    Ocman -.->|OTLP, optional| Otel[Telemetry collector]
```

- **Browser SPA** — the only UI; talks REST/SSE to the hub, never to
  remotes directly.
- **opencode.db** — foreign data, opened read-only; ocman never writes
  to it.
- **state.db** — ocman's own state: archive flags, child sessions,
  loops, immutable workflow versions/runs, settings, and remote tokens.
- **Remote ocman instances** — the hub dials remotes over gRPC and
  re-exposes their sessions/hosts transparently.

## 2. Backend Composition

The Go package graph, collapsed to the seams that matter.

```mermaid
flowchart TD
    Server[internal/server<br/>HTTP, SSE, handlers] --> Registry[platforms.Registry<br/>session seam]
    Server --> Router[hostsvc.Router<br/>host/dir seam]
    Server --> Loops[loops.Service<br/>trigger→action engine]
    Server --> Workflows[workflows.Service<br/>durable DAG lifecycle]
    Workflows --> Registry
    Workflows --> Router
    Server --> MCP[internal/mcp<br/>MCP tools]
    Registry --> OC[platforms/opencode<br/>adapter]
    Registry --> RP[remote.Platform<br/>gRPC-backed]
    OC --> DB[internal/db<br/>read-only queries]
    Router --> Local[hostsvc/local<br/>git, tmux, worktree]
    Server --> State[internal/state<br/>state.db]
    Server --> Forge[forge + integrations<br/>GitHub/Forgejo clients]
```

- **internal/server** — HTTP mux, SSE broadcast/fanout, ~60 handler
  files, plus tmux, terminal, whisper, auto-approve, and loop/workflow ticks.
- **platforms.Registry** — session-scoped seam. One adapter per
  platform; remotes register as compound-ID platforms so handlers
  can't tell local from remote.
- **hostsvc.Router** — directory-scoped seam (git, worktrees, tmux,
  projects); resolves the owning host and delegates. Same transparency
  trick as the registry. Worktree sessions run **in-app on the
  project's single opencode instance** (one per project, ensured via
  `EnsureProjectOpencode`) with a per-session working directory — there
  is no per-worktree opencode/tmux process.
- **platforms/opencode** — wraps the read-only DB queries
  (`internal/db`) plus an HTTP client to live instances, with
  `lsof`-based port discovery.
- **loops.Service** — pure domain logic behind small interfaces;
  driven by a 5 s tick goroutine in the server; shared by REST and
  MCP.
- **workflows.Service** — shared validation and run-lifecycle seam for
  immutable workflow versions. It schedules approval, permission-scoped
  command, and agent nodes from persisted dependency state. The command
  executor owns directory/environment policy, bounded logs, collectors, and
  process-tree cancellation; agent attempts create/send/abort through the
  session service, poll platform-neutral session status, and collect declared
  messages, diffs, or files through platform/host seams. REST and SSE never
  implement independent transitions.
- **internal/mcp** — prompt composer + session launcher + tool
  handlers; all side effects go through the same `Platform` interface
  the HTTP layer uses.
- **internal/state** — the only writable store; migrations, settings,
  loops, child sessions.
- **forge / integrations** — forge-agnostic types in `forge`,
  per-forge HTTP clients in `integrations/{github,forgejo}`.

## 3. Session & Event Data Flow

How a session read and a live update travel through the system.

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
    A->>D: SQL json_extract / HTTP proxy
    D-->>B: JSON (status inferred at query time)
    Note over S,E: background: loop/workflow engine ticks,<br/>child-session watcher, remote gRPC streams
    E-->>B: SSE (session.updated, loop.updated, workflow.run.updated)
```

Key property: status is *inferred*, not stored — ocman derives session
state from the last message row on every query, so there is no sync
problem with OpenCode's DB.

Workflow state takes the opposite path because it is ocman-owned: publish,
start, approval, command completion, agent supervision, pause, and cancel
delegate to `workflows.Service`, which commits version/run/node/attempt state
and bounded command/collector output to `state.db` before broadcasting a run
ID. The browser then refetches the authoritative run graph, including linked
agent sessions and collector output.

## 4. Frontend Composition

```mermaid
flowchart TD
    Pages[pages/<br/>routes] --> Comp[components/<br/>~80 components]
    Pages --> Stores[Client state<br/>sessions, loops, workflows]
    Comp --> Stores
    Stores --> API[lib/ API client]
    Stores --> SSE[SSE subscription]
    API -->|/api| Hub[ocman backend]
    SSE -->|events| Hub
    Comp --> Caps[useCapabilities<br/>capability gating]
```

- **Capability gating** — the UI never branches on platform identity;
  features toggle via `/api/capabilities` (enforced by a lint script).
- **Client state** — shared Zustand stores hold broad session/loop state;
  the bounded workflow page keeps its selected run locally and reconciles
  it from REST whenever the shared SSE stream reports a run change.
