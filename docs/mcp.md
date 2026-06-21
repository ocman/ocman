# MCP server (agent integration)

Ocman embeds an optional **MCP (Model Context Protocol)** server so an AI
coding agent — or you, through the agent — can split work from an active
session into new parallel sessions or isolated git worktrees, and
coordinate between them.

Ocman works fine as a plain dashboard without this. Install it only if
you want agent-driven session splitting.

## Endpoint

The server uses the Streamable HTTP transport at:

- `http://localhost:8229/mcp` — the production binary (Go backend).
- `http://localhost:8228/mcp` — during development (`make dev`); the Vite
  dev server proxies `/mcp` to the backend on `:8229`.

The current URL is also exposed via `/api/capabilities` as
`mcpServer.url`. The endpoint is **localhost-only**.

## Setup

Add the server to your project's `opencode.json` (or the global
`~/.config/opencode/config.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ocman": {
      "type": "remote",
      "url": "http://localhost:8228/mcp",
      "enabled": true
    }
  }
}
```

Use `http://localhost:8229/mcp` when running the production binary
directly (no Vite dev proxy).

## Tools

| Tool | Description |
|------|-------------|
| `split_to_session` | Launch a new OpenCode session in the same directory with a context-enriched prompt. |
| `split_to_worktree` | Launch a new OpenCode session in a fresh git worktree. |
| `get_current_session_id` | Return the most recently updated OpenCode session ID known to ocman, optionally filtered by project directory. |
| `get_session_status` | Check the status of a previously spawned child session. |
| `list_child_sessions` | List all child sessions spawned from a parent session. |
| `cancel_session` | Cancel a running child session (kills its tmux window). |
| `send_message_to_child` | Send a message from a parent session to one of its child sessions. |
| `send_message_to_parent` | Send a message from a child session back to its parent session. |

## How splitting works

1. The model (or you, via the agent) calls `split_to_session` or
   `split_to_worktree` with a brief `intent` and the current
   `session_id`.
2. Ocman gathers context from the parent session's directory — the last
   10 messages, the current git branch, and `git diff --stat`.
3. A structured Markdown prompt is assembled and sent to a new OpenCode
   session.
4. A background watcher polls the child session; when it completes,
   ocman injects a result summary back into the parent session.

## Splitting skill (optional)

Ocman ships an OpenCode **skill** that gives the agent better heuristics
for *when and how* to split work, so the MCP tool descriptions can stay
short and action-focused:

```text
.opencode/skills/ocman-session-splitting/SKILL.md
```

When working inside this repository, OpenCode loads the skill from the
project config automatically (after a restart). To use the same guidance
in another project, copy that folder into the target project's
`.opencode/skills/` directory, or add this repository's
`.opencode/skills` path to that project's OpenCode `skills.paths` config.
