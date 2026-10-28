---
title: Plugin contribution guide
weight: 19
---

Plugins extend Ocman's features using host-rendered declarations and typed data.
They cannot supply React components, HTML, CSS, browser JavaScript, arbitrary
routes or shell commands for the browser to execute. Ocman owns the controls,
layout, loading states and action confirmations.

Page forms use Ocman's bounded `PageField` format, not arbitrary JSON Schema or
JSON Forms declarations. No frontend library is required in a plugin.

## Choose a contribution

| Contribution | Capability | What you can supply |
| --- | --- | --- |
| Installation settings | Description `settings` | String, boolean, number, integer, string enum, secret, multiline and string-list settings |
| Navigation workspace | `page` 1.0 or 1.1 | Up to eight labeled owner-local project pages |
| Workspace form | `page` 1.0 | Typed fields, help text and nested repeating sections |
| Workspace list and detail | `page` 1.1 | Typed table columns, bounded rows, cursor pagination and linked item details |
| Form validation | `page` 1.1 | Static constraints and an optional submit-time plugin validation hook |
| Project result tree | `page` or `pane` 1.0 | Read-only bounded trees, with pane contributions in the session sidebar |
| Command | `action` 1.0 | Global, project or session command-palette action with optional confirmation |

Workspace pages also show the same plugin's approved project actions. There is
no arbitrary context-menu placement or item-specific action parameter contract.
Conversation and session capabilities let plugins interact with coding sessions;
see [Plugins](../plugins/) for those contracts.

## Get started with the working example

The repository includes `examples/ocman-plugin-workspaces`. It demonstrates a
repeating service form, numeric inputs, labeled choices, static rules, a
validation hook, a list and a read-only detail page. It uses no external service.

From a checkout:

```sh
go test ./examples/ocman-plugin-workspaces ./sdk/plugin/...
go build -trimpath -o ./ocman-plugin-workspaces ./examples/ocman-plugin-workspaces
```

Copy the executable to the plugin directory on the machine that owns the
projects, normally `~/.local/share/ocman/plugins`. In **Settings → Plugins**,
select that owner, rescan, review the executable checksum and requested grants,
then approve and enable it. Open **Example services** in navigation, choose
**New configuration** and a project, then add a service and save.

Use `reserved` as a service name to exercise plugin validation. A replica count
outside 1–20 exercises host static validation. After saving, use **View** in the
service table to open its detail page. Restarting the plugin retains the saved
configuration. Rebuilding or changing declarations requires rescanning and fresh
approval.

