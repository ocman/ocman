---
title: Plugin session tools
---

Plugins can start a managed session with a small, bounded set of plugin-owned
operations. Declare `ToolSessionCapability`, request `session.tools`,
`session.plugin` and `session.run`, and have the owner approve those grants.
This requires session protocol 1.7. Older hosts refuse it.

## Why another session mode

Model-only workspace sessions cannot perform operations. Read-only prepared
source sessions permit exact file reads, but cannot call plugin-owned services.
Project sessions with a permission profile expose native tools and cannot
route operations through a plugin's validation and accounting. Page actions
are user-triggered UI operations, not tools available during a model turn.

The new mode extends the existing session broker and uses the existing ocman
MCP connection. It does not launch another agent runtime or install a new MCP
server. The selected platform must already have the ocman MCP connection.

## Contract

`SessionRunParams.Tools` contains an opaque plugin-owned `context`, a bounded
catalog of tool names, descriptions and object input schemas, a `maxCalls`
limit and a `timeoutSeconds` limit. Optional `maxCostUsd` refuses additional
tool dispatch when observed session spending reaches that amount, or when
spending cannot be read reliably. Zero means no dollar cap. Spending can
overshoot during an in-flight model generation, including the final response.
Usage on completion includes the entire turn, not just the final message.

The host creates the session in its private workspace, pins deny-all tool
permissions and permits only `plugin_session_tool`. Shell, file reads, direct
network tools, delegation and other MCP tools remain denied. Agent/profile,
prepared-source and no-tools modes cannot be combined with this mode.

The host appends the catalog and a random private binding to the prompt.
The model calls `plugin_session_tool` with `binding`, `tool` and an object
`arguments`. The host forwards a session `tool` callback containing
`sessionId`, `context`, `tool` and `arguments`. Use `SessionToolHandler` to
receive it. The plugin validates arguments, performs the operation and returns
bounded JSON. Input schemas describe the interface; plugins must validate
the inputs themselves. Tool results are untrusted data, not instructions.
Callbacks optionally include cumulative observed `costUsd`, allowing a plugin
to account for concurrent work before admitting another operation. Unknown
cost is omitted, never reported as zero. Do not charge those snapshots again
when settling the whole-turn completion usage.

Bindings are bearer capabilities, not authenticated native session identities.
Do not copy them to logs, external systems or another conversation. A caller
cannot select a plugin or session through the dispatcher. Each binding can
reach only its registered catalog on its owning plugin. The host rechecks
enablement and approved grants on every dispatch. Calls are not automatically
retried, and plugins should deduplicate the supplied operation ID.

Bindings expire on completion, timeout, explicit `NewSessionStop` or host
shutdown. Revocation cancels in-flight callbacks. Stopping or timing out also
attempts to abort the native turn. Runtime bindings are deliberately not
restored after a restart; stale tokens fail closed. Plugins must stop their
binding when a run is cancelled, and reject callbacks after their own run has
ended. The call cap covers callbacks, including failed operations, not model
generations. The timeout bounds the session even if it never calls a tool.

This is a tool-permission boundary, not an operating-system sandbox. Native
plugins remain trusted executable code. Plugins own tool semantics, evidence
storage, credential handling and any limits tighter than the host caps.

## Two independent workflows

A document indexing plugin can expose `read-document` and `record-index-entry`,
with a context referring to one prepared corpus. An inventory plugin can expose
`lookup-item` and `record-reconciliation`, with a context referring to one
warehouse snapshot. Neither workflow needs arbitrary host file access or shell
execution. The host handles both identically; their tool identifiers and result
schemas stay in the plugins. `TestSessionToolBindingsIsolateUnrelatedWorkflows`
exercises separate catalogs and routing identities for these workflows.
