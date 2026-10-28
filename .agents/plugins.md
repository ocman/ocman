# Native plugins and conversation delivery

Read `sdk/protocol/README.md`, `docs/features/plugins.md` and the SDK
conformance tests before changing plugin behavior.

## Protocol and ownership

`sdk/protocol` (in the nested public `github.com/ocman/ocman/sdk` module) owns
executable discovery, versioned NDJSON, supervision and the capability brokers.
Host packages import it as `plugins "github.com/ocman/ocman/sdk/protocol"`. `pluginsmod` owns HTTP, Settings, discovery lifetime,
remote operations, palette actions, panes, conversation delivery, the `session.v1`
broker and completion outbox, launch-time catalogs and `/api/permission-profiles`.
Registration and conversation tables remain in `internal/state`. Permission
profiles and `session.v1` tables belong to `pluginsmod.Schema` in `store.go`.

- Executables live in `~/.local/share/ocman/plugins` or `OCMAN_PLUGIN_DIR`.
  Discovery itself runs code; do not treat unapproved binaries as data.
- Build examples with `make install-plugin PLUGIN=slack|beads|fixture|catalog` and
  `-trimpath` for reproducible approved checksums. Owners retain binaries,
  secrets, grants, config, diagnostics and action receipts. Remote projection
  uses authenticated `PluginOperation` RPCs.
- Declarative results (`action`, `pane`, `page`) are typed data, never plugin
  JavaScript, HTML or CSS. `ui.v1` is the separate in-app tier (ADR 0009):
  hub-local only, `ui.app` grant, checksum-bound bundles, shared singletons at
  `/plugin-shared/` and the curated `@ocman/plugin-ui` surface in
  `frontend/src/plugin-ui` typed by `sdk/plugin-ui/index.d.ts`. Never forward
  `ui-*` operations to remote owners. Add public exports only to both files and
  bump `UISDKVersion` and the package version together. Platform providers,
  remote-owner (iframe) UI, relay inbox capability, registry/updates,
  signatures, sandboxing and Codex remain future work.
- SDK DTOs alias canonical `sdk/protocol` types. Reusable conformance tests
  live in `sdk/plugin/conformance`; the deterministic example is
  `sdk/examples/ocman-plugin-fixture`.
- `sdk/` must depend only on the standard library and never on the root module;
  `cd sdk && go mod tidy` checks this in CI. Root `./...` skips the nested
  module, so run `cd sdk && go test ./...` too. Root `go.mod` replaces it with
  `./sdk`. Changes under `sdk/` (other than Markdown) are tagged `sdk/vX.Y.Z` on
  `main` by `scripts/sdk-next-tag.sh`; see `sdk/plugin/README.md`.
- `pane.v1` declarations are owner-local and require `pane.project`. Listing
  declarations invokes no plugin. Reads authorize before sending the directory
  through the owner's RPC. Validate bounded acyclic trees. Mount the frontend
  query only while open and consume AbortSignal. Beads CLI parsing lives in
  `examples/ocman-plugin-beads`, not Host.
- Drain events through `consumePluginEvents`. An unread event flood fails the
  process rather than silently losing events.

## Conversation admission

Plugins emit normalized unsolicited messages. Authorize enablement,
`conversation.session` grant and the one required project setting from one row
under one lock. `Description.Validate` refuses hub/global conversation scope.
A connector owns one machine's credentials and project; two owners sharing a
provider app would compete for its stream.

- Create through `Deps.Local`, wired to `Router.Local()`, never inferred
  `ForDir`. Otherwise a conversation could run on an unapproved machine or
  silently fall back after disconnection. Remote Settings projection fails
  visibly with 503 when the owner is offline.
- Atomically claim durable `(pluginId, accountId, threadId)` to
  `(platformId, sessionId)` mappings. Preserve isolation across workspaces and
  owner-qualify the session. Restart must resume the same mapping.
- Reserve stable inbound event IDs as `conv-in:<accountId>:<eventId>` in durable
  receipts before work, for at-most-once prompt admission.
- Deliver through `queuesvc.Enqueue`, never direct `sendNow`: idle sessions
  respond now; mid-turn messages wait in arrival order for the next idle edge.
- On idle, read the newest assistant text under a recovered worker and append
  to the durable outbox keyed by `<sessionId>:<messageId>`. Repeated edges append
  nothing. Do not send provider replies directly from the idle callback.

## Durable outbox

`pluginsmod/outbox.go` delivers independently of admission. Each AUTOINCREMENT
row ID is immutable identity and sequence, exposed as stable `conv-out:<id>`.

- Claim only the head of each conversation's ordering group. Different threads
  may deliver concurrently, but replies in one thread never overtake each other.
- Acknowledge after the call. Delivery is at-least-once; replay the same operation
  ID after a crash. The provider adapter must deduplicate, and must never blindly
  repeat an uncertain post.
- Retry with bounded backoff from five seconds to five minutes. After six
  attempts, expose a dead letter that blocks only its conversation. Retry/discard
  decisions are localhost-only and visible in Settings reply delivery.
- Recheck `plugins.ConversationAllowed` before scheduling. Disabling/revoking
  pauses rows without spending retry budget or discarding text/mappings.
- Per-plugin count/byte caps stop inbound admission before reserving its event
  receipt. Do not drop owed replies; redelivery can enter after backlog drains.

Slack specifics stay in `examples/ocman-plugin-slack`: Socket Mode uses
`apps.connections.open`, WebSocket `app_mention`, then `chat.postMessage`. Ignore
bot-originated events to prevent loops and honor `Retry-After`.
`internal/server/plugin_conversation_slack_test.go` covers discovery, conformance,
first mention, mid-turn input, reply and restart against a mock workspace.
`OCMAN_SLACK_API` is a test-wrapper hook, unreachable in production because the
plugin environment is fixed. Mock tests do not replace live app/token/channel
validation described in the user docs.

## Attention notices

Notices point users to ocman when permissions, questions, errors or failed
admission block progress. Use the same durable outbox and `(plugin, operation_id)`
deduplication, not new state.

- Emit only a fixed `conversationNoticeText` sentence plus a link. Never include
  permission text, patterns, metadata, errors or transcript content. Decisions
  remain in ocman under existing provenance and auto-approval rules.
- Key notices by prompt request ID, so re-observation appends nothing. Resolve
  through the continued turn's reply; do not retract notices.
- Trigger through `autoapprove.Deps.PromptNeedsUser` after the approval decision,
  including unsafe verdicts, disabled auto-approval and question observation.
  Never page on raw `permission.asked` while the judge may still approve it.
- Links use `Deps.PublicURL`, configured through `-public-base-url` or
  `OCMAN_PUBLIC_BASE_URL`. Without it they fall back to loopback; remote provider
  deployments must set a reachable public URL.
