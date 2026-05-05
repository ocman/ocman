import { useMemo } from 'react';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useSyncRef } from '../../lib/useSyncRef';
import type { Session, TmuxSession } from '../../lib/api';

export interface UseSessionShortcutsOptions {
  /** Page session — used by the enabled() guards to gate shortcuts
   *  that require an active session. */
  session: Session | null;
  /** Whether the platform process is reachable (gates create/switch). */
  portAvailable: boolean;
  /** The tmux session whose path matches the page directory, if any.
   *  The "switch tmux" shortcut is disabled when this is undefined. */
  matchingTmuxSession: TmuxSession | undefined;
  /** Navigate by `direction` in the recent-sessions list. */
  jumpToSession: (direction: 1 | -1) => void;
  /** Switch the user's tmux client to the matching session. */
  handleTmuxShortcut: () => void;
  /** Open the page directory in VS Code. */
  handleVSCodeShortcut: () => void;
  /** Create a new session in the current project. */
  handleNewSession: (title?: string) => Promise<void>;
}

/**
 * Registers the per-page keyboard shortcuts:
 *
 *   Alt+J / Alt+K — next / previous recent session
 *   Alt+T         — switch tmux to matching session
 *   Alt+V         — open in VS Code
 *   Alt+C         — create new session in current project
 *   Alt+M         — open the model-change palette
 *
 * Internally uses useSyncRef to mirror handler / state values into
 * stable refs so the useShortcut registrations don't re-bind on
 * every render — the registry's identity check (id, scope, keys)
 * keys off referential stability of the handler descriptor object.
 */
export function useSessionShortcuts({
  session,
  portAvailable,
  matchingTmuxSession,
  jumpToSession,
  handleTmuxShortcut,
  handleVSCodeShortcut,
  handleNewSession,
}: UseSessionShortcutsOptions) {
  // Mirror the latest handler / state values into refs so shortcut
  // descriptors below can reference them without forcing a re-bind
  // on every render of the page.
  const handleTmuxShortcutRef = useSyncRef(handleTmuxShortcut);
  const handleVSCodeShortcutRef = useSyncRef(handleVSCodeShortcut);
  const handleNewSessionRef = useSyncRef(handleNewSession);
  const matchingTmuxSessionRef = useSyncRef(matchingTmuxSession);
  const sessionRef = useSyncRef(session);
  const portAvailableRef = useSyncRef(portAvailable);

  // Alt+J / Alt+K — navigate between recent sessions.
  const navNextShortcut = useMemo(() => ({
    id: 'session.nav-next',
    scope: 'session' as const,
    keys: { code: 'KeyJ', alt: true },
    description: 'Go to next session',
    handler: () => jumpToSession(1),
  }), [jumpToSession]);

  const navPrevShortcut = useMemo(() => ({
    id: 'session.nav-prev',
    scope: 'session' as const,
    keys: { code: 'KeyK', alt: true },
    description: 'Go to previous session',
    handler: () => jumpToSession(-1),
  }), [jumpToSession]);

  useShortcut(navNextShortcut);
  useShortcut(navPrevShortcut);

  const switchTmuxShortcut = useMemo(() => ({
    id: 'session.switch-tmux',
    scope: 'session' as const,
    keys: { code: 'KeyT', alt: true },
    description: 'Switch tmux for current session',
    enabled: () => !!matchingTmuxSessionRef.current,
    handler: () => handleTmuxShortcutRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const openVscodeShortcut = useMemo(() => ({
    id: 'session.open-vscode',
    scope: 'session' as const,
    keys: { code: 'KeyV', alt: true },
    description: 'Open current session in VS Code',
    enabled: () => !!sessionRef.current,
    handler: () => handleVSCodeShortcutRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const newSessionShortcut = useMemo(() => ({
    id: 'session.new-session',
    scope: 'session' as const,
    keys: { code: 'KeyC', alt: true },
    description: 'Create new session in current project',
    enabled: () => !!sessionRef.current && portAvailableRef.current,
    handler: () => handleNewSessionRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  useShortcut(switchTmuxShortcut);
  useShortcut(openVscodeShortcut);
  useShortcut(newSessionShortcut);

  // Alt+M — pop open the model-change palette via the composer's
  // CustomEvent bridge. The event listener lives on the composer
  // itself (see lib/composerSubmit + the assistant composer).
  const changeModelShortcut = useMemo(() => ({
    id: 'session.change-model',
    scope: 'session' as const,
    keys: { code: 'KeyM', alt: true },
    description: 'Change model via palette',
    handler: () => {
      const el = document.querySelector('.oc-composer-input') as HTMLTextAreaElement | null;
      if (el) {
        el.value = '/model ';
        el.dispatchEvent(new CustomEvent('oc-model-picker-open', { detail: '' }));
        el.focus();
      }
    },
  }), []);

  useShortcut(changeModelShortcut);

  // Expose the synced refs so the page can use them where it
  // currently passes them to other code (e.g. SSE handler reading
  // sessionRef / portAvailableRef).
  return { sessionRef, portAvailableRef };
}
