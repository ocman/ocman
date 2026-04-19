# Codex Adapter — Paper Design

**Status**: paper design only (SC-6). Not merged. Serves as a sanity
check that the `Platform` interface holds up for a third coding-agent
platform.

## What is Codex CLI

[OpenAI Codex CLI](https://github.com/openai/codex) is a terminal
coding agent backed by OpenAI's API. It stores sessions as JSONL
"rollout" files on disk and exposes a local JSON-RPC app-server for
live interaction. As of `rust-v0.75.0` the relevant surfaces are:

| Surface | Path / endpoint | Notes |
|---------|----------------|-------|
| Thread rollout files | `~/.codex/threads/<thread-id>.jsonl` (inferred) | Persisted per-thread JSONL; archived via `thread/archive` |
| Cross-session history | `~/.codex/history.jsonl` | Text-only, cross-session; not per-turn |
| App-server JSON-RPC | `localhost:<port>` (auto-discovered) | `thread/list`, `thread/read`, `thread/start`, `turn/submit` |
| Hooks | `SessionStart`, `Stop`, tool events | Same hook model as Claude Code; fired by the CLI |

> **Unknown 1**: The exact on-disk path for rollout files is not
> publicly documented. The app-server README refers to "a JSONL file
> on disk" but does not specify the directory. Investigation needed
> before real work.
>
> **Unknown 2**: The per-turn event schema inside the rollout JSONL
> is not documented. The app-server `thread/read` endpoint likely
> returns the same events; using that as the read path avoids
> reverse-engineering the file format.

## Adapter design

### Package

`internal/platforms/codex/` — mirrors the `claudecode` package
structure.

### `Adapter` struct

```go
type Adapter struct {
    // threadsDir is the absolute path to ~/.codex/threads (or
    // equivalent). Set by New(); overridable by NewFromDir for tests.
    threadsDir string

    // appServer is the JSON-RPC client for the running Codex
    // app-server. nil when no server is reachable.
    appServer *appServerClient

    // cache memoises parsed thread files by (path, mtime, size),
    // same pattern as the Claude Code adapter.
    cache *cache

    // live is the in-memory live-state cache driven by hook events.
    // Same design as claudecode.liveCache.
    live *liveCache
}
```

### `Platform` interface mapping

| Method | Implementation |
|--------|---------------|
| `ID()` | `"codex"` |
| `DisplayName()` | `"Codex"` |
| `Available(ctx)` | `threadsDir` exists AND (`~/.codex/history.jsonl` exists OR app-server reachable) |
| `Capabilities()` | `Composer: true` (via `turn/submit`), rest `false` initially |
| `Sessions(ctx, dir, since)` | Walk `threadsDir/*.jsonl`, parse head via `thread/read` or direct JSONL parse, apply live overlay |
| `Session(ctx, id, limit, offset)` | `thread/read` JSON-RPC call; fall back to direct JSONL parse |
| `SendMessage(ctx, req)` | `turn/submit` JSON-RPC; refuse if `liveCache` reports `busy` (AD-13 pattern) |
| `SessionsInactiveBefore(ctx, cutoff)` | Walk threads, filter by `updated_at` |
| `LiveStatus(id)` | `liveCache.Get(id)` |
| `AgentCatalog`, `SlashCommands`, etc. | `ErrUnsupported` initially |

### Read path

Prefer the app-server `thread/read` endpoint when a server is
reachable — it returns structured events without requiring us to
reverse-engineer the rollout JSONL schema. Fall back to direct JSONL
parse (same mtime-keyed cache as Claude Code) when no server is
running.

The `thread/list` endpoint returns `{ data: [{ id, title, updated_at,
cwd, ... }] }` — enough to build `db.Session` summaries without
reading individual files. Use this for `Sessions()` when the server
is up; fall back to the file walk otherwise.

### Live state

Codex fires `SessionStart` hooks (with `source: "startup" | "clear"`)
and tool-use events. The hook payload schema is similar to Claude
Code's. Install a managed block into `~/.codex/config.toml` (Codex's
config file) on startup, using the same sentinel pattern as the Claude
Code adapter (`_owner = "ocman"`). Hook events POST to
`/api/hooks/codex` on loopback.

