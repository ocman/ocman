// Slash-command table for the session composer. Each entry is a small
// pure-ish function over a `CommandContext`; `useSessionActions` builds
// the context and dispatches. Commands marked `live` require a reachable
// OpenCode port and are skipped silently otherwise; the rest are
// client-side toggles or ocman-only actions that work offline.

import type { MutableRefObject } from 'react';
import { flushSync } from 'react-dom';
import { api, type Message, type Part, type PlatformCapabilities } from '../../lib/api';
import { createSessionWithLaunch } from '../../lib/createSessionWithLaunch';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { copyTextToClipboard, copyToClipboard } from '../../lib/clipboard';
import { remoteLog } from '../../lib/remoteLog';
import { projectRootForDirectory } from '../../lib/worktrees';
import { downloadSessionMarkdown, serializeSessionMarkdown } from '../../lib/exportMarkdown';
import type { UsePendingSendResult } from './usePendingSend';

export interface CommandSession {
  id: string;
  platform: string;
  remoteId?: string;
  directory: string;
  title?: string;
  timeUpdated: number;
}

export interface CommandContext {
  session: CommandSession;
  portAvailable: boolean;
  caps: Pick<PlatformCapabilities, 'fork' | 'move'>;
  tmuxAvailable: boolean;
  pending: UsePendingSendResult;
  recentSessionsRef: MutableRefObject<Array<{ id: string }>>;
  messagesRef: MutableRefObject<Message[]>;
  partsRef: MutableRefObject<Part[]>;
  archiveSession: (platform: string, id: string, timeUpdated: number, archive: boolean) => Promise<unknown>;
  createSession: (directory: string, platform?: string, title?: string) => Promise<{ id: string }>;
  launchOpencodeInTmux: (directory: string, remoteId?: string) => Promise<{ session: string }>;
  seedNewSession: (id: string, directory: string, platform: string, title?: string, remoteId?: string) => void;
  navigate: (to: string) => void;
  navigateToSession: (id: string) => void;
  openWorktreeForm: (opts: { projectDir: string; branch?: string; parentSessionId?: string }) => void;
  handleCompact: () => Promise<void>;
  handleNewSession: (title?: string) => Promise<void>;
  handleTmuxShortcut: () => void;
  handleVSCodeShortcut: () => void;
  setShowRenameModal: (show: boolean) => void;
  setShowForkPicker: (show: boolean) => void;
  setShowMovePicker: (show: boolean) => void;
  setShowRenameToast: (show: boolean) => void;
  setShowDisconnectedToast: (show: boolean) => void;
  setRestartToastMessage: (message: string | null) => void;
  setCopyToastMessage: (message: string | null) => void;
  reloadCapabilities?: () => void;
  refreshThread?: () => Promise<void>;
}

export interface SlashCommand {
  /** Requires `portAvailable`; skipped silently when the port is down. */
  live?: boolean;
  run: (ctx: CommandContext, args: string, command: string) => Promise<void> | void;
}

const archive: SlashCommand = {
  run: async ({ session, recentSessionsRef, archiveSession, navigateToSession, navigate }) => {
    const recentSessions = recentSessionsRef.current;
    const idx = recentSessions.findIndex((s) => s.id === session.id);
    const nextSession = recentSessions[idx + 1] ?? recentSessions[idx - 1];
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
    } catch (e) {
      remoteLog.error('Failed to archive session', e);
      return;
    }
    // Remember the just-closed session so it can be reopened via the
    // Alt+Shift+N "reopen last closed session" shortcut.
    useApiStore.getState().pushClosedSession({
      platform: session.platform,
      id: session.id,
      timeUpdated: session.timeUpdated,
    });
    if (nextSession) {
      navigateToSession(nextSession.id);
    } else {
      flushSync(() => {
        navigate('/');
      });
    }
  },
};

const worktree: SlashCommand = {
  run: ({ session, openWorktreeForm }, args) => {
    openWorktreeForm({
      projectDir: session.directory,
      branch: args.trim() || undefined,
      // Inherit this session's always-allow permissions (#101).
      parentSessionId: session.id,
    });
  },
};

