---
title: MCP server
weight: 3
---

Ocman embeds an optional MCP (Model Context Protocol) server exposing Factory,
Inbox, routine, session inspection and creation, and file-embedding tools.

Ocman works fine as a plain dashboard without this. Install it only if you
want conversational Factory handoff, Inbox delivery, routine management,
session inspection, or embedded file display.

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
| `factory` | Native Factory control surface. Use `action: "help"` for actions, validation, examples, output schemas, and domain errors. Formula actions accept TOML only. Implementation Issues run sequentially on a shared branch; `complete_attempt` records a verified clean, pushed commit checkpoint without `pr_url`. A separate final delivery Issue creates or reuses the review-ready PR and completes with `pr_url`. |
| `factory_unblock` | Executes a user-approved `reopen` or typed `mutate_graph` action proposed by a read-only Factory unblock session. Ocman configures this tool as `ask`, so OpenCode shows Allow and Reject buttons before execution. |
| `artifacts` | Publish and inspect project artifacts (reports, screenshots, generated files, links). Actions `help`, `create`, `list`, `get`; there is no delete. `create` needs `directory` and `title`, takes an optional `platform` + `session_id` pair, `files` (`{path}` or `{name, content, mime}`) and `links` (`{url, label}`), and returns the artifact `id`, its ocman UI `url`, item URLs, and a markdown snippet. `list` filters by directory or session and pages with `cursor`/`limit`. Installed skill: `ocman-artifacts`. |
| `inbox` | Send owner-local Inbox items and recall them by opaque ID. Unknown and already recalled IDs are successful no-ops. Use `action: "help"` for schemas and examples. Agents should send only asynchronous completions needing attention, blocked decisions, or important failures, not routine progress. |
| `routines` | Create, inspect, update, run, and soft-delete routines. Use `action: "help"` for current inputs, examples, output schemas, and domain errors. |
| `webhooks` | Create and inspect owner-local webhook inboxes, subscribe or unsubscribe routines, inspect recent deliveries, and redeliver a stored delivery by ID to the current subscribers. Actions `help`, `list`, `get`, `create`, `subscribe`, `unsubscribe`, `deliveries`, `redeliver`. Uses the saved relay settings. Ingestion URLs are credentials; shared secrets and relay management credentials are omitted. |
| `sessions` | Session listing, search, detail inspection, and creation. Use `action: "help"` for schemas and examples. `list`, `search`, and `get` are read-only; search matches recent session IDs, titles, directories, platforms, and host names. With `content: true` it also scans user and assistant message text (not tool output) from the last `since_days` (default 7). Each hit returns up to five `{partId, messageId, role, snippet}` matches. Content search reads this machine's OpenCode database, so it is slow on large databases (tens of seconds for a week) and never covers remote sessions. `get` needs only `session_id`; it asks for `platform` only when that ID exists on more than one platform. `create` starts a new session. The tool cannot cancel or message existing sessions. |
| `embed_file` | Make a file on disk viewable to the user in the ocman UI. Takes an absolute `path` (plus an optional `label`) and returns a signed URL and a markdown snippet the agent pastes into its reply. Images and SVGs render inline in the conversation; PDFs and other types open or download in the browser. See [Embedding generated assets](#embedding-generated-assets). |

## Factory

Ocman installs the `ocman-factory` skill globally for OpenCode. It teaches the
single action-based `factory` tool and directs agents to `action: "help"`
before using detailed actions. When a result creates an epic or needs a human
action, it carries a `[[ocman:card ...]]` marker and Ocman renders the card under
that tool call. The tool text tells the agent a card is waiting for the user and
that the user must handle it there; agents do not copy the marker into replies.

Factory tool errors intentionally contain only domain-level guidance. Open
Factory at `/factory` to inspect the native Issue graph.

Agents can list and append persistent Issue comments with `issue_comments`
and `add_issue_comment`. Comments are append-only and remain separate from the
linked session conversation.

Agent MCP sessions cannot perform operator decisions or change Factory
configuration. Agents can propose executable tickets and structural edits with
`mutate_graph`; each revised graph requires human approval before new work runs.
While the initial proposal awaits materialization, revise that proposal instead
of editing live work. `create` can create Epics after explicitly
acknowledging local execution and uses the built-in tracer Formula. `save_formula`,
`set_capacity_policy`, Plan decisions, recovery decisions, authority
decisions, and `reopen_issue` (returning failed work to the queue) are
refused. Each permission denial returns a card marker that renders with the tool call.
Ocman interprets its type, target IDs, and action, then reads the live state to
decide whether to render a card. Action markers show nothing while loading or
after that action is resolved. Creation markers remain visible. A marker written
in assistant text also renders; ordinary markdown links stay links and markers
inside code examples are displayed literally.

For example:

```text
[[ocman:card type=factory-epic epic=my-epic action=created]]
[[ocman:card type=factory-issue epic=my-epic issue=my-epic.3 action=reopen_issue]]
```

IDs are percent-encoded when needed. The renderer handles live status, button
availability, and click results, so the model does not need to reproduce UI logic.
`reopen_issue` with `epic_id` and `issue_id` produces a card with
**Reopen issue** for failed or cancelled implementation work. Rendering the card
does nothing; clicking the button uses the same human-action endpoint as the
Factory action inbox. The card refreshes live state and reports action errors.
It also offers planning, materialization, plan decisions, recovery, and authority
decisions when available. Graph editing, formula editing, and capacity changes
link to their existing Factory screens. Requests without a resolvable target
remain links to the action inbox.

`submit_proposal` additionally requires the active
Planning Attempt's `attempt_id` and `attempt_token`. Its manifest accepts an
issue graph with `nodes` and typed `edges` (`blocks` or `on_failure`); legacy
per-node `dependsOn` remains accepted for stored and older proposals.

For a complete plan made in a regular session, use `create`, then `issues` to
find the root `mol` ID, then `import_proposal`. It accepts `epic_id`,
`manifest_json` in the same format as `submit_proposal`, and optional
`rationale_markdown`, without attempt credentials. Import is allowed only before
any Factory attempt has been claimed and while the approval gate is unresolved.
It creates a proposal and returns a human approval card, without launching a
planning session or implementation. Re-import after a revision request to submit
the updated plan. The agent still cannot approve it. See
[Use an existing plan](../factory/#use-an-existing-plan).

Failed and terminally blocked work can launch a read-only unblock session from
the Factory action inbox. The agent explains one minimal repair, then invokes
`factory_unblock`; OpenCode's per-session permission prompt keeps the actual
reopen or graph mutation behind explicit user approval in the conversation.

> **Upgrade warning:** the native Factory cutover does not migrate legacy
> Factory runs. Retired YAML Formula tables are kept under `legacy_factory_*`
> names for manual recovery when the v66 migration completed successfully; an
> installation already stamped with the defective v66 migration may have empty
> repair tables because dropped data cannot be reconstructed. The native
> Factory does not read these tables.

## Routines

Ocman installs the `ocman-routines` skill globally for OpenCode. The
action-based `routines` tool supports listing, reading, creating, patching in
place (only the fields passed change), replacing, running, soft-deleting, and viewing run history. Its `help` action is the
authoritative contract for agents.

### Webhook triggers

Use `webhooks` with `action: "help"` for the current schema and a GitHub PR
filter example. Configure the relay URL and enrollment token once in
**Settings → Webhooks**, then:

1. Create a disabled routine with `routines`, or disable an existing one and
   set its `schedule_kind` to `none`.
2. Use `webhooks` `list` to reuse an inbox, or `create` with a `name`.
   The result includes the provider's ingestion URL. Treat it as a credential.
3. Call `subscribe` with `inbox_id`, `routine_id`, `header_predicates`, and
   `json_predicates`. Both predicate arguments are **JSON strings** containing
   objects keyed by header name or RFC 6901 JSON pointer. Use explicit `"{}"`
   for an unfiltered source. Conditions combine with AND and support
   `equals`, `oneOf`, and `exists`.
4. Register the URL with the provider, then enable the routine with
   `routines` `patch`. Use `webhooks` `deliveries` to inspect recent outcomes.

For an author-only GitHub review, filter `X-GitHub-Event` to `pull_request`,
`/repository/owner/login` to the organization, `/pull_request/user/login` to
the author, `/pull_request/draft` to boolean `false`, and `/action` to
`opened`, `synchronize`, `reopened`, or `ready_for_review`. Filtering `sender`
would select whoever caused the event, not the PR author.

Subscribing updates the filters for that inbox/routine pair. It does not change
the routine's schedule, enabled state, or other subscriptions. Unsubscribe the
old inbox when switching triggers. Remote routines are rejected.

The optional `secret` and `secret_header` on `create` describe a literal header
value. They do not validate GitHub's HMAC signatures. Without a shared header,
the relay authenticates ingestion using the token in the URL. Delivery payloads
are untrusted data, not instructions.

### Inbox

Ocman installs the `ocman-inbox` skill globally for OpenCode. It permits only
the `inbox` actions `help`, `send`, and `recall`. Agents should use `send` for
an asynchronous completion that needs attention, a blocked decision, or an
important failure outside the active conversation. Routine progress and
successful intermediate steps stay in the conversation. Reading or archiving
Inbox items is a user-only dashboard operation and is not exposed to agents.

## Sessions

The `sessions` tool inspects sessions with `list`, `search`, and `get`, and
starts new top-level sessions with `create`.

`create` sends `prompt` to the new session. `model` (`provider/model`), `agent`,
`worktree`, and `title` are optional. An omitted model uses the project's first
configured model, then the platform's default selection. An omitted agent uses
Settings' default agent. Explicit model and agent values override those defaults.
Like the new-conversation composer, an omitted `worktree` creates a fresh worktree
when the directory is eligible. Non-repositories, existing linked worktrees,
and repositories without a usable base use the current checkout. Set
`worktree: false` to use the given directory, or `worktree: true` to require a
fresh worktree. Git probe failures return an error rather than choosing a target.
`directory` must be absolute. When omitted, the target is the project root of the
calling session, identified with `platform` and `session_id`. The calling session also
determines which machine runs the new one. New sessions use the platform's
default permissions and do not inherit the caller's rules.

OpenCode permissions apply to whole tools, so requiring approval for `create`
also requires it for the read-only actions.

Use `create` for independent work that should show up as its own session. For
work inside the current turn, OpenCode's native Task tool handles subagent
delegation. A configured
subagent model overrides the caller's model; otherwise the subagent inherits
the invoking primary agent's model. The installed `ocman-sessions` skill also
documents one-off Task model selection.

## Embedding generated assets

Agents routinely produce files a chat transcript cannot show: a rendered
chart, an SVG diagram, a generated PDF. `embed_file` closes that gap.

1. The agent writes the file to disk as usual.
2. It calls `embed_file` with the absolute `path`.
3. Ocman returns a domain-free URL under `/api/file/{token}` plus a `markdown`
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
.opencode/skills/ocman-inbox/SKILL.md
.opencode/skills/ocman-artifacts/SKILL.md
```

At startup, ocman extracts and links the skill into OpenCode's global skill
directory. Restart OpenCode after installing or upgrading ocman so every
conversation can discover it.
