# Style ownership

- `src/tokens.css` contains shared variable definitions only.
- `src/base.css` owns resets and document-wide defaults.
- `src/App.css` owns viewport and route containers.
- Component styles live beside their component. `AppHeader.css` owns the app
  header and its action slots; `MainNav.css` owns navigation, including mobile
  and native-window variants.
- Always split CSS by its owning component. When touching a stylesheet that
  mixes several components, move their rules into separate owner-local files.
- Use `Component.module.css` for new and migrated component styles. Import its
  classes in the owning component; keep theme variables in `tokens.css`.
  Local styles own layout, while shared controls own control appearance and
  interaction states. Avoid parent selectors that restyle child controls:
  CSS Modules scopes class names, but descendant selectors still cross component
  boundaries. Do not use another feature's classes as shared helpers.
- Use `FilterField` for picker captions and sizing. Reuse `SearchSelect` for the
  picker itself instead of overriding its appearance from a page stylesheet.
- Use `SecretField` for secret inputs. `allowReveal` controls the eye button;
  `protect` marks a stored secret whose blank replacement means no change.
  `onReset` enables the trash button and `resetPending` marks an explicit clear
  awaiting save. The form owns serialization: omit unchanged secrets and send
  its API's clear value only after an explicit reset. Reveal shows newly entered
  text, not a fetched stored secret.
- Page styles own page layout. Scope element selectors to the owning class:
  `.app-header`, never a global `header` rule for application chrome.
- `src/shared.css` holds existing cross-page helpers. Prefer the existing
  shared controls or component-owned styles when adding UI.
- `src/print.css` coordinates printing the authenticated conversation. The
  public share page owns its separate print rules.

`main.tsx` loads tokens, base styles, shared helpers, shell layout and print
rules before the application's component imports. Keep responsive and state
rules with their owner. Check cascade order when moving rules between files.

For shell changes, run `e2e/shell-styles.spec.ts`. It checks desktop, collapsed,
mobile, native-window and print geometry, plus Factory drawer isolation.
The fixture mocks every API request, so it needs only a frontend server.
`src/tokens.test.ts` checks that shared and shell classes have one owner.
