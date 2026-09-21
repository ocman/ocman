---
title: Plugins
weight: 18
---

Ocman runs native plugin executables on the machine where they are installed.
Two capabilities are supported: `action.v1`, which adds commands to the command
palette and returns results rendered by ocman, and `conversation.v1`, which lets
a chat provider start an ocman session and receive its completed reply. Manage
installations in **Settings → Plugins**, selecting the plugin owner before making
changes.

## Trust and installation

**Plugins are trusted native code, including during discovery.** Rescanning runs
each candidate's `describe` command before you enable it. Only install code you
trust to run with the ocman user's operating-system access. Grants limit the
context ocman sends through its action broker; they do not restrict a plugin's
filesystem or network access. Checksums detect changes, not publisher identity.
There is no sandbox or signature verification.

Install a regular executable named `ocman-plugin-<name>` directly under
`~/.local/share/ocman/plugins`. Set `OCMAN_PLUGIN_DIR` on the owning ocman process
to use another directory. Hidden files, symlinks, subdirectories, nested files,
and files without executable permission are ignored. The host does not download
or install binaries for you.

For a local development installation, run these commands from the ocman checkout:

```sh
mkdir -p "$HOME/.local/share/ocman/plugins"
go build -o "$HOME/.local/share/ocman/plugins/ocman-plugin-fixture" ./examples/ocman-plugin-fixture
```

Ocman scans at startup. Click **Rescan plugins** after installing, replacing, or
removing an executable; there is no filesystem watcher. New registrations start
disabled. Review the ID, version, SHA-256 checksum, capabilities, execution scope,
and requested grants. Configure required settings, then click **Enable** and
**Approve grants and enable**. Enablement requires approval of all requested
grants, including an explicit empty set when none are requested.
Approval is bound to the reviewed executable and declaration. If another rescan
changes them before approval, refresh the catalog and review the new registration.

Changed code or declarations revoke approval. Duplicate plugin IDs conflict and
disable all matching candidates. An executable path cannot change its registered
plugin ID. Fix the installation and rescan before approving it again.

## Configuration and secrets

**Configure** renders declared string, boolean, number, integer, and string-enum
settings. The host rejects unknown fields, wrong types, and missing required
values. Public values are replaced when saved and declared defaults are applied.
Secret inputs are write-only strings: omitting a secret preserves it, while an
empty string clears an optional secret. Reads show only whether it is configured.

Configuration and health belong to the installation's owner. Public settings live
in its `state.db`. Beside that database, ocman stores private plugin files under
`plugin-data/<sha256(id)>/` and secret snapshots under
`plugin-secrets/<sha256(id)>/`. Directories use mode `0700`; secret files use `0600`.
These are permission-protected files, not an encrypted secret vault. Historical
snapshots remain for rollback and diagnostic redaction.

Serve processes receive effective configuration, including secrets, as one JSON
object followed by a newline on inherited file descriptor 3. Read and close it
before emitting the serve hello. Configuration is absent from arguments and
environment variables; describe has no configuration descriptor. No repository
path, inherited ocman credentials, or secret-store path is supplied.

Saving configuration for a ready, enabled plugin restarts it and waits for its
handshake. Failure restores the last working public and secret snapshots and
restarts the previous configuration, even if the HTTP request was cancelled.
If ocman exits before activation finishes, startup restores the last working
checkpoint before launching enabled plugins. Saved edits to disabled plugins remain.
Check **Refresh health** after a failed save. An enabled unhealthy plugin must be
disabled before repairing its configuration, then enabled again. Disabled plugins
can be configured without starting a serve process.

## Process contract and lifecycle

An executable accepts one argument, `describe` or `serve`. Stdout is strictly
UTF-8 newline-delimited JSON, one object per line; diagnostics go to stderr.
Each launch receives a fresh `OCMAN_PLUGIN_TOKEN`, which the first `hello` must
echo. Never log or persist it. The remaining environment is
`PATH=/usr/bin:/bin` and `LANG=C.UTF-8`; package runtime dependencies accordingly.

- `describe` runs in `/` with stdin at EOF, emits one token-bound hello containing
  the description, and exits successfully within three seconds.
- `serve` runs in the plugin's private data directory. It reads configuration,
  emits the same approved description in a token-bound hello, then waits for the
  host's hello acknowledgment before handling calls. Readiness has a three-second
  deadline.
