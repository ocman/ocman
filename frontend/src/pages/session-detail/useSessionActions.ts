import { useCallback, useState } from 'react';
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
import { openVSCode } from '../../lib/shortcuts';
import type { UsePendingSendResult } from './usePendingSend';
import { remoteLog } from '../../lib/remoteLog';

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
  activeModel: string;
  activeAgent: string;
  /** Mutable ref to the recent sessions list — read inside handleCommand. */
  recentSessionsRef: MutableRefObject<Array<{ id: string }>>;
  tmuxAvailable: boolean;
  failedSends: FailedSend[];
  setFailedSends: Dispatch<SetStateAction<FailedSend[]>>;
  /** Pending-slot hook output. handleSend records the optimistic
   *  bubble here instead of injecting temp-id messages into the
   *  page's messages array. */
  pending: UsePendingSendResult;
  navigate: (to: string) => void;
  navigateToSession: (id: string) => void;
  openWorktreeForm: (opts: { projectDir: string; branch?: string }) => void;
  handleCompact: () => Promise<void>;
  handleNewSession: (title?: string) => Promise<void>;
  handleTmuxShortcut: () => void;
  setShowRenameModal: Dispatch<SetStateAction<boolean>>;
  setShowRenameToast: Dispatch<SetStateAction<boolean>>;
  setShowDisconnectedToast: Dispatch<SetStateAction<boolean>>;
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
  activeModel,
  activeAgent,
  recentSessionsRef,
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
}: UseSessionActionsOptions): UseSessionActionsResult {
  const [awaitingAssistantResponse, setAwaitingAssistantResponse] = useState(false);

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
      await sendMessage(session.id, text, images, model, agent, reasoning);
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
    // Begin a pending send — generates a stable id, sets the bubble
    // visible immediately. The composer's send button stays
    // responsive for the next prompt as soon as performSend kicks
    // off.
    const entryId = pending.begin(text, images, {
      model: selectedModel || activeModel || undefined,
      agent: selectedAgent || activeAgent || undefined,
      reasoning: selectedReasoning || undefined,
    });
    await performSend(
      entryId,
      text,
      images,
      selectedModel || activeModel || undefined,
      selectedAgent || activeAgent || undefined,
      selectedReasoning || undefined,
    );
  }, [activeAgent, activeModel, pendingPermission, pendingQuestion, performSend, portAvailable, selectedAgent, selectedModel, selectedReasoning, session, pending]);

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

  const handleShell = useCallback(async (command: string) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;
    const agent = selectedAgent || activeAgent || 'build';
    try {
      await api.runShell(session.id, command, agent);
    } catch (e) {
      remoteLog.error('Failed to run shell command', e);
      // Shell errors don't go through the pending slot — they're
      // rare enough that we just log and rely on the platform to
      // surface the error.
    }
  }, [activeAgent, pendingPermission, pendingQuestion, portAvailable, selectedAgent, session]);

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
      });
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
      const clearTitle = args.trim() || undefined;
      try {
        const res = await createSessionWithLaunch(
          { createSession, launchOpencodeInTmux, tmuxAvailable, onStatusChange: () => {} },
          { directory: session.directory, title: clearTitle },
        );
        newId = res.id;
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
        seedNewSession(newId, session.directory, session.platform, clearTitle);
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
      if (args.trim()) {
        try {
          await api.renameSession(session.id, args.trim());
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
      model: selectedModel || activeModel || undefined,
      agent: selectedAgent || activeAgent || undefined,
    });
    try {
      await api.executeCommand(
        session.id,
        command,
        args,
        selectedModel || activeModel || undefined,
        selectedAgent || activeAgent || undefined,
      );
    } catch (e) {
      remoteLog.error('Failed to execute command', e);
      pending.fail(e instanceof Error ? e.message : 'Unknown error');
    }
  }, [activeAgent, activeModel, archiveSession, createSession, launchOpencodeInTmux, tmuxAvailable, seedNewSession, handleCompact, handleNewSession, handleTmuxShortcut, handleVSCodeShortcut, navigate, navigateToSession, openWorktreeForm, portAvailable, recentSessionsRef, selectedAgent, selectedModel, session, setShowRenameModal, setShowRenameToast, pending]);

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
  };
}
