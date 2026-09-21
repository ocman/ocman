---
title: Slack integration
weight: 19
---

The bundled Slack plugin connects a Slack thread to an ocman session over
Socket Mode. It needs no inbound network exposure and no relay. Channel messages
require an explicit `@ocman` mention. Allowlisted users can also talk to ocman
from the app's Messages tab without mentioning it.

## Create the Slack app

Create an app **from a manifest** and paste
[`examples/ocman-plugin-slack/slack-app-manifest.yaml`](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/examples/ocman-plugin-slack/slack-app-manifest.yaml).
The manifest is the configuration blueprint and enables:

- Socket Mode.
- The App Home Messages tab and AI Assistant view.
- `app_mention` events for channels and `message.im` events for direct chats.
- The exact bot scopes: `app_mentions:read`, `assistant:write`, `chat:write`, and
  `im:history`.

The plugin reads no channel history. `assistant:write` only displays
`Thinking...` while a turn runs; the plugin clears it after posting the reply.

After creating the app:

1. Under **Basic Information → App-Level Tokens**, generate a token with the
   `connections:write` scope. It starts with `xapp-`.
2. Under **Install App**, install the app to the workspace and copy the bot
   token. It starts with `xoxb-`.
3. If Slack changed any scope after installation, reinstall the app and use the
   newly displayed bot token.
4. Invite the bot to each channel where it should receive mentions.

## Install and configure

Build and install the executable on the machine that owns the project:

```sh
make install-plugin PLUGIN=slack
```

A conversation plugin is owner-scoped. The binary, tokens, and project all live
on that host; the hub never copies them. In **Settings → Plugins**, select that
owner, rescan, and configure the Slack plugin:

| Setting | Value |
| --- | --- |
| `project` | Absolute path of the one project sessions may run in |
| `appToken` | App-level `xapp-` token |
| `botToken` | Bot `xoxb-` token |
| `allowedUsers` | Comma-separated Slack member IDs allowed to drive sessions |
| `agent` | Optional OpenCode agent, such as `build` |
| `model` | Optional `provider/model` for a new Slack thread |

Enable the plugin and approve the `conversation.session` grant. Set
`OCMAN_PUBLIC_BASE_URL` to the address Slack users can open before using the
integration; attention notices link back to that address.

## Conversation behavior

`@ocman ship it` in a channel or channel thread starts a session. A direct
message starts one without a mention. The first message becomes the Slack thread
root, completed replies stay in that thread, and follow-ups in the same thread
continue the same durable ocman session. A new top-level direct message starts a
new session.

Messages accepted while a turn is running are queued and sent to the session in
arrival order. Permission prompts, questions, and turn failures post a fixed
notice linking to ocman; Slack never receives permission details, errors, tool
output, or hidden model reasoning.

Both tokens use write-only secret handling. They are delivered only on file
descriptor 3, never appear in results or error text, and are redacted from
captured stderr. `allowedUsers` fails closed: an empty list authorizes nobody.
Bot messages, message edits, and unauthorized users are dropped silently.

Reply delivery is durable and at-least-once. Slack `429` responses honor
`Retry-After`. An uncertain transport or Slack 5xx outcome is not immediately
repeated because a duplicate visible reply is worse than one that may already
have landed.

## Verify the installation

Walk these steps in order before using a shared channel:

1. **Installed.** `make install-plugin PLUGIN=slack` prints the path and SHA-256.
   Rescan must show `org.ocman.slack` as disabled with the same checksum.
2. **Configured.** Save the project and tokens, enable the plugin, and approve
   `conversation.session`.
3. **Connected.** **Refresh health** should report healthy. Recent stderr should
   contain `slack socket connected`, never tokens or message content.
4. **Channel.** Mention `@ocman` in an invited channel. A session appears and
   the completed reply lands in the same thread.
5. **Direct chat.** Send a message from an allowlisted account in the app's
   Messages tab. The reply remains in that assistant thread.
6. **Follow-up.** Send another message in the same thread while a turn runs. It
   is answered after that turn rather than interleaved into it.
7. **Restart.** Restart ocman and continue the thread. It should reuse the same
   session and deliver any reply owed before the restart.

If nothing happens, confirm the channel invite, member ID allowlist, Socket Mode,
event subscriptions, and reinstallation after scope changes. Then inspect
**Refresh health** and **Load recent stderr**. `invalid_auth` indicates a token
problem; an `assistant.threads.setStatus` error affects only the thinking status,
not prompt or reply delivery.

## Test and remove

The automated path uses the real executable with mock Slack Web API and Socket
Mode endpoints:

```sh
go test ./examples/ocman-plugin-slack
go test ./internal/server -run Slack
```

These tests cannot validate workspace configuration, tokens, channel membership,
or Slack's rendered UI.

To remove the integration, disable the plugin, delete `ocman-plugin-slack` from
the owner's plugin directory, rescan, and use **Remove data** to erase tokens,
thread mappings, and undelivered replies. Revoke the tokens or delete the app in
Slack separately.
