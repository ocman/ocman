---
title: Beads
weight: 19
---

The optional Beads plugin adds the current project's ticket tree to the session
sidebar. It runs `bd` on the project's owning machine, including for remote
sessions. Ocman renders the returned tree; the plugin supplies no browser code.

## Install

Install Beads 1.1.0 or newer on the project owner, then build the bundled plugin
from an ocman checkout on that same machine:

```sh
make install-plugin PLUGIN=beads
```

In **Settings → Plugins**:

1. Select that machine as the plugin owner and click **Rescan plugins**.
2. Configure **Absolute path to bd**, using the absolute executable path reported
   by `command -v bd`. Plugins do not inherit ocman's shell environment.
3. Review and approve the plugin's `pane.project` grant, then enable it. This grant
   lets ocman pass the active project directory to the plugin for a pane read.

Enable the plugin separately on each owner whose sessions need Beads. A disabled
or uninstalled plugin contributes no sidebar tab. Open the plugin tab after
updating; the former built-in tab's preferences are not mapped by core code.

## Viewing tickets

Opening **Beads** reads the project's default `bd list` rows and their
`parent-child` dependencies. The tree keeps the status circles, priority badges,
issue types, ticket IDs and nesting. Default CLI filters and its result limit
remain in effect.

The pane refreshes every 30 seconds while open and offers a manual refresh.
Closing it stops polling and cancels a pending read. Switching projects or
refocusing the browser while it is closed does not run `bd`. Listing sidebar
contributions only reads plugin declarations, never probes Beads workspaces.

A project without a supported Beads workspace shows an unavailable message after
you open the pane. A refresh failure keeps the last tree with a retry control.
The tab remains available while the plugin is enabled, so workspace discovery
does not need to run in the background.

Commands use fixed arguments, a five-second deadline per command, and bounded
output. Ticket data stays in Beads; ocman does not persist it. Commands use
`--readonly` where supported, with the same best-effort semantics as the CLI.

The former `/api/project/beads-status` route and host-specific Beads operation
have been removed. Sidebar reads use the owner-routed plugin API instead.
