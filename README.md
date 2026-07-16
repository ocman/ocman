# ocman

[![CI](https://forgejo.nousefreak.be/dries/ocman/actions/workflows/ci.yml/badge.svg)](https://forgejo.nousefreak.be/dries/ocman/actions?workflow=ci.yml)
[![Release](https://forgejo.nousefreak.be/dries/ocman/actions/workflows/release.yml/badge.svg)](https://forgejo.nousefreak.be/dries/ocman/actions?workflow=release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A web dashboard for browsing and driving your coding-agent sessions.
Supports [OpenCode](https://github.com/anomalyco/opencode) and [Claude Code](https://claude.ai/code).

![ocman dashboard](docs/sessions.png)

## Features

- **Session browser** — List, search, archive, and replay every session. Sessions are grouped by project with status indicators and a `+` button to start new ones.
- **Live composer** — Send messages, respond to permission prompts, abort, and compact a running session straight from the browser. Streaming output renders live.
- **Bash mode** — Prefix a message with `!` to run a shell command in the session's working directory and capture the output.
- **Command palette** — Unified ⌘K palette for jumping between sessions, settings, and actions, with in-app notifications.
- **Slash commands** — `/new [title]` to create a session, `/clear` to archive the current one and start fresh, plus keyboard-driven session rename.
- **Tmux integration** — Launch or auto-launch an OpenCode instance inside tmux directly from the UI.
- **Multi-remote** — Attach other ocman instances over the network and manage every machine's sessions from one dashboard, with a host badge per session and machine-aware new-session creation. See [docs/multi-remote.md](docs/multi-remote.md).
- **Diff & changes view** — Syntax-highlighted diffs inline in the thread, plus a *Changes* sidebar combining session edits with the working-tree `git` diff.
- **Stats dashboard** — Per-project metrics, wall-clock totals, token/pricing graphs, and system stats.
- **Model picker** — Per-platform favorites and a refreshable catalog so new models appear without restarting.
- **Auth** — Optional password gate with rate-limited logins and persistent signed cookies. Off by default for localhost.
- **PWA** — Installable as a Progressive Web App.

## Quick start

```sh
# Run directly against the OpenCode database
./ocman
# Open http://localhost:8228
```

Download the latest binary from the [releases page](https://forgejo.nousefreak.be/dries/ocman/releases),
or build from source with `make build` (requires Go 1.24+ and Node.js 22+).

### macOS desktop app

The releases page also ships `ocman-darwin-arm64.dmg` — a drag-to-Applications
installer for the native desktop build. The DMG is **not signed or notarized**,
so macOS Gatekeeper will refuse to open the app on first launch with a
"cannot be opened because the developer cannot be verified" warning.

To bypass:

1. Open the DMG and drag `ocman.app` to `/Applications`.
2. In Finder, right-click (or Control-click) `ocman.app` → **Open**.
3. Confirm **Open** in the dialog. macOS remembers the choice and subsequent
   launches work normally.

Alternative — strip the quarantine attribute from a terminal:

```sh
xattr -dr com.apple.quarantine /Applications/ocman.app
```

For interactive features (composer, permission replies, abort) start OpenCode with an explicit port:

```sh
opencode --port 0   # let OpenCode pick a free port
```

Ocman auto-discovers listening OpenCode processes and connects. Without `--port`, sessions are
still readable but the composer stays disabled — the UI shows a hint to re-launch with `--port 0`.

## Configuration

```sh
./ocman                              # default: listens on 127.0.0.1:8228
./ocman -addr localhost:9090         # custom listen address
./ocman -db /path/to/opencode.db     # custom OpenCode database path
./ocman -platforms opencode,claude-code  # enable multiple platforms
```

See [docs/configuration.md](docs/configuration.md) for the full flag and environment variable reference, including authentication setup.

## Optional agent integration

Ocman works as a dashboard without any agent integration. It also embeds an
optional, localhost-only **MCP server** so an AI agent can split work into
child OpenCode sessions or isolated git worktrees and coordinate between them
(`new_session`, `get_session_status`, `list_child_sessions`,
`cancel_session`, and parent/child message tools).

Point your OpenCode config at `http://localhost:8229/mcp` (or
`http://localhost:8228/mcp` via the `make dev` proxy). See the
[MCP integration guide](docs/mcp.md) for setup, the full tool list, and the
optional session-splitting skill.

## Managing sessions across machines

Run ocman on several machines and manage them all from one **hub**. On
each machine you want to manage remotely, start ocman with a gRPC listen
address:

```sh
ocman -remote-listen 0.0.0.0:8230 \
  -remote-tls-cert cert.pem -remote-tls-key key.pem
```

Then on the hub, open **Settings → Remotes → Attach a remote**, paste the
remote's address and its access token (revealed from the remote's own
**Settings → Remotes** page), and its sessions join the unified list with
a host badge. Opening, driving, and creating sessions all route to the
owning machine automatically.

This is off by default — a plain `./ocman` with no remotes is unchanged.
See the step-by-step [multi-remote guide](docs/multi-remote.md) for TLS,
security notes, and troubleshooting.

## Documentation

- [Configuration](docs/configuration.md) — flags, env vars, authentication
- [Multi-remote support](docs/multi-remote.md) — manage sessions across machines
- [MCP integration](docs/mcp.md) — agent-driven session splitting
- [Workflows](docs/workflows.md) — authoring and safely running migration campaigns
- [Contributing](docs/contributing.md) — architecture, project structure, development workflow

## License

This project is licensed under the MIT License. See [LICENSE](LICENSE) for details.