A new `hooks_codex.go` handler in `internal/server/` mirrors
`hooks_claude.go`. The `liveCache` type from `claudecode` can be
copied verbatim (or extracted to `internal/platforms/livecache.go` as
a shared helper — see "Shared helpers" below).

### Composer

`turn/submit` JSON-RPC sends a new user turn to a running Codex
thread. The `response_id` from the previous `TurnComplete` event is
passed as the resume bookmark. Refuse while `liveCache` reports
`busy` (AD-13).

When no app-server is running, the composer falls back to
`codex exec --thread <id> "<message>"` (if such a CLI flag exists —
**Unknown 2** applies here too). If neither path is available,
`SendMessage` returns `ErrUnsupported` for that session.

### Port discovery

Codex's app-server listens on a local port. Discovery mirrors the
OpenCode approach: `lsof -nP -iTCP -sTCP:LISTEN` filtered to
processes named `codex`, then resolve cwd. Cache with a 3 s TTL.

## Shared helpers — refactor opportunity

Implementing the Codex adapter would expose three pieces of code
duplicated across `claudecode` and `codex`:

| Helper | Current location | Proposed shared location |
|--------|-----------------|--------------------------|
| `liveCache` | `claudecode/live_cache.go` | `internal/platforms/livecache/` |
| `mtime-keyed file cache` | `claudecode/cache.go` | `internal/platforms/filecache/` |
| Hook install / sentinel merge | `claudecode/install.go` | `internal/platforms/hookinstall/` |

These refactors are not required before the Codex adapter ships — the
duplication is small and the packages are already well-tested — but
they would reduce maintenance surface if a fourth platform arrives.

## What the interface does NOT need to change

- `Platform` interface: no new methods required. All Codex operations
  map cleanly to existing methods.
- `Registry`: no changes. `registry.Register(codex.New())` is the
  only addition to `main.go`.
- `state.db`: no schema changes. The `(platform, session_id)` PK
  already scopes state per platform; `platform = "codex"` just works.
- Frontend: no changes. The platform-agnostic capability-gating
  approach means the UI adapts automatically once the adapter
  declares its capabilities.
- `scripts/check-platform-branching.sh`: no changes needed; the
  script already blocks any new `platform === 'codex'` branches.

## Unresolved unknowns

1. **Rollout file path**: `~/.codex/threads/` is inferred from the
   app-server README's "JSONL file on disk" language. Needs
   confirmation by running Codex and inspecting `~/.codex/`.
2. **Rollout JSONL schema**: the per-event structure inside the
   rollout file is not documented. Using `thread/read` as the primary
   read path sidesteps this, but a direct-parse fallback needs the
   schema. Likely similar to the `turn/item` SSE events from the
   TypeScript SDK (`item.completed`, `turn.completed`, etc.).
3. **Hook config path**: Codex uses `~/.codex/config.toml` (TOML, not
   JSON). The hook install logic needs a TOML read/write path instead
   of the JSON merge used for Claude Code. A minimal TOML library
   (e.g. `github.com/BurntSushi/toml`) would be needed, or the hooks
   block could be appended as a raw TOML string with a sentinel
   comment.
4. **`codex exec` CLI flag**: whether a `--thread` / `--resume` flag
   exists for non-interactive prompt injection is not confirmed.
   The app-server `turn/submit` is the preferred path; the CLI
   fallback is a nice-to-have.

## Effort estimate

| Phase | Work | Estimate |
|-------|------|----------|
| Investigate unknowns 1–4 | Read source + run Codex | 0.5 d |
| Adapter scaffold + tests | Mirror `claudecode` structure | 1 d |
| Read path (app-server + JSONL fallback) | Parser + cache | 1.5 d |
| Live state (hooks + install) | Mirror `claudecode` hooks | 1 d |
| Composer (`turn/submit`) | JSON-RPC client | 0.5 d |
| Server wiring + integration tests | New handler + registry entry | 0.5 d |
| **Total** | | **~5 d** |

This is consistent with the Claude Code adapter effort (Phases 4–6,
~5 d of focused work).