- The description includes a reverse-domain ID, release version, process version,
  capability versions, execution scope, concurrency, grants, settings, and actions.
  Process major versions must match; the lower minor is selected. Capabilities
  negotiate independently. Unknown or incompatible capabilities are omitted.
- General calls have an ID, stable operation ID, capability/version, method,
  deadline, and parameters. They produce zero or more ordered chunks and exactly
  one result. `action.v1` permits only the terminal result, without chunks.

The server keeps one supervised serve process per enabled local registration and
rechecks its approved checksum before launch. Disable, removal, or conflict stops
it. Crashes and protocol failures settle callers without replaying their calls.
Restart delays start at 100 ms and double to a five-second cap, with at most five
automatic restarts per supervised lifetime. A successful handshake does not reset
that budget. Exhaustion leaves durable unhealthy state across ocman restarts;
**Retry**, **Restart**, or explicit enablement resets it.

Calls respect advertised concurrency, at most 256. Deadlines or cancellation send
a cancel frame; a plugin that fails to finish within the one-second cancellation
grace is terminated. Shutdown sends a shutdown frame, allows one second to exit,
then kills and reaps the process group. Frames are limited to 1 MiB, general call
output to 8 MiB, and chunk/event queues are bounded. Invalid framing, stream
ordering, or slow-consumer overflow terminates the process.

The [wire contract](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/internal/plugins/README.md)
defines exact frames, validation rules, error categories, and HTTP endpoints.

## Authoring action.v1

Advertise capability `{"name":"action","version":{"major":1,"minor":0}}`
and declare actions in the description. For example:

```json
{
  "id": "report",
  "label": "Create report",
  "placement": "session",
  "requiredGrants": ["context.session"],
  "surfaces": ["command-palette"],
  "confirmation": "Create a report for this session?"
}
```

Also include `context.session` in the plugin's `requestedGrants`. Placements are
`global`, `project`, and `session`; `command-palette` is the only current surface.
Each invocation uses method `invoke` with parameters such as:

```json
{"actionId":"report","context":{"sessionId":"ses-1"}}
```

There are no arbitrary action parameters. The broker sends only context covered
by both the action's required grants and current user approvals:

| Grant | Supplied context |
| --- | --- |
| `context.owner` | Opaque owner ID |
| `context.project` | Opaque project ID, never a directory |
| `context.session` | Opaque session ID |
| `context.route` | Core route name without parameters or query strings |
| `context.selection` | Up to 100 project/session references, never selected text |

These references provide no API access to transcripts, credentials, databases,
or filesystem paths. The host handles confirmation before dispatch. The plugin
receives a maximum 30-second deadline and must honor cancellation.

Return `{"results":[...]}` with 1–16 typed items, at most 1 MiB total:

```json
{"results":[{"kind":"notice","text":"Report ready"}]}
```

Other result kinds are HTTP(S) `link`, base64 `artifact`, static core `navigation`,
and `refresh` hints. The host renders text, validates URLs and targets, and
replaces artifact bytes with authorized download handles. HTML, JavaScript,
arbitrary routes, filesystem download paths, and unknown result fields are rejected.
Failures return a safe error category rather than diagnostic text.

Keep the same operation ID across confirmation and retries. The owner deduplicates
concurrent calls and commits a receipt before dispatch. Reusing an ID with changed
input conflicts; after a host restart, a prior receipt also conflicts instead of
repeating an uncertain side effect. Results and artifacts are memory-only, and
their reads recheck grants. Never automatically retry a failed action with a new
operation ID.

## Authoring conversation.v1

Advertise capability `{"name":"conversation","version":{"major":1,"minor":0}}`.
A conversation plugin must use execution scope `owner`, request the
`conversation.session` grant, and declare a required, non-secret `project`
setting. Ocman refuses a declaration missing any of the three, so the project you
approve in **Configure** is always the one project the plugin can reach.

The capability has exactly two moves. Inbound, the plugin emits an unsolicited
`message` event:

```json
{"type":"event","event":{"capability":"conversation","name":"message",
 "data":{"accountId":"T0WORKSPACE","threadId":"C123:1700000000.000100",
         "eventId":"Ev0A1B2C3","text":"ship it"}}}
```

