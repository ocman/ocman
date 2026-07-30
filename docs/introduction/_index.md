---
title: Introduction
weight: 1
---

Ocman is a web dashboard for browsing and driving your coding-agent
sessions. It reads [OpenCode](https://github.com/anomalyco/opencode)'s
session database **read-only** and talks to running OpenCode instances over
their HTTP API, so it never owns your data and never gets in the way of the
CLI.

One binary, no runtime dependencies (pure-Go SQLite), listening on
`127.0.0.1:8228` by default.

## Install

```sh
# Download a binary from the releases page, then:
./ocman
# Open http://localhost:8228
```

Prebuilt binaries — including a macOS `.dmg` desktop build — are on the
[releases page](https://forgejo.nousefreak.be/dries/ocman/releases). To build
from source you need Go 1.24+ and Node.js 22+:

```sh
make build && ./ocman
```

{{< callout type="info" >}}
The macOS DMG is not signed or notarized. Right-click `ocman.app` → **Open**
on first launch, or run `xattr -dr com.apple.quarantine /Applications/ocman.app`.
{{< /callout >}}

## Your first session

Existing sessions show up immediately — grouped by project, searchable,
replayable. To get an *interactive* session (composer, permission replies,
abort), let ocman launch it:

1. Press <kbd>⌘K</kbd> to open the command palette.
2. Run `/wt` to create a git worktree session, or use the project's
   **Worktrees** view.

Ocman keeps one managed OpenCode instance per project and connects to it
automatically, so parallel worktree sessions get isolated files and staging
areas without a process per worktree.

If you prefer to run OpenCode yourself, start it with an explicit port so
ocman can discover it:

```sh
opencode --port 0   # let OpenCode pick a free port
```

Without `--port`, externally launched sessions stay readable but the composer
is disabled.

## Where to next

{{< cards >}}
  {{< card link="../features" title="Features" subtitle="What ocman can do." >}}
  {{< card link="../configuration" title="Configuration" subtitle="Flags, env vars, auth." >}}
  {{< card link="../faq" title="FAQ" subtitle="Quick answers." >}}
{{< /cards >}}
