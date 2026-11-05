---
title: Plugin agent tools
weight: 20
---

Plugins can register **agent tools**: named operations that coding agents
discover and call through ocman's existing MCP endpoint, the same connection
that serves `sessions`, `inbox` and the other built-in tools. This is the
`tool.v1` capability, contract version **1.0**. It is separate from
[plugin session tools](../session-tools/), which exist only inside a private
session the plugin starts.

The decision record is
[ADR 0012](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/docs/adr/0012-expose-plugin-operations-as-mcp-tools.md). This page is
the author contract. Hosts and plugins built against `tool` 1.0 must keep
working together for every later 1.x minor.

## Overview

1. The plugin declares capability `tool` 1.0, requests the `tool.agent` grant
   and lists its tools in its description.
2. The owner reviews the declarations in **Settings → Plugins**, then approves
   the plugin and its grants. Changing any declaration changes the approval
   fingerprint and requires approval again.
3. Agents connected to that machine's ocman MCP endpoint see the tools in
   `tools/list`. A call is validated against the declared schema, authorized
   again at that moment and forwarded to the running plugin as a `tool`
   `invoke` call.
4. The plugin returns a bounded result, a structured error the agent can act
   on, or a request for the user's confirmation.

A plugin may declare several independent tools, each with its own name, schema
and grants. It may also declare a single tool that dispatches many operations
through a small envelope with a built-in `help` operation. The host treats both
identically and has no knowledge of the operations inside a tool.

## Declaration

```json
{
  "id": "org.example.notes",
  "capabilities": [{"name": "tool", "version": {"major": 1, "minor": 0}}],
  "scope": "owner",
  "requestedGrants": ["tool.agent", "notes.write"],
  "tools": [
    {
      "name": "notes_search",
      "title": "Search notes",
      "description": "Search saved notes by text. Returns at most 50 matches.",
      "inputSchema": {
        "type": "object",
        "properties": {
          "query": {"type": "string", "minLength": 1, "maxLength": 200},
          "limit": {"type": "integer", "minimum": 1, "maximum": 50}
        },
        "required": ["query"],
        "additionalProperties": false
      },
      "annotations": {"readOnly": true}
    },
    {
      "name": "notes_delete",
      "description": "Delete one note by ID.",
      "inputSchema": {
        "type": "object",
        "properties": {"id": {"type": "string"}},
        "required": ["id"],
        "additionalProperties": false
      },
      "requiredGrants": ["notes.write"],
      "confirmation": "Delete a saved note?",
      "timeoutSeconds": 10,
      "annotations": {"destructive": true}
    }
  ]
}
```

