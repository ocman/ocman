// Central keyboard-shortcut registry.
//
// All keyboard shortcuts in ocman register themselves here via the
// `useShortcut` hook. A single window-level dispatcher reads the registry and
// routes matching keydown events to the right handler. This keeps three
// things in one place:
//
//   1. Key-matching logic (e.code + modifiers), correct on Mac Option.
//   2. `preventDefault` policy — every matched shortcut prevents the default
//      action so Mac's Option doesn't insert ∂/˙/¬ into focused inputs.
//   3. The list of shortcuts shown in the help dialog, grouped by scope.
//
// See `shortcuts.ts` for the project-wide Alt/Option conventions that
// informed this module. All bindings use `code` (physical key) — never `key`.

import { useEffect } from 'react';
import { create } from 'zustand';
import { isMacPlatform } from './shortcuts';
import { remoteLog } from './remoteLog';

export type Scope = 'site' | 'session' | 'project' | 'composer' | 'prompt';

export type KeyBinding = {
  // Physical key code, e.g. 'KeyJ', 'Space', 'ArrowUp', 'Slash'.
  code: string;
  // Modifiers. Defaults to false. `ctrl` and `meta` are currently not
  // supported (no existing shortcut uses them); the dispatcher requires both
  // to be unpressed on every match.
  alt?: boolean;
  shift?: boolean;
};

export type Shortcut = {
  // Stable, dotted id, e.g. 'session.nav-next'.
  id: string;
  scope: Scope;
  // Either a single binding or a list of alternatives. Multiple bindings
  // still surface as a single row in the help dialog.
  keys: KeyBinding | KeyBinding[];
  // Optional display label override. When omitted, the dialog derives the
  // chips from the first KeyBinding in `keys` using platform-aware modifier
  // names (⌥ on Mac, Alt elsewhere). Set this only when the derived label
  // isn't right, e.g. when two bindings collapse into a single display
  // (Alt+/ + Alt+Shift+/ → "Alt+?").
  label?: string;
  description: string;
  handler: (e: KeyboardEvent) => void;
  // When true, the shortcut fires even if an editable element (input/
  // textarea/contenteditable) is focused. When undefined, defaults to `true`
  // for any binding that uses Alt (so Mac Option doesn't leak characters into
  // the focused field) and `false` otherwise.
  runInEditable?: boolean;
  // Optional predicate evaluated at key-time. When it returns false the
  // shortcut is skipped and the next-highest-priority match is tried.
  enabled?: () => boolean;
  // Higher-priority shortcuts win when multiple match. Defaults to the
  // scope's default priority; see SCOPE_PRIORITY below.
  priority?: number;
};

// Scope precedence. Narrower scopes win over broader ones so a composer-
// scoped shortcut can shadow a session-scoped one that uses the same key.
const SCOPE_PRIORITY: Record<Scope, number> = {
  prompt: 40,
  composer: 30,
  session: 20,
  project: 20,
  site: 10,
};

type Registry = {
  shortcuts: Map<string, Shortcut>;
  register: (shortcut: Shortcut) => void;
  unregister: (id: string) => void;
};

export const useShortcutRegistry = create<Registry>((set) => ({
  shortcuts: new Map(),
  register: (shortcut) =>
    set((state) => {
      const next = new Map(state.shortcuts);
      next.set(shortcut.id, shortcut);
      return { shortcuts: next };
    }),
  unregister: (id) =>
    set((state) => {
      if (!state.shortcuts.has(id)) return state;
      const next = new Map(state.shortcuts);
      next.delete(id);
      return { shortcuts: next };
    }),
}));

// React hook: register a shortcut for the lifetime of the calling component.
// The shortcut argument is captured in a ref so handlers see the latest
// closure without re-registering on every render.
export function useShortcut(shortcut: Shortcut): void {
  const register = useShortcutRegistry((s) => s.register);
  const unregister = useShortcutRegistry((s) => s.unregister);

  useEffect(() => {
    remoteLog.debug('[shortcut] register', { id: shortcut.id, scope: shortcut.scope });
    register(shortcut);
    return () => {
      remoteLog.debug('[shortcut] unregister', { id: shortcut.id });
      unregister(shortcut.id);
    };
    // We deliberately track every field so consumers can pass inline objects
    // without stale closures. Handlers are usually stable (useCallback) — if
    // not, the re-register on each render is still cheap (O(1) map write).
  }, [
    register,
    unregister,
    shortcut,
    shortcut.id,
    shortcut.scope,
    shortcut.label,
    shortcut.description,
    shortcut.handler,
    shortcut.runInEditable,
    shortcut.enabled,
    shortcut.priority,
    shortcut.keys,
  ]);
}

