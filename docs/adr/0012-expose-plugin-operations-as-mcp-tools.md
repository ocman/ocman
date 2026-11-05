# Expose plugin operations to agents as MCP tools

Plugins can already do a lot for a user: settings, palette actions, declarative pages, in-app UI and private sessions. None of that is reachable by a coding agent working in an ordinary session. An agent that should set up, control or inspect what a plugin manages has to ask the user to click through the UI. We want an installed, enabled plugin to register operations that agents discover and call through ocman's MCP endpoint, with the same authorization and consent the user interface enforces.

## Existing mechanisms investigated

- **`session.v1` tools (`plugin_session_tool`).** Plugin-declared catalogs with schemas, a generic MCP dispatcher, grant rechecks per call and cancellation. They exist only inside a private session the plugin itself starts. The catalog is sent in that session's prompt and reached through a random binding, so an agent in any other session cannot discover or call them. Gap: no installation-wide discovery, and the binding model is the wrong authority for a user's own agents.
- **`ui.v1` `call`.** Arbitrary plugin-defined methods with JSON parameters, admitted under the lifecycle lock and rechecked before return. This is the plugin's own browser API. Its methods are not declared, have no schemas, carry no mutation or consent metadata and assume a user is looking at the UI. Exposing it to agents would hand them every UI method, including ones the UI guards with explicit dialogs. Gap: no declaration, schema or confirmation semantics.
- **`action.v1`.** Declared commands with host-held confirmation tokens and durable receipts. Actions take no arguments, return only UI result kinds, and the confirmation token is returned to the caller, which would let an agent confirm its own request. Gap: no arguments, results unsuitable for models, confirmation not separated from the caller.
- **`page.v1`.** Typed forms and collections with validation hooks. Pages are rendered views with fixed field types, not callable operations. Gap: not an invocation surface.
- **MCP module tools.** Feature modules contribute static tools when the handler is built, and `ToolFilter` hides them per request. Plugin tools appear and disappear with approval, so they cannot be registered once at startup. Gap: no dynamic registration.
- **Factory control cards.** `factory_control` records a proposal and the conversation renders a card under the tool call that the user confirms in the browser. This is the established pattern for user consent requested by an agent, and we reuse it.

## Decision

Add a negotiated capability, `tool` 1.0, with the static `tools` declaration in the plugin description, the `tool.agent` grant and one `invoke` method. Declarations are part of the approval fingerprint, so the owner reviews them, and changes require new approval. The host validates declarations at discovery, including names, a closed JSON Schema subset and limits.

The MCP module synchronizes plugin tools into the shared MCP server before each `tools/list` and `tools/call`. Listing and dispatch apply the same authorization: enabled, running, not conflicted, `tool.agent` and per-tool grants currently approved, and no name collision. Built-in tools win collisions; duplicate plugin names withhold every claimant. Each call is admitted under the plugin lifecycle lock, rechecked when the result returns, bounded by a per-tool timeout and cancelled with the MCP request. Arguments are validated by the host against the declared schema, then the plugin validates its own domain rules.

The MCP connection authenticates the installation, not a session. The host therefore forwards no caller identity and marks every call with `principal: "agent"` and the plugin's current grants. Session-scoped authority remains the job of `session.v1` tools.

Consent is host-owned. A tool can require confirmation statically, or the plugin can ask for it with a key and text. The host records the request in memory, returns its ID to the agent and shows a card under the tool call, using the Factory control-card pattern, and in the plugin's Settings section. Only a localhost browser request can approve it. The agent repeats the call with that ID, and the host forwards the confirmation only when the plugin, fingerprint, tool and canonical arguments still match. The agent never receives anything that can approve its own request.

Plugins may declare many tools. The SDK also offers an operation envelope, one tool with a `help` operation and per-operation schemas, for plugins whose surface would otherwise flood a model's context. The host does not know about operations.

## Consequences

Agents can perform any operation a plugin chooses to expose, under the same grants and consent as the UI, without host changes for each plugin. Plugins own the parity between their UI and their tools, and should share implementation between them. Confirmations do not survive a restart, and in-flight results are discarded when authority is revoked mid-call. Tools are owner-local: an agent sees plugins installed on the machine whose MCP endpoint it uses, and cross-owner routing is future work. Plugin lifecycle management itself (approval, grants, configuration, removal) stays a user-only browser action, because it is the trust root for everything above.
