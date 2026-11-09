import { useMemo } from 'react';
import { useShortcut } from '../../lib/shortcutRegistry';
import { useSyncRef } from '../../lib/useSyncRef';
import type { Session } from '../../lib/api';

export interface UseSessionShortcutsOptions {
  /** Page session — used by the enabled() guards to gate shortcuts
   *  that require an active session. */
  session: Session | null;
  /** Whether the platform process is reachable (gates create). */
  portAvailable: boolean;
  /** Navigate by `direction` in the recent-sessions list. */
  jumpToSession: (direction: 1 | -1) => void;
  /** Open the page directory in VS Code. */
  handleVSCodeShortcut: () => void;
  /** Create a new session in the current project. */
  handleNewSession: (title?: string) => Promise<void>;
  /** Open the user-message jump picker. */
  openMessageJumpPicker: () => void;
  /** Open the model picker owned by the composer. */
  openModelPicker: () => void;
}

/**
 * Registers session navigation, editor, creation and picker actions.
 * Key combinations come from the backend catalog. Stable refs keep the
 * registered handlers and availability checks on the latest page state.
 */
export function useSessionShortcuts({
  session,
  portAvailable,
  jumpToSession,
  handleVSCodeShortcut,
  handleNewSession,
  openMessageJumpPicker,
  openModelPicker,
}: UseSessionShortcutsOptions) {
  // Mirror the latest handler / state values into refs so shortcut
  // descriptors below can reference them without forcing a re-bind
  // on every render of the page.
  const handleVSCodeShortcutRef = useSyncRef(handleVSCodeShortcut);
  const handleNewSessionRef = useSyncRef(handleNewSession);
  const openMessageJumpPickerRef = useSyncRef(openMessageJumpPicker);
  const openModelPickerRef = useSyncRef(openModelPicker);
  const sessionRef = useSyncRef(session);
  const portAvailableRef = useSyncRef(portAvailable);

  // Alt+J / Alt+K — navigate between recent sessions.
  const navNextShortcut = useMemo(() => ({
    id: 'session.nav-next',
    scope: 'session' as const,
    description: 'Go to next session',
    handler: () => jumpToSession(1),
  }), [jumpToSession]);

  const navPrevShortcut = useMemo(() => ({
    id: 'session.nav-prev',
    scope: 'session' as const,
    description: 'Go to previous session',
    handler: () => jumpToSession(-1),
  }), [jumpToSession]);

  useShortcut(navNextShortcut);
  useShortcut(navPrevShortcut);

  const openVscodeShortcut = useMemo(() => ({
    id: 'session.open-vscode',
    scope: 'session' as const,
    description: 'Open current session in VS Code',
    enabled: () => !!sessionRef.current,
    handler: () => handleVSCodeShortcutRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  const newSessionShortcut = useMemo(() => ({
    id: 'session.new-session',
    scope: 'session' as const,
    description: 'Create new session in current project',
    enabled: () => !!sessionRef.current && portAvailableRef.current,
    handler: () => handleNewSessionRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  useShortcut(openVscodeShortcut);
  useShortcut(newSessionShortcut);

  const changeModelShortcut = useMemo(() => ({
    id: 'session.change-model',
    scope: 'session' as const,
    description: 'Change model via palette',
    handler: () => openModelPickerRef.current(),
  }), [openModelPickerRef]);

  useShortcut(changeModelShortcut);

  const messageJumpShortcut = useMemo(() => ({
    id: 'session.jump-to-message',
    scope: 'session' as const,
    description: 'Jump to user message',
    enabled: () => !!sessionRef.current,
    handler: () => openMessageJumpPickerRef.current(),
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }), []);

  useShortcut(messageJumpShortcut);
}
