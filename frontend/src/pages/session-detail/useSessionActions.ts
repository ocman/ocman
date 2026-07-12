import { useCallback, useRef, useState } from 'react';
import type { Dispatch, SetStateAction, MutableRefObject } from 'react';
import { flushSync } from 'react-dom';
import { api, type PlatformCapabilities } from '../../lib/api';
import type { AttachedImage } from '../../components/assistant/Composer';
import type { PendingPermission } from '../../lib/sseHelpers';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';
import {
  recordFailedSend,
  removeFailedSend,
  type FailedSend,
} from '../../lib/failedSends';
import { createSessionWithLaunch } from '../../lib/createSessionWithLaunch';
import { useApiStore } from '../../lib/apiStore';
import { useUiStore } from '../../lib/uiStore';
import { openVSCode } from '../../lib/shortcuts';
import { copyTextToClipboard, copyToClipboard } from '../../lib/clipboard';
import type { UsePendingSendResult } from './usePendingSend';
import { remoteLog } from '../../lib/remoteLog';
import { projectRootForDirectory } from '../../lib/worktrees';
import { downloadSessionMarkdown, serializeSessionMarkdown } from '../../lib/exportMarkdown';
import type { Message, Part } from '../../lib/api';

// Narrowed session shape — only the fields needed by these handlers.
interface ActionSession {
  id: string;
  platform: string;
  directory: string;
  title?: string;
  status?: string;
  timeUpdated: number;
}

export interface UseSessionActionsOptions {
  session: ActionSession | null;
  portAvailable: boolean;
  caps: PlatformCapabilities;
  pendingPermission: PendingPermission | null;
  pendingQuestion: PendingQuestion | null;
  selectedModel: string;
  selectedAgent: string;
  selectedReasoning: string;
  activeAgent: string;
  /** Mutable ref to the recent sessions list — read inside handleCommand. */
  recentSessionsRef: MutableRefObject<Array<{ id: string }>>;
  /** Mutable refs to the current transcript — read by `/export`. Refs
   *  (not values) so handleCommand doesn't re-bind on every message. */
  messagesRef: MutableRefObject<Message[]>;
  partsRef: MutableRefObject<Part[]>;
  /**
   * Mutable ref reflecting whether the session is currently streaming
   * an assistant response. Read inside `handleShell` so a `!`-prefixed
   * shell command issued mid-stream is queued instead of POSTed into a
   * busy session (OpenCode rejects `/shell` while the session is
   * generating). SessionDetail keeps this ref in sync with `isRunning`.
   */
  isRunningRef: MutableRefObject<boolean>;
  tmuxAvailable: boolean;
  failedSends: FailedSend[];
  setFailedSends: Dispatch<SetStateAction<FailedSend[]>>;
  /** Pending-slot hook output. handleSend records the optimistic
   *  bubble here instead of injecting temp-id messages into the
   *  page's messages array. */
  pending: UsePendingSendResult;
  navigate: (to: string) => void;
  navigateToSession: (id: string) => void;
  openWorktreeForm: (opts: { projectDir: string; branch?: string; parentSessionId?: string }) => void;
  handleCompact: () => Promise<void>;
  handleNewSession: (title?: string) => Promise<void>;
  handleTmuxShortcut: () => void;
  setShowRenameModal: Dispatch<SetStateAction<boolean>>;
  setShowRenameToast: Dispatch<SetStateAction<boolean>>;
  setShowDisconnectedToast: Dispatch<SetStateAction<boolean>>;
  setRestartToastMessage: Dispatch<SetStateAction<string | null>>;
  /** Transient feedback toast for the `/copy` command. */
  setCopyToastMessage: Dispatch<SetStateAction<string | null>>;
}

export interface UseSessionActionsResult {
  awaitingAssistantResponse: boolean;
  setAwaitingAssistantResponse: Dispatch<SetStateAction<boolean>>;
  handleSend: (text: string, images?: AttachedImage[]) => Promise<void>;
  handleRetrySend: (entryId: string) => void;
  handleDismissFailedSend: (entryId: string) => void;
  handleShell: (command: string) => Promise<void>;
  handleAbort: () => Promise<void>;
  handleVSCodeShortcut: () => void;
  handleCommand: (command: string, args: string) => Promise<void>;
  /**
   * The shell command waiting for the current assistant turn to
   * finish, or null when nothing is queued. Surfaced in the composer
   * so the user knows the command was accepted and what we're waiting
   * for. Only one command is queued at a time — re-submitting while a
   * command is already queued replaces it.
   */
  queuedShellCommand: string | null;
  /** Drop the queued shell command without running it. */
  cancelQueuedShell: () => void;
  /**
   * Run the queued shell command now (if any) provided the session is
   * idle. Called by SessionDetail when `isRunning` transitions
   * true → false.
   */
  flushQueuedShell: () => void;
}

