# ocman

[![CI](https://forgejo.nousefreak.be/dries/ocman/actions/workflows/ci.yml/badge.svg)](https://forgejo.nousefreak.be/dries/ocman/actions?workflow=ci.yml)
[![Release](https://forgejo.nousefreak.be/dries/ocman/actions/workflows/release.yml/badge.svg)](https://forgejo.nousefreak.be/dries/ocman/actions?workflow=release.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

A web dashboard for browsing and driving your coding-agent sessions.
Supports [OpenCode](https://github.com/anomalyco/opencode).

![ocman dashboard](docs/sessions.png)

## Features

- **Session browser.** List, search, archive, and replay every session. Sessions are grouped by project, with status indicators and a `+` button to start new ones.
- **Live composer.** Send messages, answer permission prompts, abort, and compact a running session from the browser. Streaming output renders live.
- **Bash mode.** Prefix a message with `!` to run a shell command in the session's working directory and capture the output.
- **Command palette.** One ⌘K palette for jumping between sessions, settings, and actions, with in-app notifications.
- **Slash commands.** `/new [title]` creates a session, `/clear` archives the current one and starts fresh. Renaming a session is keyboard-driven too.
- **Tmux integration.** Launch or auto-launch an OpenCode instance inside tmux from the UI.
- **Scheduled prompts.** Run one stored project prompt later in a fresh OpenCode session. Status survives a restart and links back to the session it created.
- **Multi-remote.** Attach other ocman instances over the network and manage every machine's sessions from one dashboard. Every session carries a host badge, and new-session creation knows which machine has the project. See [Multi-remote](docs/features/multi-remote.md).
- **Diff and changes view.** Syntax-highlighted diffs inline in the thread, plus a *Changes* sidebar that combines session edits with the working-tree `git` diff.
- **Stats dashboard.** Per-project metrics, wall-clock totals, token and pricing graphs, system stats.
- **Model picker.** Per-platform favorites and a refreshable catalog, so new models appear without a restart.
- **Auth.** Optional password gate with rate-limited logins and persistent signed cookies. Off by default for localhost.
- **PWA.** Installable as a Progressive Web App.

## Quick start

```sh
curl -fsSL https://forgejo.nousefreak.be/dries/ocman/raw/branch/main/install.sh | bash -s -- install
# Open http://localhost:8228
```

The script checks your toolchain (git, go, node, pnpm), builds from source into
`~/.local/share/ocman/src`, installs the binary to `~/.local/bin/ocman`, and
starts it in the background. It also installs itself as `ocman-ctl`:

```sh
ocman-ctl status | start | stop | restart | logs
ocman-ctl update              # pull, rebuild, restart
ocman-ctl uninstall           # add --purge to also delete state.db
ocman-ctl doctor              # dependency check only
```

Override with `OCMAN_ADDR`, `OCMAN_PREFIX`, `OCMAN_BRANCH`, `OCMAN_SRC`.

Alternatively, download the latest binary from the
[releases page](https://forgejo.nousefreak.be/dries/ocman/releases), or build
from source with `make build` (requires Go 1.24+ and Node.js 22+).

### macOS desktop app

The releases page also ships `ocman-darwin-arm64.dmg`, a drag-to-Applications
installer for the native desktop build. The DMG is not signed or notarized, so
Gatekeeper refuses to open the app on first launch with a "cannot be opened
because the developer cannot be verified" warning.

To get past it:

1. Open the DMG and drag `ocman.app` to `/Applications`.
2. In Finder, right-click (or Control-click) `ocman.app` and choose **Open**.
3. Confirm **Open** in the dialog. macOS remembers the choice and later
   launches work normally.

Or strip the quarantine attribute from a terminal:

```sh
xattr -dr com.apple.quarantine /Applications/ocman.app
```

The easiest way to get interactive sessions (composer, permission replies, abort) is to launch
them from ocman itself, using the command palette (`/wt` for worktrees) or the per-project
Worktrees view. Ocman manages one OpenCode instance per project and connects automatically.

If you prefer running OpenCode yourself, start it with an explicit port so ocman can discover it:

```sh
opencode --port 0   # let OpenCode pick a free port
```

Without `--port`, externally launched sessions are still readable but the composer stays disabled.

## Configuration

```sh
./ocman                              # default: listens on 127.0.0.1:8228
./ocman -addr localhost:9090         # custom listen address
./ocman -db /path/to/opencode.db     # custom OpenCode database path
./ocman -platforms opencode,claude-code  # enable multiple platforms
```

See [Configuration](docs/configuration/_index.md) for the full flag and environment variable reference, including authentication setup.

## Optional agent integration

You don't need any of this to use ocman as a dashboard. It also embeds an
optional, localhost-only MCP server so an AI agent can split work into child
OpenCode sessions or isolated git worktrees and coordinate between them
(`new_session`, `await_session_result`, `get_session_status`, `list_child_sessions`,
`cancel_session`, and parent/child message tools).

Point your OpenCode config at `http://localhost:8229/mcp` (or
`http://localhost:8228/mcp` via the `make dev` proxy). See the
[MCP integration guide](docs/features/mcp.md) for setup, the full tool list, and the
optional session-splitting skill.

## Managing sessions across machines

Run ocman on several machines and manage them all from one hub. On each
machine you want to manage remotely, start ocman with a gRPC listen
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

This is off by default. A plain `./ocman` with no remotes is unchanged.
See the step-by-step [multi-remote guide](docs/features/multi-remote.md) for TLS,
security notes, and troubleshooting.

## Documentation

`docs/` mirrors the site structure: five chapters, same layout in the repo
and on the rendered site (`make docs`).

- Introduction: [what ocman is, install, first session](docs/introduction/_index.md)
- Features: [overview](docs/features/_index.md) ·
  [multi-remote](docs/features/multi-remote.md) ·
  [MCP integration](docs/features/mcp.md) ·
  [workflows](docs/features/workflows.md) ·
  [scheduled prompts](docs/features/scheduled-prompts.md)
- Configuration: [flags, env vars, authentication](docs/configuration/_index.md)
- FAQ: [short answers](docs/faq/_index.md)
- Other: [architecture](docs/other/architecture.md) ·
  [contributing](docs/other/contributing.md) ·
  [profiling](docs/other/profiling.md) ·
  [releases](docs/other/releases.md)

## License

MIT. See [LICENSE](LICENSE).