// True when the event target is an editable field (textarea/input/select/
// contenteditable). Duck-typed on tagName / isContentEditable so it also
// works in unit tests that don't load a real DOM.
function isEditableTarget(target: EventTarget | null): boolean {
  if (!target || typeof target !== 'object') return false;
  const el = target as { tagName?: unknown; isContentEditable?: unknown };
  if (el.isContentEditable === true) return true;
  const tag = typeof el.tagName === 'string' ? el.tagName : '';
  return tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT';
}

function bindingsOf(shortcut: Shortcut): KeyBinding[] {
  return Array.isArray(shortcut.keys) ? shortcut.keys : [shortcut.keys];
}

function anyBindingUsesAlt(shortcut: Shortcut): boolean {
  return bindingsOf(shortcut).some((b) => b.alt === true);
}

function bindingMatches(binding: KeyBinding, e: KeyboardEvent): boolean {
  if (e.code !== binding.code) return false;
  // ctrl/meta must always be off — no current shortcut uses them and
  // accepting them here would stomp on Cmd+J on Mac, Ctrl+L in the browser,
  // etc.
  if (e.ctrlKey || e.metaKey) return false;
  if (!!binding.alt !== e.altKey) return false;
  if (!!binding.shift !== e.shiftKey) return false;
  return true;
}

// Result of dispatching an event against the registry. The dispatcher
// always prevents the browser's default action when ANY registered
// binding syntactically matches an Alt-based key event — even if the
// matched shortcut's `enabled()` predicate returns false. This keeps
// Mac's Option key from leaking ∂/˙/¬/… into focused inputs whenever a
// binding *could* have fired but didn't for scope/enabled reasons.
export type DispatchOutcome = {
  // The shortcut that should handle the event, if any. Null when no
  // shortcut matches, or when the best match is gated by `enabled()`.
  match: Shortcut | null;
  // Whether the event should have its default action suppressed. True
  // whenever any Alt-based binding syntactically matched, regardless of
  // whether a handler ran.
  preventDefault: boolean;
};

// Core dispatch logic. Exported for unit tests; consumers should prefer
// the `useShortcutDispatcher` hook.
export function matchShortcut(
  shortcuts: Iterable<Shortcut>,
  e: Pick<KeyboardEvent, 'code' | 'altKey' | 'ctrlKey' | 'metaKey' | 'shiftKey' | 'repeat' | 'defaultPrevented' | 'target'>,
): DispatchOutcome {
  if (e.repeat || e.defaultPrevented) return { match: null, preventDefault: false };

  const editable = isEditableTarget(e.target);

  let best: Shortcut | null = null;
  let bestPriority = -Infinity;
  // True when at least one Alt-based binding syntactically matched —
  // regardless of whether we end up running its handler.
  let altBindingMatched = false;

  for (const shortcut of shortcuts) {
    const matchingBinding = bindingsOf(shortcut).find((b) => bindingMatches(b, e as KeyboardEvent));
    if (!matchingBinding) continue;

    if (matchingBinding.alt) altBindingMatched = true;

    const runInEditable = shortcut.runInEditable ?? anyBindingUsesAlt(shortcut);
    if (editable && !runInEditable) continue;

    if (shortcut.enabled && !shortcut.enabled()) continue;

    const priority = shortcut.priority ?? SCOPE_PRIORITY[shortcut.scope];
    if (priority > bestPriority) {
      best = shortcut;
      bestPriority = priority;
    }
  }

  // preventDefault runs when either:
  //   - a handler will run (best !== null), OR
  //   - any Alt binding syntactically matched — blocking Mac's Option
  //     character even for shortcuts that are currently gated.
  return { match: best, preventDefault: best !== null || altBindingMatched };
}

