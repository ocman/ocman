---
title: MCP server
weight: 3
---

Ocman embeds an optional MCP (Model Context Protocol) server so an AI coding
agent, or you through the agent, can split work from an active session into
new parallel sessions or isolated git worktrees and coordinate between them.

Ocman works fine as a plain dashboard without this. Install it only if
you want agent-driven session splitting.

## Endpoint

The server uses the Streamable HTTP transport and listens on its own
loopback-only port, separate from the web UI:

- `http://127.0.0.1:8227/mcp` is the dedicated MCP listener (`-mcp-addr`).
  It works the same in dev and production, and needs no credentials.
- `http://localhost:8228/mcp` (dev) or `http://localhost:8229/mcp`
  (production binary) is the same endpoint on the web UI's port. Password auth
  applies there, so a native MCP client gets `403` when auth is configured.

`/api/capabilities` also reports the recommended URL as `mcpServer.url`. Both
paths are localhost-only. Origin-less native MCP clients work,
cross-origin browser requests are rejected.

The dedicated listener exists because MCP clients cannot present an auth
cookie, so it has to treat the loopback peer address as the credential. That
is unsafe on the web UI's port, where every request forwarded by a reverse
proxy arrives from `127.0.0.1`. Binding a separate loopback-only listener
keeps it out of reach of a proxy pointed at the main port. Ocman refuses to
bind `-mcp-addr` to a non-loopback address, and `-mcp-addr ""` disables the
dedicated listener entirely.

## Setup

Ocman checks OpenCode's global config on every page load and, if it doesn't
find its own entry, shows a toast offering to install it. Clicking **Install**
writes the entry below into `~/.config/opencode/opencode.json`
(`$XDG_CONFIG_HOME` and `OPENCODE_CONFIG` are honoured), after copying the
original to `opencode.<timestamp>-backup.json` in the same directory. Every
other key in the file survives, including any other MCP servers. Restart
OpenCode afterwards, since it reads the config at startup.

Ocman won't touch a config it can't rewrite losslessly, meaning a `.jsonc`
file or a `.json` file with comments. The toast then shows the URL to paste in
yourself. It also offers to update a stale entry, say one still pointing at an
older port. `GET /api/mcp/config` returns the same information.