const restartOpencode: SlashCommand = {
  run: async ({ session, pending, setRestartToastMessage, reloadCapabilities }, args) => {
    const flags = args.trim() ? args.trim().toLowerCase().split(/\s+/) : [];
    if (flags.some((flag) => flag !== 'all' && flag !== 'now') || new Set(flags).size !== flags.length) {
      pending.fail('Usage: /restart-opencode [all] [now]');
      return;
    }
    const all = flags.includes('all');
    const force = flags.includes('now');
    pending.begin('/restart-opencode');
    setRestartToastMessage(force ? 'Checking running sessions...' : 'Restarting OpenCode when sessions are idle...');
    try {
      let result = await api.restartOpencode(session.id, { all, force });
      if (result.confirmationRequired) {
        const scope = all ? 'all managed OpenCode instances' : 'this managed OpenCode instance';
        if (!window.confirm(`Running OpenCode instances and sessions will be stopped. Force restart ${scope}?`)) {
          pending.clear();
          setRestartToastMessage(null);
          return;
        }
        result = await api.restartOpencode(session.id, { all, force: true, confirmed: true });
      }
      pending.clear();
      setRestartToastMessage(result.restarted === 1 ? 'Restarted OpenCode' : `Restarted ${result.restarted ?? 0} OpenCode instances`);
      // The new instance re-reads its config, so the agent catalog
      // and model list we fetched from the old one may be stale.
      reloadCapabilities?.();
    } catch (e) {
      setRestartToastMessage(null);
      remoteLog.error('Failed to restart OpenCode', e);
      pending.fail(e instanceof Error ? e.message : 'Unknown error');
    }
  },
};

const details: SlashCommand = {
  // Pure client-side UI toggle — works regardless of live port.
  run: () => { useUiStore.getState().toggleToolDetails(); },
};

// Display-only toggle for reasoning/thinking blocks (#290). Runs
// regardless of `portAvailable` — it never touches the agent, it just
// flips ocman's own render preference. Accepts optional `on`/`off`
// args; a bare `/thinking` flips the current value.
const thinking: SlashCommand = {
  run: (_ctx, args) => {
    const arg = args.trim().toLowerCase();
    const ui = useUiStore.getState();
    if (arg === 'on' || arg === 'show') ui.setShowReasoning(true);
    else if (arg === 'off' || arg === 'hide') ui.setShowReasoning(false);
    else ui.toggleShowReasoning();
  },
};

const exportMarkdown: SlashCommand = {
  run: ({ session, messagesRef, partsRef }) => {
    downloadSessionMarkdown(session.title, messagesRef.current, partsRef.current);
  },
};

// /sessions (aliases /resume, /continue): thin shim onto the existing
// command palette, whose default mode is the session switcher (#292).
const sessions: SlashCommand = {
  run: () => { useUiStore.getState().openCommandPalette(); },
};

const share: SlashCommand = {
  run: async ({ session, setRestartToastMessage }) => {
    // Share ocman's OWN session URL (issue #294) — not OpenCode's
    // cloud share. The browser is already talking to this ocman
    // instance, so window.location.origin is the reachable address
    // (honours the actual bind address / any reverse proxy).
    const url = `${window.location.origin}/session/${encodeURIComponent(session.id)}`;
    const ok = await copyToClipboard(url);
    setRestartToastMessage(
      ok
        ? 'Session link copied (reachable only where this ocman instance is)'
        : 'Could not copy link to clipboard',
    );
  },
};

const copy: SlashCommand = {
  run: async ({ session, messagesRef, partsRef, setCopyToastMessage }) => {
    const transcript = serializeSessionMarkdown(session.title, messagesRef.current, partsRef.current);
    const ok = await copyTextToClipboard(transcript);
    setCopyToastMessage(ok ? 'Transcript copied' : 'Copy failed — clipboard unavailable');
  },
};

