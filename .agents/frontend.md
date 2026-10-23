# Frontend implementation rules

The frontend is React, TypeScript and Vite. State uses Zustand; routing uses
react-router-dom. Read [sessions.md](sessions.md) before changing composer,
draft, first-delivery, queue or status behavior.

## Platform and owner independence

Do not branch on `session.platform === '...'`. Use `/api/capabilities` and
`useCapabilities()`. ESLint's `no-restricted-syntax` rule enforces this.
Use a justified `eslint-disable-next-line no-restricted-syntax` only for false positives.
Keep machine differences behind host badges, capabilities and owner-routed APIs.

## Reuse the UI

Look for shared components before writing markup or CSS:

- Controls: `Button`, `AnchorButton`, `ButtonGroup`, `TextField`, `SearchField`,
  `SelectField`, `TextareaField` in `components/Control.tsx`, plus `CopyButton`
  and `IconButton`. Use existing variants such as `accent`, `danger` and `ghost`.
- Dialogs: `Modal`, `ModalHeader`, `ModalFooter`. Leave class props unset for
  default theming.
- Layout/state: `DataTable` with `framed` for a bordered scrollable card,
  `DataTableGroup`, `DataTableRow`, `EmptyState`, `LoadingState`, `Tabs`,
  `SegmentedControl`, `SettingRow` and `HeaderPortal` for header actions.
- Markdown: wrap `MarkdownContent` in `.oc-md`.

Do not hand-roll styled buttons, inputs, selects, danger colors or table frames.
Extract repeated patterns instead of copying them.

Colors and fonts come only from `frontend/src/tokens.css`, including `--bg`,
`--bg-card`, `--border`, `--text`, `--text-dim`, `--accent`, `--danger` and
`--font-conversation-mono`. Check that a token exists before using it.
`--text-muted`, `--surface` and `--font-mono` do not exist; undefined variables
silently inherit styles.

## Feature registration

Feature routes, header titles, main navigation and settings groups register in
one `FEATURES` entry under `frontend/src/features`. Do not add feature entries
manually to `App.tsx`, `MainNav`, `routeTitle` or `SettingsTab`.

Choose `dashboard` or `page` layout and provide a route `title`. Settings groups
use `mount: 'always'` to retain unsaved edits across switches or `'active'` to
mount on demand. The registry is static and eagerly imported. Core pages stay
in the existing core files, with titles in `lib/routeTitle.ts`. Backend plugin
   panes and palette actions are separate from this registry. The generic plugin
   workspace route and dynamic navigation component register as one feature;
   individual plugin pages come from owner-approved declarations.

Read [testing.md](testing.md) for stable e2e locators and frontend coverage checks.
