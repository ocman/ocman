# Optional plugin navigation icons

## Existing mechanisms examined

- `PluginPageNav.tsx` renders a fixed Bootstrap puzzle icon for both page and UI
  declarations. The bundled Bootstrap font already supplies host-managed icons,
  but plugin metadata cannot select one.
- `sdk/protocol/ui.go` and `internal/pluginsmod/ui.go` transport checksum-bound
  JavaScript bundles. They require `ui.app` and only support the hub. An inert
  navigation image must not require executable UI approval.
- Action artifacts use owner-routed, authorized opaque handles and attachment
  downloads. They are action results, require action grants and do not declare
  reusable plugin metadata.
- `internal/httpx.SetInertFileHeaders` already supplies nosniff, sandbox CSP and
  framing policy for inert files. Reuse it for validated icon responses.

The missing feature is an optional icon descriptor carried through discovery,
persistence and navigation catalogs, plus a bounded inert SVG read that needs no
extra grant or capability declaration. Reuse the supervised process transport,
authenticated PluginOperation routing and current installation authorization,
not the executable UI bundle endpoint or filesystem access.

## Contract

`Description.Icon` is an optional `*IconDescriptor` with exactly one of
`bootstrap` or `path`. Bootstrap names are bare names from the installed font;
unknown names fall back to puzzle. Asset paths are relative slash-separated
ASCII names with a `.svg` suffix, at most 256 bytes. Each segment uses letters,
digits, dot, underscore or hyphen, and cannot be empty, `.` or `..`. No URL,
percent encoding, backslash, query or fragment is accepted.

An SVG declaration implicitly offers the standard `asset` 1.0 protocol method
`read`, with `{path}` parameters and unary `{data}` base64 bytes. No entry in
`capabilities` or `requestedGrants` is needed. Only the exact declared path is
readable. The SDK accepts an `fs.FS`, including `embed.FS`, never an inferred
working-directory path. Reads are capped at 64 KiB. The host rechecks enablement,
description and ownership before returning bytes. Disabled, missing, conflicted
or disconnected installations fail closed.

The host accepts a small SVG geometry/attribute allowlist and rejects scripts,
styles, foreign content, events, links, references, entities and external URLs.
It serves validated SVG with nosniff and a restrictive CSP. The browser loads it
as an image and uses an alpha mask with `currentColor`. It never inserts plugin
markup into the DOM. Missing, invalid or failed images retain puzzle.

## Independent workflows

- A document-indexing workspace selects `bootstrap: "journal-text"` without
  serving any image or adding grants.
- An equipment-maintenance workspace embeds `assets/wrench.svg` and serves it
  with `plugin.AssetHandler`. Its page permissions remain unchanged.