Native executables are trusted code, including during discovery. Grants control
what Ocman sends through its interface, not the executable's OS permissions.
See [installation and trust](../plugins/#trust-and-installation).

## Declare a workspace

The plugin's display name names its single main navigation entry and app header.
Each declared root page becomes a host-generated tab using its page `label`.
Tabs are scoped to the plugin and owner. Switching tabs opens that root page,
without carrying an unrelated item or project into it. Single-page plugins need no tab bar.
The header uses `Plugin workspace` only when the matching declaration is unavailable.

### Summary tables in page 1.4

Declare `PageTableCapability` to use `summary` and `severity` displays in
collections and detail properties. A summary value is a `PageSummary` with a
required `Label`, optional `Description` and optional `Count` of additional items.
The host renders secondary text below the label and a count badge beside it.
If the first collection column is a summary, its name links to the row detail
instead of adding a separate Details column. Text is truncated visually, with
the full value available on hover and in the detail page.

Severity values are `critical`, `high`, `medium`, `low`, `info` or `unknown`.
The host owns badge colors and always includes a text label. These displays
require page 1.4. Plugins supporting earlier hosts must return text cells there.

Set `PageCollection.DetailPresentation` to `"drawer"` to open row details in the
host's right-hand drawer instead of navigating away. The host reads the same
page and opaque item ID on the same owner/project only when opened. Closing
the drawer cancels pending reads and restores focus, while keeping pagination
and list position unchanged. Detail properties render as labeled, wrapping
sections with preserved line breaks. Configuration rows with `projectId` still
navigate to their project editor. Omitting the preference retains inline routes.
The drawer preference also requires page 1.4 and must be omitted for older hosts.

### Native model picker in page 1.3

Use `PageModelCapability` instead of earlier page capability versions, and a field such as
`PageField{Key: "model", Label: "Review model", Type: "model"}` to use ocman's
searchable model picker. It lists models from the plugin owner's available
platform catalogs, without selecting a project or starting a runtime.

The saved value is a string using ocman's `[platform:]provider/model` format.
OpenCode references remain unprefixed for compatibility; other platforms retain
their prefix, for example `claudecode:anthropic/sonnet`. An empty string means
use the default. Saved models remain visible when a catalog is unavailable.
Model names grant no execution permissions; action and session grants still apply.

### Plugin-local homepages in page 1.2

Declare `PluginPageCapability`, request `PagePluginGrant` (`page.plugin`), and
set the page descriptor's `scope` to `plugin` to store and list configurations
inside the plugin. Ocman calls that homepage once with an empty `projectId`,
instead of probing every host project. No project directory is sent or resolved.
The plugin decides its own private storage layout.

The default scope remains `project`, requires `page.project`, and still rejects
requests without a known owner-local project. Plugin-local pages may also accept
an explicit `projectId`; the host resolves and authorizes it before dispatch.
This supports project-scoped editors, actions and model sessions without making
the homepage discover their configuration. Neither grant authorizes sessions by
itself. Existing action and session grants still apply.

A plugin-local collection can set `projectId` on a row to open its project-scoped
editor instead of an item detail. This is an opaque project ID, never a path.
The link preserves owner, plugin and page, and the destination is authorized
on read. Project links and plugin-local scope require a page 1.2 host.

For an independent workspace, omit project IDs entirely. Use a stable page-safe
row ID to open its editor through `itemId`. The homepage can declare a small
creation form; ocman shows a **New workspace** button and uses that form to
create it. The workspace can link to any number of code projects without an
owning project. The host renders `plugin` placement actions for the item's
`workspaceId`, gated by `context.workspace`; remote workspaces execute on their
explicit owner, never by falling back to the hub.

For model-only work, declare `PluginSessionCapability` (session 1.2), request
`session.run` and `session.plugin`, and submit `workspaceId` with `noTools: true`.
The owner chooses a private directory below the plugin data directory. A project
ID, subpath or enabled tools is rejected in this mode. The all-tools-denied
profile is applied before creating the turn. Older hosts reject the request
locally instead of downgrading it to a project-backed session.

Go plugins import `github.com/ocman/ocman/sdk/plugin` from the
`github.com/ocman/ocman/sdk` module. The canonical types are shared with the
host. Declare owner scope, the `page.project` grant and
`PageContributionCapability` for the 1.1 options:

```go
d := plugin.Description{
    ID: "org.example.services", Name: "Services", Version: "1.0.0",
    Protocol: plugin.Version{Major: 1}, Scope: plugin.ScopeOwner,
    MaxConcurrency: 2,
    Capabilities: []plugin.Capability{plugin.PageContributionCapability},
    RequestedGrants: []string{plugin.PageProjectGrant},
    Pages: []plugin.PageDescriptor{
        {ID: "services", Label: "Services", Validate: true},
    },
}
```

Pass `plugin.PageHandler(d, handle)` to `plugin.Run`. The callback receives a
context, method and `PageRequest`; it returns a `PageDocument` or normalized
`plugin.Failure(...)`. Read launch configuration with
`plugin.ReadConfiguration()` before `Run` in serve mode. Stdout is reserved for
NDJSON; diagnostics belong on stderr. The complete runnable example includes
this lifecycle setup.

Navigation is built from enabled, approved declarations without invoking a page
handler. Owners and plugin IDs remain part of every request and route. A
disconnected explicit owner fails visibly rather than falling back locally.

## Requests and documents

The host calls these page methods:

| Method | Request | Handler responsibility |
| --- | --- | --- |
| `read` | `pageId`, `projectId`, optional `itemId` or `cursor` | Return the current form, list or detail without mutation |
| `validate` | `pageId`, `projectId`, `revision`, `values`, optional `itemId` | Check values without mutation; return issues in a document |
| `save` | Same as validation | Recheck validity and revision, persist atomically and return sanitized saved state |

`validate` is called only when the page descriptor sets `validate: true`.
It is a submit-time hook, not an on-change or blur hook. It uses the same plugin
runtime, grants, project resolution and cancellation as other page calls.

Every document has `revision` and an object-valued `values`. `fields` may be
omitted, null or empty for read-only pages. Optional members are `tree`,
`configured`, `collection`, `detail` and `issues`. Forms, lists and details may
coexist in a document.

Use `configured: false` when a project has no saved configuration and `true`
when it should appear on the workspace homepage, including read-only lists.
Without it, the legacy heuristic uses nonempty values or an available tree.

The owner resolves `projectId` before admitting the call. It is an opaque
identity, not a directory. Item IDs and cursors are also untrusted references;
resolve them within the selected project, never as filesystem paths.

## Supported form fields

All fields require unique sibling `key`, plain-text `label`, and `type`. Optional
`help` is plain text. Keys use letters, digits, underscores, dots and hyphens,
starting with a letter. They are literal object keys, not dotted paths.

| Type | JSON value | Options |
| --- | --- | --- |
| `text` | String | Single-line input |
| `multiline` | String | Textarea |
| `secret` | String | Write-only password input |
| `boolean` | Boolean | Checkbox |
| `number` | Number | Numeric input, 1.1 |
| `integer` | Integer number | Numeric input with integer validation, 1.1 |
| `list` | Array of objects | Required nested `fields`; add/remove and collapsible rows |
| `select` | String | `optionsFrom` sibling list, or fixed `options` in 1.1 |
| `projects` | Newline-separated string | Ocman's multi-project picker, restricted to the owner |
| `model` | Platform-qualified model string, or empty for default | Ocman's searchable owner/platform model picker, page 1.3 |

Fixed options have `{ "value": "staging", "label": "Staging" }` shape. Do
not combine `options` and `optionsFrom`. A dynamic source must be a sibling
`list` whose first field is `text`; that field supplies the choices. A list's
first text field also supplies its row headings. Secret fields never name rows.

### Static rules in 1.1

| Rule | Supported types | Meaning |
| --- | --- | --- |
| `required` | All | Value must be present; strings must not be empty |
| `minimum`, `maximum` | Number, integer | Inclusive numeric bounds |
| `minLength`, `maxLength` | Text, multiline, secret | Unicode code-point counts |
| `minItems`, `maxItems` | List | Inclusive row-count bounds |
| Fixed or dynamic options | Select | Submitted nonempty value must be an available choice |

A required boolean may be `false`. Use `minItems: 1` to require a nonempty
list. Missing optional fields are allowed; constraints apply when a value is
present. Clearing a numeric input omits the key rather than submitting an empty
string. New rows start with empty strings, false booleans and empty lists;
numeric fields stay absent until entered. There are no automatic schema defaults.

The host reads the current schema on the executing owner before a 1.1 save.
It rejects stale revisions, applies static rules, and skips both validation hooks
and persistence if those rules fail. Unknown submitted keys and wrong value types
are rejected. Invalid drafts may still appear in read responses so users can
repair them.

Stored secrets must never appear in responses. Omit them or return empty input
values, and implement unchanged/clear semantics explicitly in the plugin. Do not
mark a secret `required` if an existing stored value should permit blank input;
check that condition in the validation hook instead.

### Validation issues

Return issues as typed data, never exception text or HTML:

```json
{
  "revision": "r7",
  "values": {},
  "issues": [
    { "path": "/services/0/name", "message": "This name is already used." },
    { "path": "", "message": "The service provider is unavailable." }
  ]
}
```

Paths are JSON Pointers into submitted values; empty means form-wide. Ocman
keeps the user's draft and shows field errors plus a summary. Every issue blocks
saving. A hook error, cancellation or timeout also prevents the save. Do not echo
passwords or other sensitive values in messages.

Validation is advisory until persistence. The plugin must validate again and
compare revisions atomically in `save`, since another writer can change state
after the host checks it. Reads and hooks must not perform the save themselves.
Writes are explicit, not automatically retried. A failed or cancelled save may
have completed; reload before deciding to repeat it.

## Lists and detail pages in 1.1

Return a collection in a read response:

```json
{
  "revision": "r7",
  "values": {},
  "configured": true,
  "collection": {
    "title": "Services",
    "columns": [
      { "key": "name", "label": "Name", "display": "text" },
      { "key": "replicas", "label": "Replicas", "display": "number" }
    ],
    "rows": [
      { "id": "service-a", "values": { "name": "API", "replicas": 2 } }
    ],
    "nextCursor": "page-2"
  }
}
```

Every row needs a unique stable ID and exactly one value per column. The `rows`
and detail `properties` members must be arrays; use `[]` for empty content,
not missing or null members. Ocman renders its shared table and adds a **View**
link. The link preserves the owner,
plugin, page and project, and sends `itemId` on the next read. Return `not_found`
when the item no longer exists. Detail links can be bookmarked:

```text
/plugins/org.example.services/services?ownerId=local&projectId=project-a&itemId=service-a
```

A detail response uses labeled properties:

```json
{
  "revision": "r7",
  "values": {},
  "detail": {
    "title": "API service",
    "properties": [
      { "label": "Replicas", "display": "number", "value": 2 },
      { "label": "Enabled", "display": "boolean", "value": true }
    ]
  }
}
```

Lists and details support `text`, `number`, `boolean` and `status` displays.
Statuses use the existing tree vocabulary: `open`, `in_progress`, `blocked`,
`deferred`, `closed`. The host formats them; there is no plugin color or class
option. Text is rendered literally, not as Markdown or HTML.

`nextCursor` enables **Next page**, which sends that opaque cursor back on a
read. **First page** clears it. Plugins implement stable pagination and decide
page size within the host limit. Built-in server filtering, sorting and total
counts are not part of this version. Item links and pagination are unavailable
while the editor has unsaved changes.

Saving a form resets its collection to the first page. Save responses describe
the cursor-less document, never the previously selected later page.

## Limits and compatibility

- Up to eight pages per plugin; labels and IDs up to 128 bytes.
- Up to 128 declared form fields across four nested list levels.
- Up to 128 fixed choices per select and 128 validation issues per response.
- Up to 16 collection columns, 200 rows and 64 detail properties.
- Display text and issue messages up to 4096 bytes; cursors up to 1024 bytes.
- Documents and requests remain subject to the 1 MiB wire limit and existing
  JSON-depth limits. Each plugin dispatch has a maximum 30-second timeout.

Existing `PageCapability` 1.0 plugins keep their read/save behavior. Declare
`PageContributionCapability` 1.1 to use the new options; they are rejected on a
1.0 connection. Plugin authors must check compatibility with the target host.
No arbitrary JSON Schema, custom rendering, remote browser URLs, conditional
visibility expressions, file-upload controls or plugin styles are supported.

When a new display or interaction is needed, add a host-owned option with tests
and document it here. Do not add an executable browser escape hatch.

## Test your contribution

Test declaration validation, read-only reads, static and plugin validation,
stale revision refusal, project/item isolation, secret omission and atomic writes.
Use temporary private directories or databases, never live plugin data. The
workspace example's tests show this pattern.

For SDK lifecycle and action grant checks, see the
[SDK conformance guide](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/sdk/plugin/README.md#conformance-tests-for-external-plugins).
For exact framing and capability rules, see the
[wire contract](https://forgejo.nousefreak.be/dries/ocman/src/branch/main/sdk/protocol/README.md).