/**
 * Encapsulates the session-level send/command/shell/abort actions plus the
 * failed-send list.
 *
 * Compared to the legacy hook, this version is much smaller because the
 * optimistic-user-message dance moved into `usePendingSend`. The flow is:
 *
 *   1. `handleSend` calls `pending.begin(text, images, opts)`. The bubble
 *      is materialised at render time from that slot.
 *   2. On success: SSE delivers the server's `message.created`; pending
 *      auto-clears via `observeMessages`.
 *   3. On failure: `pending.fail(message)` puts the entry into the failed
 *      state; persistent `failedSends` stores it for refresh recovery.
 *
 * No temp-* ids. No reparenting. No ghost injection.
 */
export function useSessionActions({
  session,
  portAvailable,
  caps,
  pendingPermission,
  pendingQuestion,
  selectedModel,
  selectedAgent,
  selectedReasoning,
  activeAgent,
  recentSessionsRef,
  messagesRef,
  partsRef,
  isRunningRef,
  tmuxAvailable,
  failedSends,
  setFailedSends,
  pending,
  navigate,
  navigateToSession,
  openWorktreeForm,
  handleCompact,
  handleNewSession,
  handleTmuxShortcut,
  setShowRenameModal,
  setShowRenameToast,
  setShowDisconnectedToast,
  setRestartToastMessage,
  setCopyToastMessage,
}: UseSessionActionsOptions): UseSessionActionsResult {
  const [awaitingAssistantResponse, setAwaitingAssistantResponse] = useState(false);
  // Shell command waiting for the current turn to finish. Mirrored in
  // a ref so the idle-transition flush (driven by an effect in
  // SessionDetail) reads the latest value without re-binding.
  const [queuedShellCommand, setQueuedShellCommand] = useState<string | null>(null);
  const queuedShellRef = useRef<string | null>(null);

  const sendMessage = useApiStore((state) => state.sendMessage);
  const abortSession = useApiStore((state) => state.abortSession);
  const archiveSession = useApiStore((state) => state.archiveSession);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const seedNewSession = useApiStore((state) => state.seedNewSession);

  // Internal send. Drives both `handleSend` (fresh prompt) and
  // `handleRetrySend` (replay of a previously failed send) so the
  // pending entry's stable id survives across retries.
  const performSend = useCallback(async (
    entryId: string,
    text: string,
    images: AttachedImage[] | undefined,
    model: string | undefined,
    agent: string | undefined,
    reasoning: string | undefined,
  ) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;

    setAwaitingAssistantResponse(true);

    try {
      await sendMessage(session.id, text, images, model, agent, reasoning, session.platform);
      // Success — drop any prior failed entry for this id.
      // The pending slot auto-clears when SSE delivers the real
      // user message via `observeMessages`.
      setFailedSends((prev) => prev.filter((e) => e.id !== entryId));
      removeFailedSend(session.id, entryId);
    } catch (e) {
      setAwaitingAssistantResponse(false);
      remoteLog.error('Failed to send message', e);
      const msg = e instanceof Error ? e.message : '';
      if (msg.includes('no running OpenCode instance')) {
        // Drop the optimistic bubble — the user wants to launch
        // OpenCode, not retry blindly.
        pending.clear();
        setFailedSends((prev) => prev.filter((e) => e.id !== entryId));
        removeFailedSend(session.id, entryId);
        setShowDisconnectedToast(true);
        return;
      }
      pending.fail(msg || 'Unknown error');
      const failed: FailedSend = {
        id: entryId,
        text,
        images,
        model,
        agent,
        reasoning,
        error: msg || 'Unknown error',
        failedAt: Date.now(),
      };
      setFailedSends((prev) => {
        const idx = prev.findIndex((e) => e.id === entryId);
        if (idx >= 0) return prev.map((e, i) => (i === idx ? failed : e));
        return [...prev, failed];
      });
      recordFailedSend(session.id, failed);
    }
  }, [pendingPermission, pendingQuestion, portAvailable, sendMessage, session, setFailedSends, pending, setShowDisconnectedToast]);

  const handleSend = useCallback(async (text: string, images?: AttachedImage[]) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;
    // Mid-turn: the POST will be queued server-side (#58), not sent. Do
    // NOT show an optimistic thread bubble — a user message that follows
    // an unfinished assistant turn renders as a big "QUEUED" bubble in
    // the thread, which duplicates the compact queue list under the
    // composer. Just POST (→ enqueue); the queue list surfaces it via the
    // queue.updated broadcast.
    if (isRunningRef.current) {
      // POST → enqueue server-side. The queue.updated broadcast (reliable,
      // full-state) surfaces it in the compact list; no optimistic add.
      await sendMessage(
        session.id,
        text,
        images,
        selectedModel || undefined,
        selectedAgent || activeAgent || undefined,
        selectedReasoning || undefined,
        session.platform,
        true, // queue: agent is mid-turn — hold, don't drain into the turn
      ).catch((e) => remoteLog.error('Failed to queue message', e));
      return;
    }
    // Idle: begin a pending send — generates a stable id, sets the bubble
    // visible immediately. The composer's send button stays
    // responsive for the next prompt as soon as performSend kicks
    // off.
    const entryId = pending.begin(text, images, {
      model: selectedModel || undefined,
      agent: selectedAgent || activeAgent || undefined,
      reasoning: selectedReasoning || undefined,
    });
    await performSend(
      entryId,
      text,
      images,
      selectedModel || undefined,
      selectedAgent || activeAgent || undefined,
      selectedReasoning || undefined,
    );
  }, [activeAgent, isRunningRef, pendingPermission, pendingQuestion, performSend, portAvailable, selectedAgent, selectedModel, selectedReasoning, sendMessage, session, pending]);

  // Replay a previously failed send. Reuses the entry's text /
  // images / id so the bubble stays in place — the failed banner
  // either disappears on success or updates with the new error.
  const handleRetrySend = useCallback((entryId: string) => {
    if (!session) return;
    const entry = failedSends.find((e) => e.id === entryId);
    if (!entry) return;
    // Re-establish the pending bubble so the user can see their
    // prompt while the retry is in flight. Then perform the send
    // against the SAME id so the failedSends entry is keyed
    // identically.
    pending.begin(entry.text, entry.images, {
      model: entry.model,
      agent: entry.agent,
      reasoning: entry.reasoning,
    });
    void performSend(
      entryId,
      entry.text,
      entry.images,
      entry.model,
      entry.agent,
      entry.reasoning,
    );
  }, [failedSends, performSend, session, pending]);

  // Drop a failed send (without retrying). Removes the persisted
  // entry and the pending bubble.
  const handleDismissFailedSend = useCallback((entryId: string) => {
    if (!session) return;
    setFailedSends((prev) => prev.filter((e) => e.id !== entryId));
    removeFailedSend(session.id, entryId);
    pending.clear();
  }, [session, setFailedSends, pending]);

  // Actually POST the shell command to the platform. Separated from
  // handleShell so both the immediate path and the queued-flush path
  // share one implementation.
  const runShellNow = useCallback(async (command: string) => {
    if (!session || !portAvailable) return;
    const agent = selectedAgent || activeAgent || 'build';
    try {
      await api.runShell(session.id, command, agent);
    } catch (e) {
      remoteLog.error('Failed to run shell command', e);
      // Shell errors don't go through the pending slot — they're
      // rare enough that we just log and rely on the platform to
      // surface the error.
    }
  }, [activeAgent, portAvailable, selectedAgent, session]);

  const handleShell = useCallback(async (command: string) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;
    // OpenCode rejects POST /session/{id}/shell while the session is
    // streaming an assistant response. Rather than fire-and-fail
    // silently, queue the command and run it once the turn completes
    // (flushQueuedShell, driven by SessionDetail's isRunning
    // transition). The composer shows what we're waiting for.
    if (isRunningRef.current) {
      queuedShellRef.current = command;
      setQueuedShellCommand(command);
      return;
    }
    await runShellNow(command);
  }, [isRunningRef, pendingPermission, pendingQuestion, portAvailable, runShellNow, session]);

  const cancelQueuedShell = useCallback(() => {
    queuedShellRef.current = null;
    setQueuedShellCommand(null);
  }, []);

  const flushQueuedShell = useCallback(() => {
    const command = queuedShellRef.current;
    if (!command) return;
    if (isRunningRef.current) return; // still busy — wait for the next idle
    queuedShellRef.current = null;
    setQueuedShellCommand(null);
    void runShellNow(command);
  }, [isRunningRef, runShellNow]);

  const handleAbort = useCallback(async () => {
    if (!session || !portAvailable || !caps.abort) return;
    try {
      await abortSession(session.id);
    } catch (e) {
      remoteLog.error('Failed to abort session', e);
    }
  }, [abortSession, caps.abort, portAvailable, session]);

  const handleVSCodeShortcut = useCallback(() => {
    if (!session) return;
    openVSCode(session.directory);
  }, [session]);

  const handleCommand = useCallback(async (command: string, args: string) => {
    if (!session) return;

    if (command === 'archive') {
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
      return;
    }

    if (command === 'wt') {
      openWorktreeForm({
        projectDir: session.directory,
        branch: args.trim() || undefined,
        // Inherit this session's always-allow permissions (#101).
        parentSessionId: session.id,
      });
      return;
    }

    if (command === 'restart-opencode') {
      pending.begin('/restart-opencode');
      setRestartToastMessage('Restarting OpenCode...');
      try {
        await api.restartOpencode(session.id);
        pending.clear();
        setRestartToastMessage('Restarted OpenCode');
      } catch (e) {
        setRestartToastMessage(null);
        remoteLog.error('Failed to restart OpenCode', e);
        pending.fail(e instanceof Error ? e.message : 'Unknown error');
      }
      return;
    }

    if (command === 'details') {
      // Pure client-side UI toggle — works regardless of live port.
      useUiStore.getState().toggleToolDetails();
      return;
    }

    // Display-only toggle for reasoning/thinking blocks (#290). Runs
    // regardless of `portAvailable` — it never touches the agent, it
    // just flips ocman's own render preference. Accepts optional
    // `on`/`off` args; a bare `/thinking` flips the current value.
    if (command === 'thinking') {
      const arg = args.trim().toLowerCase();
      const ui = useUiStore.getState();
      if (arg === 'on' || arg === 'show') ui.setShowReasoning(true);
      else if (arg === 'off' || arg === 'hide') ui.setShowReasoning(false);
      else ui.toggleShowReasoning();
      return;
    }

    if (command === 'export') {
      downloadSessionMarkdown(session.title, messagesRef.current, partsRef.current);
      return;
    }

    // /sessions (aliases /resume, /continue): thin shim onto the existing
    // command palette, whose default mode is the session switcher (#292).
    if (command === 'sessions' || command === 'resume' || command === 'continue') {
      useUiStore.getState().openCommandPalette();
      return;
    }

    if (command === 'share') {
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
      return;
    }

    if (command === 'copy') {
      const transcript = serializeSessionMarkdown(session.title, messagesRef.current, partsRef.current);
      const ok = await copyTextToClipboard(transcript);
      setCopyToastMessage(ok ? 'Transcript copied' : 'Copy failed — clipboard unavailable');
      return;
    }

    if (!portAvailable) return;

    if (command === 'compact') {
      await handleCompact();
      return;
    }

    if (command === 'new') {
      await handleNewSession(args.trim() || undefined);
      return;
    }

    if (command === 'clear') {
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
        seedNewSession(newId, newDirectory, session.platform, clearTitle);
        navigateToSession(newId);
      }
      return;
    }

    if (command === 'tmux') {
      handleTmuxShortcut();
      return;
    }

    if (command === 'vscode') {
      handleVSCodeShortcut();
      return;
    }

    if (command === 'rename') {
      const newTitle = args.trim();
      if (newTitle) {
        try {
          await api.renameSession(session.id, newTitle);
          // Optimistically update the sidebar store so the renamed
          // title shows immediately instead of waiting for the 3s poll.
          useApiStore.getState().patchRecentSession(session.id, { title: newTitle });
          setShowRenameToast(true);
        } catch (e) {
          remoteLog.error('Failed to rename session', e);
        }
      } else {
        setShowRenameModal(true);
      }
      return;
    }

    // Generic slash-command — surface the user's typed command via
    // the pending slot so the user sees something in the thread
    // immediately. The platform will send the real assistant
    // response via SSE.
    const displayText = args ? `/${command} ${args}` : `/${command}`;
    pending.begin(displayText, undefined, {
      model: selectedModel || undefined,
      agent: selectedAgent || activeAgent || undefined,
    });
    try {
      await api.executeCommand(
        session.id,
        command,
        args,
        selectedModel || undefined,
        selectedAgent || activeAgent || undefined,
      );
    } catch (e) {
      remoteLog.error('Failed to execute command', e);
      pending.fail(e instanceof Error ? e.message : 'Unknown error');
    }
  }, [activeAgent, archiveSession, createSession, launchOpencodeInTmux, tmuxAvailable, seedNewSession, handleCompact, handleNewSession, handleTmuxShortcut, handleVSCodeShortcut, navigate, navigateToSession, openWorktreeForm, portAvailable, recentSessionsRef, messagesRef, partsRef, selectedAgent, selectedModel, session, setShowRenameModal, setShowRenameToast, setRestartToastMessage, setCopyToastMessage, pending]);

  return {
    awaitingAssistantResponse,
    setAwaitingAssistantResponse,
    handleSend,
    handleRetrySend,
    handleDismissFailedSend,
    handleShell,
    handleAbort,
    handleVSCodeShortcut,
    handleCommand,
    queuedShellCommand,
    cancelQueuedShell,
    flushQueuedShell,
  };
}