// Unlike `live` commands, undo/redo tell the user why nothing happened.
const undoRedo: SlashCommand = {
  run: async ({ session, portAvailable, messagesRef, refreshThread, setShowDisconnectedToast }, _args, command) => {
    if (!portAvailable) {
      setShowDisconnectedToast(true);
      return;
    }
    try {
      if (command === 'undo') {
        const last = messagesRef.current.at(-1);
        if (!last) return;
        await api.revertSession(session.id, last.id);
      } else {
        await api.unrevertSession(session.id);
      }
      await refreshThread?.();
    } catch (e) {
      remoteLog.error(`Failed to ${command} session`, e);
    }
  },
};

const compact: SlashCommand = { live: true, run: ({ handleCompact }) => handleCompact() };

const fork: SlashCommand = {
  live: true,
  run: ({ caps, setShowForkPicker }) => { if (caps.fork) setShowForkPicker(true); },
};

const move: SlashCommand = {
  live: true,
  run: ({ caps, setShowMovePicker }) => { if (caps.move) setShowMovePicker(true); },
};

const newSession: SlashCommand = {
  live: true,
  run: ({ handleNewSession }, args) => handleNewSession(args.trim() || undefined),
};

const clear: SlashCommand = {
  live: true,
  run: async (ctx, args) => {
    const { session, createSession, launchOpencodeInTmux, tmuxAvailable, archiveSession, seedNewSession, navigateToSession } = ctx;
    let newId: string | undefined;
    let newDirectory = session.directory;
    const clearTitle = args.trim() || undefined;
    try {
      const res = await createSessionWithLaunch(
        { createSession, launchOpencodeInTmux, tmuxAvailable },
        {
          directory: session.directory,
          fallbackDirectory: projectRootForDirectory(session.directory),
          platform: session.platform,
          remoteId: session.remoteId,
          title: clearTitle,
        },
      );
      newId = res.id;
      newDirectory = res.directory ?? session.directory;
    } catch (e) {
      remoteLog.error('Failed to create session', e);
      return;
    }
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
    } catch (e) {
      remoteLog.error('Failed to archive session', e);
    }
    if (newId) {
      seedNewSession(newId, newDirectory, session.platform, clearTitle, session.remoteId);
      navigateToSession(newId);
    }
  },
};

const tmux: SlashCommand = { live: true, run: ({ handleTmuxShortcut }) => handleTmuxShortcut() };
const vscode: SlashCommand = { live: true, run: ({ handleVSCodeShortcut }) => handleVSCodeShortcut() };

const rename: SlashCommand = {
  live: true,
  run: async ({ session, setShowRenameToast, setShowRenameModal }, args) => {
    const newTitle = args.trim();
    if (!newTitle) {
      setShowRenameModal(true);
      return;
    }
    try {
      await api.renameSession(session.id, newTitle);
      // Optimistically update the sidebar store so the renamed
      // title shows immediately instead of waiting for the 3s poll.
      useApiStore.getState().patchRecentSession(session.id, { title: newTitle });
      setShowRenameToast(true);
    } catch (e) {
      remoteLog.error('Failed to rename session', e);
    }
  },
};

export const SLASH_COMMANDS: Readonly<Record<string, SlashCommand>> = {
  archive,
  worktree,
  wt: worktree,
  'restart-opencode': restartOpencode,
  details,
  thinking,
  export: exportMarkdown,
  sessions,
  resume: sessions,
  continue: sessions,
  share,
  copy,
  undo: undoRedo,
  redo: undoRedo,
  compact,
  fork,
  move,
  new: newSession,
  clear,
  tmux,
  vscode,
  rename,
};

/**
 * Run a built-in slash command. Returns false when `command` isn't one
 * (caller forwards it to the platform) — including when a `live`
 * command is issued with the port down, which is a silent no-op.
 */
export async function runSlashCommand(ctx: CommandContext, command: string, args: string): Promise<boolean> {
  const entry = SLASH_COMMANDS[command];
  if (!entry) return false;
  if (entry.live && !ctx.portAvailable) return true;
  await entry.run(ctx, args, command);
  return true;
}
