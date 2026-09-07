---
title: MCP server
weight: 3
---

Ocman embeds an optional MCP (Model Context Protocol) server exposing Factory,
routine, read-only session, and file-embedding tools.

Ocman works fine as a plain dashboard without this. Install it only if you
want conversational Factory handoff, routine management, session inspection,
or embedded file display.

## Endpoint

The server uses the Streamable HTTP transport and listens on its own
loopback-only port, separate from the web UI:

- `http://127.0.0.1:8227/mcp` is the dedicated MCP listener (`-mcp-addr`).
  It works the same in dev and production, and needs no credentials.
- `http://localhost:8228/mcp` is the same endpoint on the web UI's port,
  both in dev (Vite proxies it to the backend on `:8229`) and for the
  production binary's default `-addr`. Password auth applies there, so a
  native MCP client gets `403` when auth is configured.

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
| `factory` | Native Factory control surface. Use `action: "help"` for actions, validation, examples, output schemas, and domain errors. Formula actions accept TOML only. Implementation Issues run sequentially in one shared Epic worktree; `complete_attempt` requires a clean, pushed handoff and the one pull request whose head matches that branch. |
| `routines` | Create, inspect, update, run, and soft-delete routines. Use `action: "help"` for current inputs, examples, output schemas, and domain errors. |
| `sessions` | Read-only session listing, search, and detail inspection. Search matches recent session IDs, titles, directories, platforms, and host names. The tool cannot create, cancel, or message sessions. |
| `embed_file` | Make a file on disk viewable to the user in the ocman UI. Takes an absolute `path` (plus an optional `label`) and returns a signed URL and a markdown snippet the agent pastes into its reply. Images and SVGs render inline in the conversation; PDFs and other types open or download in the browser. See [Embedding generated assets](#embedding-generated-assets). |

## Factory

Ocman installs the `ocman-factory` skill globally for OpenCode. It teaches the
single action-based `factory` tool and directs agents to `action: "help"`
before using detailed actions.

Factory tool errors intentionally contain only domain-level guidance. Open
Factory at `/factory` to inspect the native Issue graph.

Agents can list and append persistent Issue comments with `issue_comments`
and `add_issue_comment`. Comments are append-only and remain separate from the
linked session conversation.

Agent MCP sessions cannot perform operator decisions, create executable graph
issues, or change Factory configuration. Non-executable graph edits remain
available through `mutate_graph`; `create` can create Epics after explicitly
acknowledging local execution and uses the built-in tracer Formula. `save_formula`,
`set_capacity_policy`, Plan decisions, recovery decisions, authority
decisions, and `reopen_issue` (returning failed work to the queue) are
refused; they stay in the Factory action inbox. `submit_proposal` additionally requires the active
Planning Attempt's `attempt_id` and `attempt_token`. Its manifest accepts an
issue graph with `nodes` and typed `edges` (`blocks` or `on_failure`); legacy
per-node `dependsOn` remains accepted for stored and older proposals.

> **Upgrade warning:** the native Factory cutover does not migrate legacy
> Factory runs. Retired YAML Formula tables are kept under `legacy_factory_*`
> names for manual recovery when the v66 migration completed successfully; an
> installation already stamped with the defective v66 migration may have empty
> repair tables because dropped data cannot be reconstructed. The native
> Factory does not read these tables.

## Routines

Ocman installs the `ocman-routines` skill globally for OpenCode. The
action-based `routines` tool supports listing, reading, creating, replacing,
running, soft-deleting, and viewing run history. Its `help` action is the
authoritative contract for agents.

## Sessions

The `sessions` tool intentionally stops at inspection: `list`, `search`, and
`get`. OpenCode's native Task tool owns subagent delegation. A configured
subagent model overrides the caller's model; otherwise the subagent inherits
the invoking primary agent's model. The installed `ocman-sessions` skill also
documents one-off Task model selection.

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

## Installed skill

MCP interaction guidance lives in:

```text
.opencode/skills/ocman-factory/SKILL.md
.opencode/skills/ocman-routines/SKILL.md
.opencode/skills/ocman-sessions/SKILL.md
```

At startup, ocman extracts and links the skill into OpenCode's global skill
directory. Restart OpenCode after installing or upgrading ocman so every
conversation can discover it.
