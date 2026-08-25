---
title: Multi-remote
weight: 2
---

Manage coding-agent sessions running on several machines from one ocman
dashboard. One ocman acts as the hub. It attaches to other ocman instances
(the remotes) over a long-lived gRPC connection and shows every machine's
sessions in a single list. You can open, watch live, and drive any remote
session, sending prompts, answering permission and question prompts, aborting
and compacting, exactly as if it were local. Creating a session is
machine-aware: the hub picks the host where the project already lives, and
asks you to choose when it lives on more than one.

> Off by default. A plain `./ocman` with no remotes behaves exactly as
> before, and nothing below is required for single-machine use.

## How it works

Every ocman has a stable random instance ID and a remote-access token,
generated on first run and stored in `state.db`. A remote opts in to being
managed by starting its gRPC server with `-remote-listen`. The hub dials each
remote, authenticates with that remote's token, and registers its sessions
under a host badge.

The browser only ever talks to the hub over the normal REST/SSE API, and the
hub fans out to remotes over gRPC. Host-local work (tmux, git worktrees, the
in-app terminal, the agent process) always runs on the machine that owns it,
so the hub never reaches into a remote's filesystem directly. Opening the
terminal on a remote project's session opens a shell on the remote machine:
the hub tunnels the browser WebSocket to the owner over a bidirectional gRPC
stream, so keystrokes and PTY output cross the wire while the shell, cwd and
tmux windows all stay on the remote.

## Prerequisites

- ocman running on each machine you want to manage. The same version is
  safest; a protocol-version handshake flags incompatible builds.
- Network reachability from the hub to each remote's gRPC port. ocman does
  not manage networking, so use direct reachability, a VPN, or a trusted
  overlay such as Tailscale or WireGuard.
- TLS certificates, unless both machines sit on a trusted overlay.

## Step-by-step setup

### 1. On each remote machine, turn on remote access

Start ocman with `-remote-listen` set to the address its gRPC server
should bind. Use a routable address (not `127.0.0.1`) so the hub can
reach it across the network:

```sh
# On the workstation you want to manage remotely:
ocman -remote-listen 0.0.0.0:8230 \
  -remote-tls-cert cert.pem -remote-tls-key key.pem
```

This starts the gRPC server in addition to the normal web UI on `:8228`. The
remote's existing REST behaviour and localhost posture are unchanged.

> Pick any free port for the gRPC server; `8230` is just a convention.
> The web UI port (`-addr`, default `:8228`) is independent.

### 2. On each remote machine, copy its access token

Open the remote's own dashboard and go to **Settings → Remotes**. The
**This machine** entry shows the remote's instance ID and listen address.
Click **Reveal token** and copy the value.

You can also fetch it over the API on that machine:

```sh
curl -s -X POST http://localhost:8228/api/settings/remote-access/reveal-token
# -> { "token": "…" }
```

The token authenticates inbound gRPC connections. It is independent of
the web UI password (`OCMAN_AUTH_PASSWORD`).

### 3. On the hub, attach the remote

Open the hub's dashboard, go to **Settings → Remotes → Attach a remote**,
and enter:

- **Address.** Use `grpcs://workstation.lan:8230` to dial TLS. Plaintext
  requires an explicit `grpc://` address and `-remote-trusted-overlay` on
  the remote; use it only over Tailscale, WireGuard, or an equivalent private
  encrypted overlay.
- **Token.** The value you revealed in step 2.
- **Display name** (optional). A friendly label for the host badge.

Click **Add remote**. The hub dials, runs the handshake, and the row
flips to **connected**. The remote's sessions now appear in the unified
list with its host badge.

### 4. Use it

- **Unified list.** Local and remote sessions are interleaved, each tagged
  with the owning machine. A slow or offline remote never blocks the list; its
  last-known sessions show dimmed with an *offline* marker.
- **Open and drive.** Click any remote session to stream its transcript and
  use the composer, permission replies, abort and compact. These route to the
  owning remote and run there.
- **New session.** When you start a session for a project, the hub looks up
  which machines already have that project checked out. On exactly one
  machine it starts there automatically; on several it asks you to pick; on
  none it asks which machine to start on.

## Managing remotes

The hub's **Settings → Remotes** page lists each attached remote with
its health, hostname, reported instance ID, session count, and last-seen
time. Per-remote actions:

- **Reconnect.** Force a fresh dial. This also happens automatically with
  backoff after a transient drop.
- **Edit.** Change the display name or address, replace the token, or
  enable and disable the remote.
- **Remove.** Detach the remote. Its sessions disappear from the list.

The **This machine** entry is non-removable, and its address and token
cannot be edited.

## Security

- **Token auth is mandatory** on the gRPC channel. A missing or wrong
  token is rejected and shown as `auth-failed` on the hub.
- **TLS is the default requirement.** Start the remote with both
  `-remote-tls-cert` and `-remote-tls-key`, and use a `grpcs://` address on
  the hub. Plaintext requires both `-remote-trusted-overlay` on the remote
  and an explicit `grpc://` address on the hub; bare addresses are rejected.
- **Plaintext exposes the bearer token.** With `grpc://`, the remote-access
  token and all session traffic are unencrypted. Use this mode only when an
  encrypted trusted overlay such as Tailscale or WireGuard already protects
  the complete route between hub and remote. A private LAN alone is not an
  encrypted trusted overlay.
- Stored remote tokens on the hub are encrypted at rest with an app-local
  key and are never returned to the browser. That protects against casual
  disclosure, not against an attacker who can read both the state DB and the
  app-local secret. Ocman enforces owner-only filesystem permissions on these
  files at startup.

## Flags

| Flag | Default | Where | Description |
|------|---------|-------|-------------|
| `-remote-listen` | _(unset, off)_ | remote | Bind address for the remote-access gRPC server, e.g. `0.0.0.0:8230`. Empty disables it. |
| `-remote-tls-cert` | _(unset)_ | remote | TLS certificate file. Required with `-remote-tls-key` unless using a trusted overlay. |
| `-remote-tls-key` | _(unset)_ | remote | TLS key file. |
| `-remote-trusted-overlay` | `false` | remote | Explicitly allow plaintext only on a trusted overlay network. |

The hub needs no flags. You add remotes at runtime from the Settings page,
and ocman persists them in `state.db`.

## Troubleshooting

| Symptom | Likely cause |
|---------|--------------|
| Health shows `auth-failed` | Wrong token, or the remote regenerated its token. Reveal it again on the remote and update it on the hub (**Edit**). |
| Health shows `offline` | The hub can't reach the remote's gRPC address. Check the port, firewall, and overlay network. Sessions appear stale until it reconnects. |
| Health shows `incompatible-version` | Hub and remote are different ocman builds with an incompatible wire protocol. Align versions. |
| Remote connected but no sessions | The remote may simply have none, or its OpenCode database isn't where it expects. Check the remote's own dashboard. |
| New-session picker never finds a remote's project | The remote's project inventory refreshes on connect and periodically, so a freshly checked-out project may take a moment to appear. |

## Limitations (v1)

- Only the core new-session action is machine-aware. The Worktrees view and
  the PR/Issue sidebar's launch actions remain local-only.
- Auto-approve for a remote session runs on the owning remote, with that
  machine's settings, not the hub's.
- Stale sessions from an offline remote are kept in memory only. After a hub
  restart an offline remote shows no sessions until it reconnects.
