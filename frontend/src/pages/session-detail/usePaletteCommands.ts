// usePaletteCommands — wires the scoped palette command dispatcher for the
// session-detail page. Each palette command (model picker, agent picker,
// compact, archive, rename, …) is handled here so the dispatch logic lives
// in one place instead of being inlined in SessionDetail.
//
// The hook reads palette state from uiStore and mutates page-level state
// through stable callbacks/refs to avoid re-running the effect on every
// render. Callers must pass stable refs and callbacks.

import { useEffect, useRef } from 'react';
import { useUiStore } from '../../lib/uiStore';
import { openVSCode } from '../../lib/shortcuts';
import { api } from '../../lib/api';
import { remoteLog } from '../../lib/remoteLog';
export interface TmuxHandle {
  available: boolean;
  sessions: { name: string }[];
  switchSession: (name: string) => Promise<void>;
}

export interface PaletteCommandsOptions {
  /** Stable ref to the current session object. */
  sessionRef: React.MutableRefObject<{ id: string; platform: string; directory: string; title?: string; timeUpdated: number } | null>;
  /** Stable ref to the current archive action. */
  archiveSessionRef: React.MutableRefObject<(platform: string, id: string, timeUpdated: number, archive: boolean) => Promise<unknown>>;
  /** Stable ref to navigate. */
  navigateRef: React.MutableRefObject<(to: string | number) => void>;
  /** Stable ref to portAvailable. */
  portAvailableRef: React.MutableRefObject<boolean>;
  /** Stable ref to caps. */
  capsRef: React.MutableRefObject<{ compact?: boolean; permissionRules?: boolean }>;
  /** Stable ref to selectedModel. */
  selectedModelRef: React.MutableRefObject<string>;
  /** Stable ref to activeModel. */
  activeModelRef: React.MutableRefObject<string>;
  /** Current tmux handle — captured in a ref internally. */
  tmux: TmuxHandle;
  setSelectedReasoning: (v: string) => void;
  setShowRenameModal: (v: boolean) => void;
  openModelPicker: () => void;
  openAgentPicker: () => void;
}

export function usePaletteCommands({
  sessionRef,
  archiveSessionRef,
  navigateRef,
  portAvailableRef,
  capsRef,
  selectedModelRef,
  activeModelRef,
  tmux,
  setSelectedReasoning,
  setShowRenameModal,
  openModelPicker,
  openAgentPicker,
}: PaletteCommandsOptions): void {
  const tmuxRef = useRef(tmux);
  useEffect(() => { tmuxRef.current = tmux; }, [tmux]);
  // Stable refs for setters so the effect only re-runs when paletteCommand changes.
  const setSelectedReasoningRef = useRef(setSelectedReasoning);
  useEffect(() => { setSelectedReasoningRef.current = setSelectedReasoning; }, [setSelectedReasoning]);
  const setShowRenameModalRef = useRef(setShowRenameModal);
  useEffect(() => { setShowRenameModalRef.current = setShowRenameModal; }, [setShowRenameModal]);

  const paletteCommand = useUiStore((s) => s.paletteCommand);

  useEffect(() => {
    if (!paletteCommand || paletteCommand.kind !== 'scoped') return;
    useUiStore.getState().closePalette();

    const cmd = paletteCommand;

    const t = tmuxRef.current;
    if (cmd.id === 'scoped.model') {
      openModelPicker();
    } else if (cmd.id === 'scoped.agent') {
      openAgentPicker();
    } else if (cmd.id === 'scoped.variant') {
      setSelectedReasoningRef.current('');
    } else if (cmd.id === 'scoped.permissions' && sessionRef.current && portAvailableRef.current && capsRef.current.permissionRules) {
      window.dispatchEvent(new Event('oc-permission-mode-open'));
    } else if (cmd.id === 'scoped.tmux' && t.available && t.sessions.length > 0) {
      t.switchSession(t.sessions[0].name).catch((err) => remoteLog.error('tmux switch failed', err));
    } else if (cmd.id === 'scoped.vscode' && sessionRef.current) {
      openVSCode(sessionRef.current.directory);
    } else if (cmd.id === 'scoped.archive' && sessionRef.current) {
      const s = sessionRef.current;
      archiveSessionRef.current(s.platform, s.id, s.timeUpdated, true)
        .then(() => navigateRef.current(-1))
        .catch((err) => remoteLog.error('Failed to archive session', err));
    } else if (cmd.id === 'scoped.rename') {
      setShowRenameModalRef.current(true);
    } else if (cmd.id === 'scoped.new-project') {
      useUiStore.getState().openProjectSessionPalette();
    } else if (cmd.id === 'scoped.compact' && sessionRef.current && portAvailableRef.current && capsRef.current.compact) {
      const model = selectedModelRef.current || activeModelRef.current || '';
      const slashIdx = model.indexOf('/');
      const providerID = slashIdx > 0 ? model.slice(0, slashIdx) : '';
      const modelID = slashIdx > 0 ? model.slice(slashIdx + 1) : model;
      api.compactSession(sessionRef.current!.id, providerID, modelID).catch((err) => remoteLog.error('Failed to compact session', err));
    }
  }, [paletteCommand, sessionRef, archiveSessionRef, navigateRef, portAvailableRef, capsRef, selectedModelRef, activeModelRef, openModelPicker, openAgentPicker]);
}