`accountId` (the provider workspace) and `threadId` (the conversation) are
opaque identities ocman only compares and echoes back. An optional `project`
field is a claim, denied unless it matches the configured project; the directory
actually used always comes from configuration. Ocman keeps one managed session
per `(plugin, account, thread)` and remembers it in its own database, so every
later message in the thread continues the same session — across a restart of
ocman or of the plugin — and two workspaces that reuse a thread identity stay
isolated.

`eventId` must identify the *event*, not the delivery attempt, so that a
provider redelivering the same event repeats it. Ocman drops a repeat: a
duplicate delivery never creates a second session or a second prompt. A mention
that arrives while the session is still working is queued and sent when the turn
finishes, in arrival order — it does not interrupt the running turn.

Outbound, ocman calls method `reply` on the same capability when the session's
turn completes:

```json
{"accountId":"T0WORKSPACE","threadId":"C123:1700000000.000100","text":"Shipped."}
```

Replies are plain text in v1: only the newest assistant message's text parts,
capped at 64 KiB. Reasoning, tool, and file parts are excluded. The call carries
a 30-second deadline and a stable operation ID. Return `{}` on success.

### Reply delivery

A completed reply is durable work, not a best-effort call. Ocman writes it to a
delivery outbox before contacting the plugin, so a disconnect, a plugin crash or
a restart of ocman retries the reply instead of losing it. Each delivery has an
immutable id that doubles as its sequence number and is passed to the plugin as
the call's `operationId`.

- **Ordering.** Replies for one conversation are delivered strictly in
  sequence, one at a time: a later reply never overtakes an earlier one. Every
  conversation is its own ordering group, so one failing thread never holds up
  another's replies.
- **Duplicate guarantee.** Delivery is **at-least-once**. The acknowledgment
  happens after the call returns, so a crash in that window replays the same
  delivery with the *same* `operationId`. A provider adapter is expected to
  recognize a repeat and not post a second visible message. A repeated idle
  edge for the same completed turn, by contrast, never produces a second
  delivery: that is deduplicated durably on the host side.
