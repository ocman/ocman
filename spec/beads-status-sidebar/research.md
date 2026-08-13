# Minimal Read-Only Beads Status Sidebar: Research

Date: 2026-07-23

## Scope and source baseline

This is investigation only. It proposes no implementation.

Confirmed product scope:

- The pane mirrors the compact hierarchical output of `bd list`: status, ID, priority, title, and parent/child nesting.
- It follows `bd list` defaults and excludes closed tickets.
- It refreshes every 30 seconds while the pane is visible.
- Once a Beads workspace is detected, command failures keep the tab visible with a retryable error.
- Unsupported older Beads versions are treated as unavailable and remain hidden.
- Best-effort `--readonly` behavior is acceptable even though Beads may perform startup migration or start Dolt.
- The entire feature remains absent until a supported Beads workspace is discovered.

- Context7 identified the official Beads project as `/gastownhall/beads` (High reputation, benchmark 83.26). Its answer confirmed `bd status`, `bd doctor`, and `bd blocked`, but did not cover enough of the JSON or storage contract, so the investigation continued in the official upstream source.
- Beads citations below are pinned to official commit [`2f9367d6`](https://github.com/gastownhall/beads/tree/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f), which declares version 1.1.0.
- Ocman citations refer to local commit `f531b3fd`; paths and line ranges are included because this repository is hosted on Forgejo.

## Facts

### Beads has an appropriate aggregate command

`bd status --json --no-activity` returns one aggregate object. Its `summary` contains total, open, in-progress, closed, blocked, deferred, ready, pinned, closeable-epic, and average-lead-time values. `--no-activity` is documented as the faster status path; current source already returns no activity data, but using the flag makes the intent and compatibility explicit. [status command](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/status.go#L12-L27) [status execution](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/status.go#L65-L107) [statistics schema](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/internal/types/types.go#L1259-L1271)

Beads provides a versioned JSON envelope when `BD_JSON_ENVELOPE=1` is set:

```json
{
  "schema_version": 1,
  "data": {
    "summary": {
      "total_issues": 12,
      "open_issues": 4,
      "in_progress_issues": 2,
      "blocked_issues": 1,
      "deferred_issues": 1,
      "closed_issues": 4,
      "ready_issues": 3
    }
  }
}
```

The envelope is opt-in now and is planned as the v2 default. Consumers should ignore unknown fields; schema versions change for incompatible shape changes. [JSON contract](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/reference/json-schema.md#L11-L65) [consumer guidance](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/reference/json-schema.md#L205-L217)

If individual work items are wanted later, `bd ready --limit 10 --json` has a documented stable issue schema and returns open, unblocked work. It is not needed for a minimal status pane. [ready JSON schema](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/reference/json-schema.md#L140-L161)

### `bd list` can supply the rows, but not the complete tree in one JSON response

`bd list --json` supplies machine-readable issue rows. Its default limit is 50 and its default filtering excludes closed issues, templates, gates, and infrastructure beads. The fields needed by the pane are `id`, `title`, `status`, `priority`, and `issue_type`. [`bd list` flags](https://github.com/gastownhall/beads/blob/main/cmd/bd/list.go)

The terminal tree is a separate rendering path: Beads loads all dependency records and passes them with the issue rows to its pretty renderer. The `bd list --json` path returns issue rows with counts, but not those dependency records. Ocman therefore must not parse ANSI/tree text or infer parents from dotted IDs. It should fetch the list as JSON and obtain `parent-child` relationships through Beads' dependency command, then build the small tree in memory. [`bd list` JSON and pretty paths](https://github.com/gastownhall/beads/blob/main/cmd/bd/list.go) [`bd dep list`](https://github.com/gastownhall/beads/blob/main/website/versioned_docs/version-1.0.4/cli-reference/dep.md)

### CLI discovery is authoritative; files are not

`bd -C <directory>` validates the directory, resolves it absolutely, discovers the applicable Beads workspace, and selects that workspace via `BEADS_DIR`. This handles ancestor discovery and worktree layouts without changing ocman's process working directory. It also overrides inherited `BEADS_DIR`, `BEADS_DB`, and `BD_DB` selection for the command. [directory selection](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/main.go#L638-L674)

`bd where --json` reports the resolved workspace path and exits non-zero when no workspace exists; the no-workspace JSON code is `no_beads_directory`. [where result](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/where.go#L17-L23) [missing workspace behavior](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/where.go#L54-L67)

Direct file parsing is not viable:

- Embedded mode stores source-of-truth data in `.beads/embeddeddolt/`; server mode uses `.beads/dolt/`. [storage modes](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/README.md#L127-L140)
- `.beads/issues.jsonl` is an optional export/interchange artifact, not the database or a backup, so it may be absent or stale. [official FAQ](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/reference/faq.md#L108-L126)
- Discovery supports redirects and worktree sharing, making a literal `<dir>/.beads` existence check incomplete. [worktree behavior](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/reference/worktrees.md#L7-L27)

### “Read-only” is not currently a strict no-write guarantee

Beads exposes a global `--readonly` flag that blocks explicit write operations, and its read commands are opened differently to avoid normal database modifications. [global flags](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/CLI_REFERENCE.md#L294-L311) [read-command classification](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/main.go#L125-L150)

However, strict filesystem non-mutation is not guaranteed in the inspected release:

- Version auto-migration runs during startup for all commands, including read commands. [startup migration](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/cmd/bd/main.go#L1121-L1127)
- The embedded read-command cache is documented as otherwise using a normal writable store, so incidental effects remain possible. [embedded cache](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/internal/storage/embeddeddolt/cache.go#L35-L42)
- Server-mode commands may connect to or transparently start `dolt sql-server`; embedded mode needs no server. [Dolt modes](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/architecture/dolt.md#L60-L129)

Therefore the supported CLI can provide a logically read-only status integration, but ocman cannot promise zero process/filesystem side effects with current Beads. Parsing storage directly would not fix this safely.

### Ocman integration constraints

Beads status is directory/host scoped, not platform or session scoped. Ocman's `hostsvc.Host` is explicitly the seam for operations that touch a machine's filesystem or processes, with ownership resolved by `Router.ForDir` or, preferably when known, `Router.LookupRemote` (strict: an unregistered owner is rejected, not degraded to the hub). [`internal/hostsvc/host.go:1-16`](../../internal/hostsvc/host.go) [`internal/hostsvc/router.go:74-100`](../../internal/hostsvc/router.go) [`spec/multi-remote-support/architecture.md:392-488`](../multi-remote-support/architecture.md)

The active session already includes `remoteId`; when the browser knows it, the multi-remote design says to send it explicitly rather than infer ownership from a path. [`internal/db/types.go:114-121`](../../internal/db/types.go) [`spec/multi-remote-support/architecture.md:463-488`](../multi-remote-support/architecture.md)

The right sidebar is an extensible tab strip driven by `ChangesSidebarTab`, `DEFAULT_TAB_ORDER`, persisted open tabs/order/sizes, and a render branch. Today its tab list is static. [`frontend/src/components/RightPanel.tsx:55-111`](../../frontend/src/components/RightPanel.tsx) [`frontend/src/components/RightPanel.tsx:143-185`](../../frontend/src/components/RightPanel.tsx) [`frontend/src/components/RightPanel.tsx:236-315`](../../frontend/src/components/RightPanel.tsx) [`frontend/src/lib/uiStore.ts:40-45`](../../frontend/src/lib/uiStore.ts)

The existing `useAsyncResource` provides request cancellation and directory-keyed loading/error/ready state. `useUpstreams` demonstrates project-scoped detection, but the current upstream tab intentionally remains visible without an upstream, so Beads must not copy that visibility choice. [`frontend/src/lib/useAsyncResource.ts:1-29`](../../frontend/src/lib/useAsyncResource.ts) [`frontend/src/lib/useAsyncResource.ts:52-108`](../../frontend/src/lib/useAsyncResource.ts) [`frontend/src/lib/useUpstreams.ts:15-36`](../../frontend/src/lib/useUpstreams.ts) [`frontend/src/components/RightPanel.tsx:153-159`](../../frontend/src/components/RightPanel.tsx)

## Smallest viable integration

These are recommendations, not established facts.

### Backend and ownership

Add one directory-scoped operation, conceptually `Host.BeadsStatus(ctx, dir)`, with one shared response type in `internal/hostsvc`. Implement it on the local host and proxy the same method through ocman's existing JSON-over-gRPC remote seam. Do not execute `bd` in an HTTP handler and do not add a Beads platform adapter: Beads does not own coding sessions.

Expose one authenticated GET endpoint:

```text
GET /api/project/beads-status?dir=<absolute>&remoteId=<owner-if-known>
```

Resolve with `LookupRemote(remoteId)` when supplied, otherwise `ForDir(dir)`, matching current host-qualified terminal/worktree handlers. This prevents a remote path from accidentally executing on the hub. A static `/api/capabilities` flag is unnecessary: binary presence is host-wide but Beads availability is repository-specific, and the status response already carries the precise per-project result.

### Availability and command sequence

On the owning host:

1. Resolve `bd` with `exec.LookPath`. Missing binary means `{ "available": false }`.
2. Require `bd version --json` 1.1.0 or newer; older or unparseable versions remain hidden.
3. Run `BD_JSON_ENVELOPE=1 bd --readonly where --json` with the process working directory set to the repository and inherited Beads database-selection variables cleared. This is deliberately not `-C`: current Beads rejects a non-Beads `-C` directory before `where` can emit its structured `no_beads_directory` result.
4. On successful discovery, run `bd -C <dir> --readonly list --json`, preserving Beads' default filters and 50-row limit.
5. Query `parent-child` dependencies for those returned IDs and construct the hierarchy in memory. Missing parents remain top-level rather than triggering more queries.
6. Validate only the fields rendered by ocman and ignore unknown fields.

Use `exec.CommandContext` with a fixed argument slice, never a shell. Capture stdout and bounded stderr separately. `where` distinguishes a missing workspace (hide everything) from a discovered workspace whose later list query fails (retain the pane and show an error).

### Minimal API model

```ts
type BeadsListResponse =
  | { available: false }
  | {
      available: true;
      tickets?: Array<{
        id: string;
        title: string;
        status: 'open' | 'in_progress' | 'blocked' | 'deferred';
        priority: number;
        parentId?: string;
      }>;
      error?: 'status_unavailable' | 'unsupported_schema';
    };
```

Do not expose workspace/database paths, raw stderr, descriptions, metadata, tracker credentials, or the full upstream payload. Beads warns that issue content may contain control characters or prompt injection, and its local data and some tracker credentials are unencrypted. [security guidance](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/SECURITY.md#L45-L53) [untrusted content](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/SECURITY.md#L84-L109)

### Frontend visibility

Fetch status when both the active directory and owner are known. Until the first result resolves, render no Beads icon or pane. When `available` is false, render no icon, pane, empty state, or Beads-specific request beyond the detection/status request. This satisfies “completely absent,” including the loading period.

Build `availableTabs` dynamically and use it for all of these, not only the icon:

- strip ordering and drag-and-drop items;
- open-tab filtering;
- `collapsed` calculation;
- pane sizing and rendering.

This prevents persisted `changesSidebarOpenTabs: ['beads']` from leaving an invisible open pane when the user switches to a repository without Beads. Persisted order/size values can remain untouched and become useful again in a Beads repository; no store migration is required beyond extending the tab union/default order.

The pane renders a compact tree matching `bd list`: status marker, ID, priority, and title. It should not render descriptions or Markdown, link into Beads storage, or add mutation actions.

### Refresh and errors

- Fetch on `(directory, remoteId)` change.
- While the Beads pane is open, poll no faster than every 30 seconds; do not poll while hidden. Beads changes made by agents do not map reliably to ocman's file-edit SSE tick.
- Keep manual refresh in the pane header.
- Abort on project/session change and prevent overlapping subprocesses.
- Use a short command timeout (recommended starting point: 5 seconds), cap stdout/stderr, and preserve the last successful summary during a transient refresh failure.
- If discovery says unavailable, remove the tab immediately. If discovery succeeds but status fails, keep the tab and show a small generic error plus retry; log bounded diagnostic stderr only on the owning backend.
- Do not persist Beads data in `state.db`; the local Beads database remains authoritative.

The 30-second interval and 5-second timeout are starting values, not facts from either project. Embedded mode is single-writer/file-locked, so bounded, non-overlapping reads matter. [embedded concurrency](https://github.com/gastownhall/beads/blob/2f9367d6a76e8bab2bf056e0a1c545014f5fe18f/docs/architecture/dolt.md#L409-L414)

## Smallest test seams

Backend:

- Inject only a tiny command runner (or executable path plus runner) into the local Beads status function; table-test missing binary, no workspace, valid envelope, command timeout, malformed JSON, unsupported schema, and discovered-workspace/status-failure.
- Handler tests need only absolute-dir validation, explicit `remoteId` owner selection, unavailable response, and available/error response using a fake `Host`.
- Add one remote round-trip test proving `dir` reaches the remote host and the JSON response returns unchanged. This is required because adding a `Host` method without its RPC path would silently break remote projects.

Frontend:

- Hook/API test: no directory means no request; directory/owner changes abort and refetch; unavailable maps to no data; retry preserves stale summary.
- `RightPanel` tests: unresolved and unavailable produce no Beads tab DOM; available produces the tab; a persisted open Beads tab is ignored in an unavailable repository; switching to an available repository restores it; status failure after successful discovery leaves the tab visible with an alert.
- Use role/label locators for the tab and refresh button, matching repository e2e conventions.

No full Beads installation fixture is needed in ocman CI. The parser/runner seam and one optional developer integration test against a temporary `bd init` repository are sufficient.

## Confirmed decisions

The agreed scope is captured at the top of this document. No product decisions remain open for the investigation phase.

## Bottom line

The smallest integration is one owner-routed operation that discovers Beads, reads the default `bd list --json` rows plus their `parent-child` relationships, and returns a minimal tree model to one dynamically available right-panel tab. It should not parse `.beads` files or terminal formatting, add a platform adapter, add a global capability, persist data, show closed tickets, or expose mutation actions.

## Tree connector line patterns

This section is visual implementation research only. Sources are official repositories pinned to inspected revisions; it does not recommend adding a tree dependency.

### VS Code tree indent guides

VS Code's base tree renders a flattened, virtualized list rather than nested DOM. Each visible row contains sibling indent, twistie, and content cells; the renderer reconstructs one full-row-height guide element per ancestor. The default depth step is 8px (configurable, clamped to 0-40px), while the twistie occupies a separate 16px cell. Guides are absolutely positioned, pointer-inert vertical borders behind the normal-flow twistie and content. They do not draw elbows or special last-child endings: their purpose is to expose ancestor indentation, so each visible row independently carries the guides it needs. [row template and geometry](https://github.com/microsoft/vscode/blob/5d10c1e64a8f48e8fb5c9edcef3582c2fc0a9854/src/vs/base/browser/ui/tree/abstractTree.ts#L345-L443) [guide generation](https://github.com/microsoft/vscode/blob/5d10c1e64a8f48e8fb5c9edcef3582c2fc0a9854/src/vs/base/browser/ui/tree/abstractTree.ts#L522-L559) [guide CSS](https://github.com/microsoft/vscode/blob/5d10c1e64a8f48e8fb5c9edcef3582c2fc0a9854/src/vs/base/browser/ui/tree/media/tree.css#L6-L59)

The row delegate can report dynamic heights and each guide uses the row's full height, so connector height is not derived from text metrics. Content overflow and wrapping remain renderer concerns. Flattening loses native nested-list semantics, so VS Code explicitly supplies `tree`/`treeitem`, level, set size, position, expanded state, and labels. This is appropriate for its interactive, keyboard-driven tree, but would be substantial machinery for ocman's read-only list. [dynamic-height delegate](https://github.com/microsoft/vscode/blob/5d10c1e64a8f48e8fb5c9edcef3582c2fc0a9854/src/vs/base/browser/ui/tree/abstractTree.ts#L224-L238) [ARIA reconstruction](https://github.com/microsoft/vscode/blob/5d10c1e64a8f48e8fb5c9edcef3582c2fc0a9854/src/vs/base/browser/ui/tree/abstractTree.ts#L182-L213)

### Ant Design Tree `showLine`

Ant Design accepts nested tree data, but `rc-tree` flattens visible nodes into virtual-list rows and records `isStart`/`isEnd` at every depth. Each row gets an `aria-hidden` fixed-width indent span per ancestor plus a switcher cell. Tree tokens make title height, switcher size, and indent width the same base unit (normally 24px); connector lines sit at half that width. The switcher/disclosure or leaf icon owns the connector intersection, and the leaf elbow is layered above the line. [flattening](https://github.com/react-component/tree/blob/a33b66e4bd9c71ef552ba99e84e6faf389bfdadd/src/utils/treeUtil.ts#L148-L178) [indent spans](https://github.com/react-component/tree/blob/a33b66e4bd9c71ef552ba99e84e6faf389bfdadd/src/Indent.tsx#L11-L30) [tokens and geometry](https://github.com/ant-design/ant-design/blob/a6a307f1ac800a0def2861a60c24e0b065c65ce1/components/tree/style/index.ts#L347-L371) [icon selection](https://github.com/ant-design/ant-design/blob/a6a307f1ac800a0def2861a60c24e0b065c65ce1/components/tree/utils/iconUtil.tsx#L37-L85)

The per-depth `isEnd` classes suppress completed ancestor rails, and the last leaf's vertical segment ends halfway through the first title line. Rows are top-aligned; indent and switcher cells stretch with a taller row while the elbow remains anchored to the first 24px line, so a wrapped title does not move its branch point. Ant documents width limitations in virtual mode. The rows expose `tree`, `treeitem`, expanded, selected, checked, disabled, and active-descendant state and hide decorative indent spans, although this inspected revision does not emit `aria-level`, `aria-setsize`, or `aria-posinset` on each flat row. [termination CSS](https://github.com/ant-design/ant-design/blob/a6a307f1ac800a0def2861a60c24e0b065c65ce1/components/tree/style/index.ts#L426-L462) [row stretching](https://github.com/ant-design/ant-design/blob/a6a307f1ac800a0def2861a60c24e0b065c65ce1/components/tree/style/index.ts#L189-L205) [virtual-mode limitation](https://github.com/ant-design/ant-design/blob/a6a307f1ac800a0def2861a60c24e0b065c65ce1/components/tree/index.en-US.md#L150-L152) [row ARIA](https://github.com/react-component/tree/blob/a33b66e4bd9c71ef552ba99e84e6faf389bfdadd/src/TreeNode.tsx#L419-L465)

### jsTree elbow connectors

jsTree uses genuine nested `ul > li` DOM. A node contains a 24px opener icon, an anchor/treeitem and content icon, followed by an optional child `ul[role=group]`. Its theme sprite draws the repeating vertical rail on the `li` and the elbow/open/closed state in the opener cell, keeping connector and disclosure geometry in one fixed 24px grid. Rendering marks the final visible sibling `jstree-last`; CSS then removes that node's repeating vertical background while its opener sprite retains the terminating elbow. [node DOM](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/jstree.js#L651-L674) [fixed theme geometry](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/themes/mixins.less#L7-L26) [last-child classification](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/jstree.js#L2548-L2579) [last-child CSS](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/themes/default/style.css#L415-L433)

That precision depends on fixed-height, single-line rows: the stock theme sets fixed node/anchor heights and `white-space: nowrap`, so it is not a model for ocman's wrapped two-line rows. jsTree supplies comprehensive interactive-tree roles and state, including level and optional set-size/position metadata, but removes the anchor's native outline and relies on themed focus state. [single-line constraint](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/themes/base.less#L2-L13) [ARIA setup and position option](https://github.com/vakata/jstree/blob/6256df013ebd98aea138402d8ac96db3efe0c0da/src/jstree.js#L452-L461)

### Comparison and smallest robust approach for ocman

Ocman's nested `ul/li` already matches the simplest DOM for a non-interactive hierarchy, and its two-line row intentionally permits title wrapping. The status circle is in a 14px grid column, but the current connector is divided among `li::before`, `li::after`, and a parent ticket's `::after`. Those pieces use different containing blocks: the `li` includes its entire descendant subtree, the ticket covers only the current variable-height row, and the child `ul` adds both padding and a separate margin. Their endpoints are therefore reconciled with negative bottoms and several independently tuned circle/indent offsets. A typography, padding, border-width, or wrapping change moves only some pieces. Repeated pseudo-element tuning is failing because it is compensating between coordinate systems rather than defining one connector geometry. [current DOM](../../frontend/src/components/BeadsPane.tsx#L34-L70) [current connector geometry](../../frontend/src/components/RightPanel.css#L337-L419) [wrapped titles](../../frontend/src/components/RightPanel.css#L454-L459)

The smallest robust direction is to keep the nested lists and introduce one fixed-width marker rail per row. The rail owns the elbow and status/disclosure stacking; its center defines both horizontal and vertical connector positions. The nested child group owns only the continuous ancestor vertical, with the final child masking or terminating it below that row's fixed first-line branch point. Content remains a separate variable-height column, so second-line wrapping cannot alter connector alignment. This borrows Ant Design's single geometry unit and icon-over-line stacking, and jsTree's nested-group/last-child behavior, without jsTree's fixed row height or the flattening, virtualization, state bookkeeping, and ARIA reconstruction used by VS Code and Ant Design.

Keep native list semantics while tickets remain read-only. Full `tree`/`treeitem` roles require the corresponding keyboard navigation, focus, selection, and expansion behavior; adding the roles only for appearance would make this less accessible, not more.
