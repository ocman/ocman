---
title: Introduction
weight: 1
---

Ocman is a web dashboard for browsing and driving your coding-agent
sessions. It opens [OpenCode](https://github.com/anomalyco/opencode)'s session
database read-only and talks to running OpenCode instances over their HTTP
API. OpenCode keeps ownership of the data, and you can keep using the CLI on
the same sessions at the same time.

One binary (pure-Go SQLite), listening on `127.0.0.1:8228` by default.

## Requirements

Ocman checks these on startup (`GET /api/doctor`). A missing required item
shows a setup panel; a missing optional one shows a dismissable banner and
hides only the feature it unlocks.

Required:

- **OpenCode or Claude Code**. For OpenCode, install https://opencode.ai and
  run it once to create its database. For Claude Code, install the `claude`
  CLI and log in. Both platforms are enabled by default; a missing OpenCode
  database does not prevent using Claude.
- **git**: `brew install git`.

Optional:

| Tool | Unlocks | Hint |
|------|---------|------|
| tmux | launching managed OpenCode or Claude sessions, /wt worktree sessions and browser terminals | `brew install tmux` |

Apps launched from Finder get a minimal PATH. Ocman merges in your login
shell's PATH at startup; when that fails (the shell exits with an error or
takes longer than 10 seconds) the setup banner shows the reason. Fix the
shell or install tools in /usr/local/bin or /opt/homebrew/bin, then restart
ocman.

## Install

```sh
# Download a binary from the releases page, then:
./ocman
# Open http://localhost:8228
```

Prebuilt binaries, including a macOS `.dmg` desktop build, are on the
[releases page](https://forgejo.nousefreak.be/dries/ocman/releases). To build
from source you need Go 1.24+ and Node.js 22+:

```sh
make build && ./ocman
```

{{< callout type="info" >}}
The macOS DMG is not signed or notarized. Right-click `ocman.app` and choose
**Open** on first launch, or run
`xattr -dr com.apple.quarantine /Applications/ocman.app`.
{{< /callout >}}

## Your first session

Existing sessions show up immediately, grouped by project, searchable and
replayable. To get an *interactive* session (composer, permission replies,
abort), let ocman launch it:

1. Press <kbd>⌘K</kbd> to open the command palette.
2. Run `/wt` to create a git worktree session, or use the project's
   **Worktrees** view.

Ocman keeps one managed OpenCode instance per project and connects to it
automatically, so parallel worktree sessions get isolated files and staging
areas without a process per worktree.

With OpenCode v2, ocman instead runs one server for the whole machine (see
[Configuration](../configuration#opencode-v2)). Start conversations through
ocman to use its managed instances. Instances you start yourself are never
discovered or adopted. Saved sessions remain visible from the database;
interactive controls require a connection to an ocman-managed instance.

## Where to next

{{< cards >}}
  {{< card link="../features" title="Features" subtitle="What ocman can do." >}}
  {{< card link="../configuration" title="Configuration" subtitle="Flags, env vars, auth." >}}
  {{< card link="../faq" title="FAQ" subtitle="Quick answers." >}}
{{< /cards >}}