- **Retries.** Failures retry with exponential backoff from 5 seconds to a
  5-minute ceiling. Provider-specific cooldowns (Slack's `Retry-After`) are
  absorbed inside the plugin, which knows its provider's limits.
- **Dead letters.** After 6 failed attempts a delivery stops retrying and waits
  for a decision under **Settings → Plugins → Reply delivery**: *Retry
  delivery* puts it back at the head of its conversation, *Discard reply* drops
  it and lets the conversation continue. Its own conversation stays paused until
  then, rather than delivering out of order.
- **Backpressure.** The backlog is capped per plugin (500 undelivered replies,
  8 MiB of reply text). At either cap ocman stops accepting *new* conversation
  messages — visibly, in the same panel — instead of dropping replies it already
  owes. A message refused this way is not marked as seen, so the provider's
  redelivery is accepted once the backlog drains.

Retry and discard are privileged, localhost-only controls, like every other
plugin mutation.

A revoked grant, a disabled plugin, or an unresolvable project denies both
directions, and denials never reach the provider. A denial that arrives while a
reply is already owed pauses that reply rather than failing it: the delivery
stays queued, unattempted, and keeps its full retry budget, so disabling a
connector for an afternoon does not turn every owed reply into a dead letter.
Re-enabling it, or restoring the grant, delivers exactly the replies that were
owed — nothing that was denied in between is replayed, because nothing was
attempted. Mappings, queued replies and private data are never deleted by a
denial.

### Attention notices

A thread only ever sees completed answers, so without help a session blocked on
a permission prompt looks exactly like one that is still thinking. Ocman posts a
short notice into the thread when its session cannot proceed on its own, or when
a turn ends somewhere other than an answer:

| Notice | When |
|---|---|
| Needs a permission decision | A permission prompt has been left for you — either auto-approve is off for the session, or it declined to approve |
| Waiting on an answer | The agent asked a question, which is never auto-answered |
| Ended with an error | The last turn failed |
| Could not be delivered | A message was accepted from the provider but no session could be reached for it |

Four things are worth knowing about them.

**Decisions still happen in ocman.** A notice is a pointer, never a prompt: it
carries a sentence and a link, and there is no way to approve, deny or answer
anything from the thread. Permission provenance and the auto-approve rules are
untouched — a thread cannot become a second approval channel.

**They follow the auto-approve decision, not the prompt.** A permission the
judge approves by itself produces no notice at all. Notices only appear once
ocman has established that a prompt is genuinely yours to answer, so a session
with auto-approve on stays quiet through the work it can do unattended.

**They carry no session content.** A notice never includes the permission text,
the command, the patterns, the tool metadata, the error message or any part of
the transcript — a provider thread is usually a wider audience than the
session's own operator. To see what is being asked, follow the link.

**They resolve, they are not retracted.** Nothing posted into a thread is ever
edited or deleted. A notice is resolved by what follows it: answer the prompt in
ocman, the turn continues, and the completed reply lands underneath. Each notice
is keyed on the prompt's own request id, so re-observing the same prompt — after
a stream reconnect, a replayed notification, or an ocman restart — adds nothing.
A second, different prompt in the same session is a new notice.

### Linking back: set a public base URL

Every notice links to the session it is about. Ocman builds that link from
`--public-base-url` / `OCMAN_PUBLIC_BASE_URL` (see
[Configuration](../../configuration/)). **Set it before pointing a conversation
plugin at a provider.** Without it ocman falls back to its own listen address,
which is normally `http://localhost:8228` — fine for you on this machine, and
useless to anyone else reading the thread.

Use the address a reader's browser would use: your Tailscale or reverse-proxy
hostname, for example `OCMAN_PUBLIC_BASE_URL=https://ocman.example.ts.net`. A
base URL with a subpath works too. Ocman does not publish anything at that
address on your behalf — the value only shapes the links it writes.

## Slack

The bundled [Slack plugin](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/examples/ocman-plugin-slack/main.go)
connects a Slack thread to a session over Socket Mode. It needs no inbound
network exposure and no relay.

Create a Slack app (**From scratch**, or paste this manifest):

```yaml
display_information:
  name: ocman
features:
  bot_user:
    display_name: ocman
oauth_config:
  scopes:
    bot:
      - app_mentions:read
      - chat:write
settings:
  event_subscriptions:
    bot_events:
      - app_mention
  socket_mode_enabled: true
```

Then, in the app's settings:

1. **Basic Information → App-Level Tokens**: generate a token with the
   `connections:write` scope. It starts with `xapp-`.
2. **Install App**: install to the workspace and copy the bot token, which
   starts with `xoxb-`.
3. Invite the bot to the channel you want to use.

Build and install the executable on the machine that owns the project:

```sh
go build -o "$HOME/.local/share/ocman/plugins/ocman-plugin-slack" ./examples/ocman-plugin-slack
```

Rescan in **Settings → Plugins**, then **Configure**:

| Setting | Value |
| --- | --- |
| `project` | Absolute path of the one project sessions may run in |
| `appToken` | App-level `xapp-` token (write-only secret) |
| `botToken` | Bot `xoxb-` token (write-only secret) |
| `allowedUsers` | Comma-separated Slack user IDs allowed to drive sessions |

Enable the plugin and approve the `conversation.session` grant. `@ocman ship it`
in a channel or thread now starts a session in that project, and the assistant's
completed reply appears in the same thread.

Both tokens use the host's write-only secret handling: they are delivered only on
file descriptor 3, never appear in logs, results, or error text, and are redacted
from captured stderr. `allowedUsers` fails closed — an empty list authorizes
nobody. Only `app_mention` events are subscribed, so every inbound message is an
explicit mention; bot messages and unauthorized users are dropped silently.

On delivery, the plugin honours Slack's rate limits: a `429` posted nothing, so
it waits out `Retry-After` (bounded) and retries in place, then hands the
delivery back to ocman's own backoff. A post whose outcome is *unknown* — a
transport failure, or a Slack 5xx — is deliberately not repeated: the plugin
remembers that operation and absorbs ocman's next attempt, because a duplicate
message in a thread everyone can see is worse than a reply that may already be
there. That memory is per process, so a plugin restart inside the retry window
can still post a reply twice.

## Remote ownership

Every installation is identified by its owner and plugin ID. Install the binary
on each machine that needs it; the hub neither copies executables nor persists
remote secrets. Settings projects the selected owner's catalog, grants,
configuration, health, and redacted stderr over authenticated gRPC. Secret updates
pass through the hub to the owner, while responses expose only presence flags.

For project/session contexts, owner-scoped actions run on the project owner and
hub-scoped actions run on the hub. Global placements resolve to the hub, as does
the `global` execution scope. The installation's `ownerId` is distinct from the
project/session `context.ownerId`. A disconnected explicit owner fails closed,
including when a hub action targets its project. Older remotes without plugin
management show an unavailable state.

The browser talks only to the hub. Remote calls use the closed `PluginOperation`
RPC, not a raw plugin protocol tunnel. Each owner applies its own grants,
confirmation, operation receipts, process supervision, and artifact checks.

Conversation plugins are owner-scoped by declaration, so a connector belongs to
the machine it is installed on. Give each installation its own provider app and
credentials: two machines configured with the same Slack app would both open a
Socket Mode connection and each receive an arbitrary half of the events. Thread
mappings, inbound deduplication, and reply delivery all live in the owner's own
`state.db`, and a conversation's session is always created on that owner — never
on a machine inferred from the project inventory, and never on the hub as a
fallback. The hub's only role is projecting Settings, which fails visibly when
the owner is disconnected.

## Diagnostics and removal

Settings lists up to 128 rejected executable filenames and safe host errors from
the latest scan, including invalid descriptions, timeouts, and identity changes.
Rescanning replaces this list; plugin stdout is never included in diagnostics.

Use **Refresh health** to inspect status, restart count, and the safe last error.
**Load recent stderr** shows bounded output from the current and most recently
stopped process. Each capture is capped at 64 KiB and redacts launch tokens and
current/historical secrets. Raw stderr stays out of SQL, telemetry, and API errors.
Redaction cannot protect secrets deliberately encoded by trusted native code.

Management mutations and stderr reads require a loopback peer and safe browser
origin in addition to configured authentication. If those controls fail through
a reverse proxy, use ocman's local endpoint. Action endpoints use normal
authentication and origin protection.

| Symptom | Check |
| --- | --- |
| No plugin discovered | Selected owner, directory override, executable name/permission, regular file, describe handshake and exit |
| Conflict or approval lost | Duplicate IDs, changed binary/declaration, attempted ID change at a registered path; correct files and rescan |
| Enabled but unhealthy | Recent stderr, configuration initialization, stdout framing, cancellation; repair and explicitly retry |
| Action missing | Placement, selected owner, negotiated capability, enablement, and required grants |
| Owner unavailable | Remote connection and that owner's plugin-management support |

**Revoke grants** clears approvals and stops in-flight work before restarting the
process. It denies affected actions and cached results, but cannot recall context
already delivered. Use **Disable** to stop execution while retaining configuration,
grants, and private data.

To uninstall, disable the plugin, remove its executable on the owner, and rescan.
Missing registrations remain visible as removed, retaining configuration and data.
To erase those too, use **Remove data** and confirm permanent removal while the
plugin is disabled. This deletes configuration, secrets, grants, and private data;
it does not delete the executable. A remaining executable is rediscovered as
disabled on the next scan. Operation receipts survive deletion to prevent
uncertain action replay.

## Development workflow

Use the optional [Go SDK](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/sdk/plugin/README.md)
at `github.com/NoUseFreak/ocman/sdk/plugin`, or implement the NDJSON contract in
another language. `plugin.Run` handles the lifecycle and `plugin.ActionHandler`
validates action dispatch/results. `plugin.RunWithEvents` adds a plugin-initiated
event source, and `plugin.ConversationHandler` plus `plugin.NewConversationMessage`
cover both conversation directions. Plugins with settings read fd 3 before calling
`Run` in serve mode. The deterministic
[fixture](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/examples/ocman-plugin-fixture/main.go)
demonstrates both modes, actions, cancellation, and safe failures.

From the ocman checkout, run:

```sh
go test ./sdk/plugin/... ./internal/plugins
go test ./internal/server ./internal/remote ./internal/state -run Plugin
```

External plugins can use `sdk/plugin/conformance.Run` for executable lifecycle
checks, `RunActionGrants` for broker grant, minimization, deduplication, and
revocation checks, and `RunConversationGrants` for the conversation declaration
contract plus grant and project denials. Supply deterministic, side-effect-free
test calls. Rebuild, rescan, review the new checksum, and enable again for a
manual palette check.

## Future work

The current release supports native processes, `action.v1`, and
`conversation.v1`. The following are future work, not available plugin
capabilities or integrations:

- Platform-provider capability.
- Iframe UI contributions.
- Relay inbox capability, separate from existing core Inbox and webhook features.
- A plugin registry and automatic updates.
- Publisher signatures and process sandboxing.
- A Codex provider.
- Conversation images, attachments, streaming replies, more than one project per
  plugin, and answering permission prompts from the thread.