// Mount once (e.g. from App.tsx). Installs a single capture-phase keydown
// listener and dispatches to the highest-priority matching shortcut.
export function useShortcutDispatcher(): void {
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const { shortcuts } = useShortcutRegistry.getState();
      const { match, preventDefault } = matchShortcut(shortcuts.values(), e);
      // Temporary diagnostic logging (remove once the keybinding regression
      // is resolved). Only logs events that look like they might be meant
      // for a shortcut — avoids spamming the server log on every keystroke.
      if (e.altKey || e.metaKey || e.code === 'Escape' || /^F\d+$/.test(e.code)) {
        remoteLog.debug('[shortcut] keydown', {
          code: e.code,
          alt: e.altKey, shift: e.shiftKey, ctrl: e.ctrlKey, meta: e.metaKey,
          repeat: e.repeat,
          target: (e.target as { tagName?: string } | null)?.tagName,
          registered: [...shortcuts.keys()],
          matchedId: match?.id ?? null,
          preventDefault,
        });
      }
      if (preventDefault) {
        e.preventDefault();
        e.stopPropagation();
      }
      if (match) match.handler(e);
    };
    window.addEventListener('keydown', onKeyDown, true);
    return () => window.removeEventListener('keydown', onKeyDown, true);
  }, []);
}

// ---------------------------------------------------------------------------
// Display helpers for the help dialog.
//
// Shortcuts are rendered as a sequence of per-key chips (modifier + key)
// instead of a single "Alt+J" string, so the dialog reads like the
// GitHub keyboard-shortcut help.
// ---------------------------------------------------------------------------

// Human-readable name for a physical key code. Fallback is the code itself,
// which is fine for things like 'Tab' and 'Enter'.
const KEY_CODE_LABELS: Record<string, string> = {
  Space: 'Space',
  Enter: 'Enter',
  Tab: 'Tab',
  Escape: 'Esc',
  Backspace: 'Backspace',
  Delete: 'Del',
  ArrowUp: '↑',
  ArrowDown: '↓',
  ArrowLeft: '←',
  ArrowRight: '→',
  Slash: '/',
  IntlRo: '/',
  Comma: ',',
  Period: '.',
  Semicolon: ';',
  Quote: "'",
  Backslash: '\\',
  Backquote: '`',
  Minus: '-',
  Equal: '=',
  BracketLeft: '[',
  BracketRight: ']',
};

function keyLabel(code: string, shift: boolean): string {
  // Shift+Slash is typically written as `?` on the dialog (matches what the
  // user types: Alt+? to open shortcuts).
  if (shift && (code === 'Slash' || code === 'IntlRo')) return '?';
  if (code.startsWith('Key') && code.length === 4) return code.slice(3);
  if (code.startsWith('Digit') && code.length === 6) return code.slice(5);
  return KEY_CODE_LABELS[code] ?? code;
}

// Platform-aware modifier labels. Follows macOS convention: ⌥ for Option,
// ⇧ for Shift. Windows/Linux gets the spelled-out names.
function modLabel(kind: 'alt' | 'shift'): string {
  const mac = isMacPlatform();
  if (kind === 'alt') return mac ? '⌥' : 'Alt';
  return mac ? '⇧' : 'Shift';
}

// Break a KeyBinding into the sequence of key chips the dialog renders.
// E.g. { code: 'KeyJ', alt: true } → ['Alt', 'J'] on Linux, ['⌥', 'J'] on Mac.
export function bindingKeys(binding: KeyBinding): string[] {
  const keys: string[] = [];
  if (binding.alt) keys.push(modLabel('alt'));
  if (binding.shift && !((binding.code === 'Slash' || binding.code === 'IntlRo'))) {
    // When Shift is part of a printable symbol (like `?`), keyLabel folds it
    // into the character; otherwise show it explicitly.
    keys.push(modLabel('shift'));
  }
  keys.push(keyLabel(binding.code, !!binding.shift));
  return keys;
}

// Resolve the display label for a shortcut as a plain string, used for
// stable sorting and for consumers who need a one-line representation.
export function shortcutLabel(shortcut: Shortcut): string {
  if (shortcut.label) return shortcut.label;
  const bindings = Array.isArray(shortcut.keys) ? shortcut.keys : [shortcut.keys];
  if (bindings.length === 0) return '';
  return bindingKeys(bindings[0]).join('+');
}

// Group shortcuts by scope and sort alphabetically by their resolved label so
// the dialog is stable regardless of registration order.
export function groupByScope(shortcuts: Iterable<Shortcut>): Record<Scope, Shortcut[]> {
  const groups: Record<Scope, Shortcut[]> = {
    site: [],
    session: [],
    project: [],
    composer: [],
    prompt: [],
  };
  for (const s of shortcuts) groups[s.scope].push(s);
  for (const list of Object.values(groups)) {
    list.sort((a, b) => shortcutLabel(a).localeCompare(shortcutLabel(b)));
  }
  return groups;
}