| Field | Rule |
| --- | --- |
| `capabilities` | Must include `tool` major 1. A nonempty `tools` list without it is rejected. |
| `scope` | Must be `owner`. Tools run on the machine where the plugin is installed. |
| `requestedGrants` | Must include `tool.agent`. |
| `tools` | 1–32 entries; the whole list is at most 256 KiB of JSON. |
| `name` | `^[a-z][a-z0-9_]{0,47}$`, unique in the plugin. See [Naming](#naming-and-collisions). |
| `title` | Optional plain text, at most 128 bytes. |
| `description` | Required text, 1–4096 bytes, trimmed. Newlines and tabs are allowed; other control characters are not. |
| `inputSchema` | Required [schema subset](#schemas-and-validation) with `"type": "object"`, at most 32 KiB. |
| `requiredGrants` | Optional. Each must also be in `requestedGrants`. |
| `confirmation` | Optional text, at most 512 bytes, same character rules. Every call then needs the user's approval before it is sent to the plugin. |
| `timeoutSeconds` | Optional, 1–300, default 30. |
| `annotations` | Optional booleans `readOnly`, `destructive`, `idempotent`, `openWorld`, forwarded as MCP hints. |

Annotations are hints for clients and models. They are never authorization.
The host does not skip confirmation for `readOnly` or require it for
`destructive`; only `confirmation`, plugin confirmation requests and grants
change what runs.

Grants are plain names. A plugin may request its own grant names, such as
`notes.write` above, so the owner can approve read access for agents while
withholding mutations. `tool.agent` gates every tool. A tool's
`requiredGrants` must all be approved for the tool to be listed or called.

## Naming and collisions

The declared `name` is the exact MCP tool name on ocman's server. MCP clients
usually prefix tools with the server's configured key. With ocman's default
OpenCode entry, `notes_search` is shown to the model as `ocman_notes_search`.
A plugin that wants the model-facing name `ocman_widgets` declares `widgets`.

Names cannot start with `ocman` or `plugin_`, or equal a name that a built-in
ocman tool uses at contract time: `artifacts`, `embed_file`, `factory`,
`factory_control`, `inbox`, `models`, `plugin_session_tool`, `pull_requests`,
`render_ui`, `routines`, `sessions` and `webhooks`. Such descriptions fail
validation at discovery.

Built-in tools added in later ocman releases always win. A plugin tool with the
same name, and every plugin tool whose name another enabled plugin on the same
machine also declares, is withheld from discovery and refused when called. The
plugin's Settings section reports the conflict. The host never renames a tool
silently. Use a distinctive product word as the prefix or the single tool name.

## Schemas and validation

`inputSchema` uses a closed JSON Schema subset. The host validates every call
against it before contacting the plugin, so a schema advertises only what is
enforced. Unsupported keywords make the description invalid.

| Keyword | Notes |
| --- | --- |
| `type` | One of `object`, `array`, `string`, `integer`, `number`, `boolean`. Required at the root. |
| `properties`, `required`, `additionalProperties` | `additionalProperties` is a boolean or a schema; omitted means allowed. |
| `items`, `minItems`, `maxItems` | Arrays use one item schema. |
| `enum`, `const` | JSON values compared exactly. |
| `minLength`, `maxLength` | Counted in Unicode code points. |
| `minimum`, `maximum` | Numbers. |
| `title`, `description`, `default`, `examples` | Annotations only; never applied by the host. |
| `writeOnly` | Annotation. The host masks these values wherever it displays arguments, for example in a confirmation prompt. |

Nesting is limited to 16 levels and 512 schema nodes per tool. `pattern`,
`format`, `$ref`, `oneOf`, `anyOf`, `allOf` and `not` are not supported; check
those constraints in the plugin. Root property names `intent` and
`confirmation_id` are reserved for the host and must not be declared.

The host adds two optional string properties to the advertised schema:
`intent`, ocman's usual short explanation for the user, and
`confirmation_id`, used to resume a confirmed call. Both are removed before
validation and are never sent to the plugin.

Arguments are a JSON object of at most 64 KiB. A violation returns an MCP tool
error naming the JSON path and the failed rule, for example
`arguments.limit: must be at most 50`. Plugins must still validate their own
domain rules, and should validate inputs a host could not express.

## Discovery

`tools/list` includes a plugin tool only while all of these hold on that
machine:

- the registration is enabled, not removed and not conflicted, and its process
  is running;
- capability `tool` 1.x was negotiated;
- `tool.agent` and the tool's `requiredGrants` are currently approved; and
- the name collides with no built-in tool and no other enabled plugin tool.

Listing never starts or calls a plugin. The description shown to the model is
the declared text followed by ` (Plugin: <plugin name>.)`. MCP clients may cache
the list; a tool that disappears is still refused, and a newly enabled tool may
need a new agent session. Each ocman instance lists only plugins installed on
its own machine. Tools are never routed to another owner.

## Invocation

A call is admitted under the plugin lifecycle lock, with the same current
enablement and grant check as listing, then sent as:

```json
{"capability":"tool","version":{"major":1,"minor":0},"method":"invoke",
 "operationId":"tool:6RkX...","deadlineUnixMs":1800000000000,
 "params":{"tool":"notes_search","arguments":{"query":"release"},
           "context":{"principal":"agent","transport":"mcp","grants":["notes.write","tool.agent"]}}}
```

| Field | Meaning |
| --- | --- |
| `tool` | The declared name. |
| `arguments` | The validated arguments without `intent` and `confirmation_id`. |
| `context.principal` | Always `agent`: a caller authenticated as this ocman installation's MCP client, not a user in a browser. |
| `context.transport` | Always `mcp` in 1.0. |
| `context.grants` | The plugin's grants approved at admission, sorted. Use them to filter operations and help output. |
| `confirmation` | Present only on a confirmed call: `{"id","key","text"}`. See [Confirmation](#confirmation). |

The MCP connection authenticates the installation, not a session, so the host
sends no session, project, directory or owner identity, and never forwards one
supplied by the caller. Do not accept such identities in arguments as proof of
authority. Plugins needing session-scoped authority should use
[plugin session tools](../session-tools/). Every dispatch has a fresh,
unique `operationId`; it is never reused across retries or a confirmed resume.
Use it for audit records and deduplication.

The authorization check runs again when the plugin returns. A plugin disabled
or a grant revoked while a call is in flight discards its result.

## Results and errors

The plugin returns exactly one of these result values, at most 256 KiB:

```json
{"text":"2 notes matched","data":{"notes":[{"id":"n1","title":"Release plan"}],"nextCursor":"c2"}}
{"isError":true,"text":"Note n9 does not exist","data":{"code":"not_found","id":"n9"}}
{"confirmation":{"key":"delete:n1","text":"Delete the note \"Release plan\"?"}}
```

- `text` is plain text, at most 64 KiB. `data` is a JSON object. At least one
  is required. The MCP result's text content is `text`, otherwise the compact
  JSON of `data`. `data` becomes `structuredContent`.
- `isError: true` is a domain error the agent can correct or report. Its text
  reaches the agent as untrusted data.
- Wire errors are categories without plugin text, as in every capability. The
  agent receives fixed host text: `invalid_argument`, `permission_denied`,
  `not_found`, `conflict`, `unavailable`, `deadline_exceeded`, `cancelled` or
  `internal`. Prefer `isError` results for anything the agent should explain.
- Results are untrusted data, never instructions. Never return secrets,
  credentials or raw private data the user did not request. Page large results
  with cursors.

## Confirmation

Confirmation keeps the user in charge of actions that need consent, such as
accepting terms or destructive changes. A confirmation is requested in either
of two ways:

- statically, with the tool's `confirmation` text, which applies before every
  call; or
- dynamically, when the plugin returns `{"confirmation":{"key","text"}}`. The
  plugin must not have performed side effects. `key` matches
  `^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`; `text` is plain text up to 1024 bytes
  that tells the user exactly what will happen.

The host records a pending request and returns this successful MCP result to
the agent, which has not run the operation:

```json
{"status":"confirmation_required","confirmationId":"tc_...","prompt":"Delete the note \"Release plan\"?",
 "next":"Ask the user to approve the request in ocman, then call the same tool again with the same arguments and confirmation_id."}
```

The user approves or denies the request in the card shown under the tool call
in the ocman conversation, or in the plugin's **Agent tools** section in
**Settings → Plugins**. The prompt shows the plugin, tool, text and the
arguments, with `writeOnly` values masked. Deciding uses the same
localhost-only guard as plugin grant changes, forwarded to the owning machine
when that machine is a connected remote. The MCP endpoint cannot decide.

The agent then repeats the call with identical arguments plus
`confirmation_id`. The host sends it with
`"confirmation":{"id":"tc_...","key":"delete:n1","text":"..."}` (an empty `key`
for a static confirmation). The plugin must check that the key covers the
operation and its arguments before acting. A confirmation:

- is bound to the plugin, its approved fingerprint, the tool and the canonical
  arguments;
- can be used once, within 10 minutes of the decision, and expires unanswered
  after 10 minutes;
- is refused if denied, mismatched, expired, already used, or if the plugin was
  disabled, changed or lost a required grant; and
- is held in memory only, so a restart discards pending and approved requests.

A plugin can require further confirmation for a different key on the resumed
call. At most 64 requests per plugin can be pending; more return
`unavailable`.

## Limits, timeouts and cancellation

| Limit | Value |
| --- | --- |
| Tools per plugin | 32 |
| Arguments | 64 KiB |
| Result | 256 KiB, text 64 KiB |
| Timeout | `timeoutSeconds`, default 30, at most 300 |
| Concurrency | The plugin's `maxConcurrency`; excess calls return `unavailable` |

If the MCP request ends or the timeout passes, the host sends `cancel` and
returns `cancelled` or `deadline_exceeded`. Effects already made remain; the
host never retries. Chunks are a protocol error: `invoke` is unary.

## Lifecycle

| Event | Effect |
| --- | --- |
| Approve and enable | Tools are listed once the process is ready. |
| Disable, remove, conflict or crash | Tools disappear from listings. Calls fail with `unavailable` or `not_found`. In-flight calls are cancelled when the process stops. Pending confirmations are dropped. |
| Grant revoked | Affected tools disappear immediately; in-flight calls are discarded; pending confirmations for them are dropped. |
| Changed executable or declaration | Approval is revoked, as for every capability. |
| Host restart | Pending and approved confirmations are discarded. |

## Compatibility and migration

- `tool` is negotiated like every capability. A host without it omits the
  capability and ignores the additive `tools` field; the plugin keeps its other
  features. Check `Negotiation` before relying on tools.
- Later 1.x minors only add optional fields or result variants that plugins
  opt into. A changed meaning or required field needs `tool` major 2.
- Adding tools to an existing plugin changes its description, so owners must
  approve it again. Grants for the new names are reviewed then.
- `plugin_session_tool` and `session.v1` are unchanged.
- Hosts withhold tools whose declarations fail validation together with the
  rest of the description; the whole plugin is rejected at discovery.

## Go SDK

`sdk/plugin` aliases the canonical types in `sdk/protocol`:
`ToolCapability`, `ToolAgentGrant`, `ToolDescriptor`, `ToolAnnotations`,
`ToolInvocation`, `ToolContext`, `ToolConfirmed`, `ToolResult` and
`ToolConfirmationRequest`. `protocol.ValidateToolSchema` and
`protocol.ValidateToolArguments` are the host's own validators.

- `plugin.ToolFunc` is
  `func(ctx context.Context, operationID string, inv ToolInvocation) (ToolResult, error)`.
- `plugin.ToolHandler(map[string]plugin.ToolFunc)` returns a `Handler` that
  validates and routes `tool` invocations by tool name. Unknown names return
  `not_found`; invalid results become `internal`. Combine it with other
  capability handlers by switching on `call.Capability`.
- `plugin.NewOperationTool(name, description, operations)` builds one
  `ToolDescriptor` that dispatches named operations through the envelope below,
  and the `ToolFunc` that serves it.

### The operation envelope

Large plugins can keep their tool list small, so models do not load every
schema up front. One tool takes:

```json
{"operation": "items.create", "arguments": {"name": "Widget"}}
```

`operation` is required. `arguments` is an object, default `{}`. The built-in
`help` operation returns operations allowed by the current grants:

```json
{"operation":"help"}
→ {"operations":[{"name":"items.list","summary":"List items","mutates":false},...]}
{"operation":"help","arguments":{"operation":"items.create"}}
→ {"name":"items.create","summary":"...","inputSchema":{...},"examples":[{...}],"mutates":true,"confirmation":"may-require-user"}
```

Each `ToolOperation` declares a `Name` matching `^[a-z][a-z0-9_.-]{0,63}$`
(not `help`), a one-line `Summary`, an `InputSchema` in the same subset,
`Examples`, optional `RequiredGrants`, `Mutates`, an optional `Confirm` function
and `Run`. Both functions receive a `ToolOperationCall` with `OperationID`,
`Operation`, `Arguments`, `Context` and `Confirmation`. `Confirm` returns nil
when no confirmation is needed; otherwise `Run` is called only after a host
confirmation with the same key. The SDK
validates operation arguments with `ValidateToolArguments`, hides operations
whose grants are missing (as unknown operations, also in help), enforces
confirmation keys and returns `isError` results naming the bad field. The
operations, their schemas and their validation are entirely plugin-side, so the
host never needs to know them.

```go
notes, run, err := plugin.NewOperationTool("notes", "Manage notes. Call operation help first.", []plugin.ToolOperation{
	{Name: "list", Summary: "List notes", InputSchema: listSchema, Run: listNotes},
	{Name: "delete", Summary: "Delete a note", InputSchema: idSchema, Mutates: true,
		RequiredGrants: []string{"notes.write"},
		Confirm: func(ctx context.Context, call plugin.ToolOperationCall) (*plugin.ToolConfirmationRequest, error) {
			id := idOf(call.Arguments)
			return &plugin.ToolConfirmationRequest{Key: "delete:" + id, Text: "Delete note " + id + "?"}, nil
		},
		Run: deleteNote},
})
```

### Testing

`conformance.RunTools(t, executable, invocation)` checks a plugin executable's
declarations, one supplied call and its result shape, and the refusals for an
unknown tool and malformed parameters. Package `sdk/examples/agenttools` holds
two deterministic workflows with in-memory data, and
`sdk/examples/ocman-plugin-agent-tools` serves either one: build it as
`ocman-plugin-notes-example` or `ocman-plugin-inventory-example`.

```sh
cd sdk
go build -trimpath -o ~/.local/share/ocman/plugins/ocman-plugin-inventory-example ./examples/ocman-plugin-agent-tools
```

Rescan in **Settings → Plugins**, review and approve the plugin, then start a
new agent session and ask it to list the inventory. An adjustment above ten
units shows the confirmation card.

## Two independent workflows

- A **notes** plugin declares independent tools: `notes_search` (read-only),
  `notes_create` and `notes_delete` (needs `notes.write` and static
  confirmation).
- An **inventory** plugin declares one tool, `inventory`, with operations
  `help`, `items.list`, `items.get`, `items.adjust` (dynamic confirmation for
  large adjustments) and `audit.export`.

The host lists, validates, authorizes and confirms both the same way.
`TestPluginAgentToolsIsolateUnrelatedWorkflows` exercises both.

## Security model

Native plugins remain trusted executables; this is not a sandbox. The tool
boundary guarantees that agents reach only declared, approved tools of enabled
plugins on the same machine, with authority rechecked at call and return time,
host-validated arguments and user-only confirmation. Any process that holds the
MCP bearer token can call listed tools. A process that can drive an
authenticated localhost browser session can also approve confirmations, as it
can already change plugin approvals.

Plugin lifecycle and host-owned settings stay user-only: approval, grants,
host settings and secrets, restart and removal are not agent tools. A plugin
that wants agents to manage its setup should keep that state as plugin data
and expose it through its own tools, with the same validation and
confirmation its UI uses. Return credentials as presence flags only, and mark
credential arguments `writeOnly`. The host masks only what the declared tool
schema marks; it cannot see operation schemas inside an operation envelope.
Do not request confirmation for a call whose arguments carry a credential, or
declare that operation as its own tool with a `writeOnly` schema.