To do it by hand, add the server to your project's `opencode.json` or the
global config:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ocman": {
      "type": "remote",
      "url": "http://127.0.0.1:8227/mcp",
      "enabled": true
    }
  }
}
```

The MCP port is fixed, so this config works for both `make dev` and the
production binary. Change it if you moved the listener with `-mcp-addr`.

## Tools

| Tool | Description |
|------|-------------|
| `new_session` | Run a new OpenCode child session with a context-enriched prompt. It waits for the terminal result by default; set `wait=false` to return the child ID immediately and deliver the final response to the parent asynchronously. Child sessions cannot create further children. Shares the parent's directory by default; set `worktree=true` (with a `branch`) to run it in a fresh git worktree. Accepts an optional `model` (`"provider/model"`) for the child; when omitted the child inherits the parent session's current model. |
| `await_session_result` | Explicitly wait for an asynchronous child or reconnect to a disconnected result wait without sending another prompt. A child ID is optional when the parent has exactly one disconnected child. |
| `get_current_session_id` | Return the most recently updated OpenCode session ID known to ocman, optionally filtered by project directory. |
| `get_session_status` | Check the status of a previously spawned child session. |
| `list_child_sessions` | List all child sessions spawned from a parent session. |
| `cancel_session` | Cancel a running child session (kills its tmux window). |
| `send_message_to_child` | Send a message from a parent session to one of its child sessions. Returns immediately by default and delivers the completed turn to the parent asynchronously; set `wait=true` to return the response directly. |
| `send_message_to_parent` | Send a message from a child session back to its parent session. |
| `embed_file` | Make a file on disk viewable to the user in the ocman UI. Takes an absolute `path` (plus an optional `label`) and returns a signed URL and a markdown snippet the agent pastes into its reply. Images and SVGs render inline in the conversation; PDFs and other types open or download in the browser. See [Embedding generated assets](#embedding-generated-assets). |
| `get_workflow_schema` | Get the workflow definition schema and a minimal valid JSON example. |
| `validate_workflow` / `publish_workflow` / `list_workflows` | Validate, publish immutable versions, and list workflows. |
| `start_workflow` / `list_workflow_runs` / `inspect_workflow_run` | Start a pinned or active version and inspect compact run state. |
| `pause_workflow_run` / `resume_workflow_run` / `cancel_workflow_run` | Control workflow run scheduling and cancellation. |
| `approve_workflow_node` / `resolve_unknown_attempt` | Approve waiting nodes or resolve externally verified unknown attempts. |
| `retry_workflow_from_node` | Derive a run that reuses successful work before a node and re-runs from that node onward. |

## How splitting works

1. The model (or you, via the agent) calls `new_session` with a brief
   `intent` and the current `session_id`, optionally with `worktree=true`
   plus a `branch`, and a `model`.
2. Ocman gathers context from the parent session's directory: the last 10
   messages, the current git branch, and a per-file summary of uncommitted
   changes.
3. Ocman assembles a structured Markdown prompt and sends it to a new
   OpenCode session.
4. A background watcher polls the child session. After the child's terminal
   turn, `new_session` returns its status and final assistant text directly.
   Called with `wait=false`, it queues that result durably for the parent's
   next idle edge instead. While a synchronous call waits, ocman emits MCP
   progress immediately and every 10 seconds so clients keep the request
   alive.
5. If the MCP caller disconnects, ocman defers a reminder telling the parent
   to call `await_session_result`. That reconnects to the same wait without
   prompting the child again, and it survives an ocman restart. Passing an
   explicit child ID also lets `await_session_result` pick up an asynchronous
   call. Direct `send_message_to_parent` updates keep the untrusted-data
   boundary explicit. `send_message_to_child` reopens the child for its next
   turn and supports the same direct result with `wait=true`.

## Embedding generated assets

Agents routinely produce files a chat transcript cannot show: a rendered
chart, an SVG diagram, a generated PDF. `embed_file` closes that gap.

1. The agent writes the file to disk as usual.
2. It calls `embed_file` with the absolute `path`.
3. Ocman returns a URL under `/api/file/{token}` plus a `markdown`
   snippet, which the agent includes in its reply.
4. The ocman UI renders that markdown: images and SVGs appear inline in
   the conversation, other types become a link the browser opens or
   downloads.

The token is an HMAC over the absolute path, signed with a key persisted
in `state.db`, so links keep working across restarts while a hand-crafted
or altered path is rejected with `403`. The endpoint sits behind the
normal dashboard auth guard, and responses carry `nosniff` plus a
`Content-Security-Policy: sandbox` header so an SVG or HTML asset opened
as a top-level document cannot run script.

MCP callers are local and already run as your user, so the tool does not
restrict which paths may be embedded. An agent that can call it can read
those files directly anyway.

## Splitting skill (optional)

Ocman ships an OpenCode skill that gives the agent better heuristics
for *when and how* to split work, so the MCP tool descriptions can stay
short and action-focused:

```text
.opencode/skills/ocman-sessions/SKILL.md
```

Workflow authoring and control guidance lives in:

```text
.opencode/skills/ocman-workflows/SKILL.md
```

For source-controlled examples, immutable-version semantics, migration safety,
and troubleshooting, see [Workflows](workflows.md). Publish a workflow before
starting it; pass a returned `version_id` to start exactly that revision.

When working inside this repository, OpenCode loads the skill from the
project config automatically (after a restart). To use the same guidance
in another project, copy that folder into the target project's
`.opencode/skills/` directory, or add this repository's
`.opencode/skills` path to that project's OpenCode `skills.paths` config.
