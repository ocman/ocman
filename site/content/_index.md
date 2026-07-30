---
title: ocman
layout: hextra-home
---

{{< hextra/hero-badge link="https://forgejo.nousefreak.be/dries/ocman/releases" >}}
  <span>Single binary · MIT licensed</span>
{{< /hextra/hero-badge >}}

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  One dashboard for&nbsp;<br class="hx:sm:block hx:hidden" />every coding-agent session
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  Browse, drive and split your OpenCode sessions from the browser —&nbsp;<br class="hx:sm:block hx:hidden" />across projects, worktrees and machines.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6">
{{< hextra/hero-button text="Get started" link="docs/introduction" >}}
</div>

<div class="hx:mt-6"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Session browser"
    subtitle="List, search, archive and replay every session, grouped by project with live status indicators."
    link="docs/features/"
  >}}
  {{< hextra/feature-card
    title="Live composer"
    subtitle="Send messages, answer permission prompts, abort and compact a running session. Streaming output renders live."
  >}}
  {{< hextra/feature-card
    title="Worktree sessions"
    subtitle="Spin up isolated git worktrees with /wt. One managed OpenCode instance per project serves them all."
  >}}
  {{< hextra/feature-card
    title="Multi-remote"
    subtitle="Attach other ocman instances over gRPC and manage every machine from one host-agnostic UI."
    link="docs/features/multi-remote"
  >}}
  {{< hextra/feature-card
    title="MCP server"
    subtitle="Let your agent split work into parallel child sessions and coordinate the results."
    link="docs/features/mcp"
  >}}
  {{< hextra/feature-card
    title="Workflows"
    subtitle="DAG workflows for migration campaigns: agent, command, approval, map and join nodes."
    link="docs/features/workflows"
  >}}
  {{< hextra/feature-card
    title="Diffs & changes"
    subtitle="Syntax-highlighted diffs inline in the thread, plus a Changes sidebar combining session edits with the working-tree diff."
  >}}
  {{< hextra/feature-card
    title="Stats dashboard"
    subtitle="Per-project metrics, wall-clock totals, token and pricing graphs, system stats."
  >}}
  {{< hextra/feature-card
    title="Runs anywhere"
    subtitle="Single Go binary, pure-Go SQLite, optional password auth, PWA install and a native macOS desktop build."
    link="docs/configuration/"
  >}}
{{< /hextra/feature-grid >}}

<div class="hx:mt-16"></div>

## Quick start

```sh
./ocman            # listens on 127.0.0.1:8228
```

Download a binary from the [releases page](https://forgejo.nousefreak.be/dries/ocman/releases),
or build from source with `make build`. Full flag and environment reference in
[Configuration](docs/configuration/).

![ocman dashboard](/sessions.png)
