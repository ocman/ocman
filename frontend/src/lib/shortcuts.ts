// Keyboard shortcut conventions for this codebase.
//
// All keyboard shortcuts in ocman go through `shortcutRegistry.ts` — a single
// window-level dispatcher matches keydown events against registered
// shortcuts and handles `preventDefault` centrally. Register a shortcut with
// the `useShortcut` hook from that module; do NOT add ad-hoc
// `window.addEventListener('keydown', ...)` handlers.
//
// The registry exists because Alt-based shortcuts need a subtle set of rules
// to work correctly on Mac, where Alt is labelled Option. The browser reports
// Option as `e.altKey === true` so detection is fine, BUT Option rewrites
// `e.key` to a special character (e.g. Option+H → ˙, Option+L → ¬,
// Option+D → ∂). Matching on `e.key` therefore breaks on Mac.
//
// Rules (enforced by the registry; listed here for context):
//   1. Match on `e.code` (physical key position, e.g. 'KeyH', 'Space',
//      'ArrowUp'), never on `e.key`. KeyBinding.code is the only option.
//   2. `preventDefault` runs whenever a shortcut matches, so Mac's Option
//      never leaks its special character into the focused field.
//   3. Alt shortcuts fire even when an input/textarea has focus (so the
//      Option character is preventDefault-ed); bare-key shortcuts don't.
export function isMacPlatform(): boolean {
  return /Mac|iPhone|iPad/.test(navigator.platform);
}

export function shortcutLabel(windowsLabel: string, macLabel = windowsLabel): string {
  return isMacPlatform() ? macLabel : windowsLabel;
}

export function openVSCode(directory: string) {
  window.location.href = `vscode://file${directory}`;
}

export function isEditableTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  const tagName = target.tagName;
  return target.isContentEditable || tagName === 'INPUT' || tagName === 'TEXTAREA' || tagName === 'SELECT';
}
