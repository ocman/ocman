import { useCallback, useRef, useState } from 'react';
import type { Dispatch, SetStateAction, MutableRefObject } from 'react';
import { flushSync } from 'react-dom';
import { api, BackendUnavailableError, type PlatformCapabilities } from '../../lib/api';
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

// A `!command` held until the current turn ends, tagged with the session
// that asked for it so it can never be flushed into a different one.
interface QueuedShell {
  command: string;
  sessionID: string;
}

export interface UseSessionActionsOptions {
  session: ActionSession | null;
  /**
   * The session id from the route (#529). When the URL changes there is
   * a one-commit window before `session` is replaced; any send fired in
   * that window would target the OLD session. Send paths fail closed
   * when `session.id` doesn't match this. Optional so callers without a
   * route context (tests) keep working — when omitted, no guard applies.
   */
  routeSessionId?: string;
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
  setShowForkPicker: Dispatch<SetStateAction<boolean>>;
  setShowMovePicker: Dispatch<SetStateAction<boolean>>;
  setShowRenameToast: Dispatch<SetStateAction<boolean>>;
  setShowDisconnectedToast: Dispatch<SetStateAction<boolean>>;
  setRestartToastMessage: Dispatch<SetStateAction<string | null>>;
  /** Re-fetch the agent catalog + model list. Called after a
   *  successful OpenCode restart, since the fresh instance may have a
   *  different config. */
  reloadCapabilities?: () => void;
  /** Transient feedback toast for the `/copy` command. */
  setCopyToastMessage: Dispatch<SetStateAction<string | null>>;
  refreshThread?: () => Promise<void>;
}

export interface UseSessionActionsResult {
  awaitingAssistantResponse: boolean;
  setAwaitingAssistantResponse: Dispatch<SetStateAction<boolean>>;
  /**
   * Submit a prompt. `queue` is the user's explicit "hold this for the
   * next idle edge" gesture (Ctrl/Cmd+Enter). Without it the prompt is
   * sent straight through, mid-turn included — OpenCode interleaves it
   * into the running turn rather than making the user wait it out.
   */
  handleSend: (text: string, images?: AttachedImage[], queue?: boolean) => Promise<void>;
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
  routeSessionId,
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
  setShowForkPicker,
  setShowMovePicker,
  setShowRenameToast,
  setShowDisconnectedToast,
  setRestartToastMessage,
  reloadCapabilities,
  setCopyToastMessage,
  refreshThread,
}: UseSessionActionsOptions): UseSessionActionsResult {
  const [awaitingAssistantResponse, setAwaitingAssistantResponse] = useState(false);
  // Shell command waiting for the current turn to finish, tagged with the
  // session that asked for it. Mirrored in a ref so the idle-transition
  // flush (driven by an effect in SessionDetail) reads the latest value
  // without re-binding.
  //
  // The session tag is what keeps a `!command` typed in one session from
  // ever running in another: SessionDetail stays mounted across
  // navigation, so a queued command outlives the session it was typed in
  // (and the shell endpoint bypasses the LLM, so a stale one would edit
  // the wrong checkout outright). Visibility is *derived* from the tag
  // rather than cleared by an effect — no clearing effect means no window
  // where the chip shows the previous session's command.
  const [queuedShell, setQueuedShell] = useState<QueuedShell | null>(null);
  const queuedShellRef = useRef<QueuedShell | null>(null);
  const queuedShellCommand = queuedShell !== null
    && session !== null
    && queuedShell.sessionID === session.id
    && (routeSessionId === undefined || session.id === routeSessionId)
    ? queuedShell.command
    : null;

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
    if (!session) return;
    if (!portAvailable) throw new BackendUnavailableError();
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
      if (e instanceof BackendUnavailableError) throw e;
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

  const handleSend = useCallback(async (text: string, images?: AttachedImage[], queue?: boolean) => {
    if (!session) return;
    // #529: the route changed but the session state hasn't caught up
    // yet — sending now would deliver to the OLD session. Drop it.
    if (routeSessionId !== undefined && session.id !== routeSessionId) return;
    if (!portAvailable) throw new BackendUnavailableError();
    if (pendingPermission || pendingQuestion) return;
    // Explicit queue gesture (Ctrl/Cmd+Enter): the server holds this for
    // the next session.idle edge (#58). Do NOT show an optimistic thread
    // bubble — the message isn't going anywhere yet, and the compact
    // queue list under the composer is where it belongs.
    if (queue) {
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
        true, // queue: hold for the next idle edge
      ).catch((e) => {
        remoteLog.error('Failed to queue message', e);
        if (e instanceof BackendUnavailableError) throw e;
      });
      return;
    }
    // Send now (plain Enter), mid-turn included: begin a pending send —
    // generates a stable id, sets the bubble visible immediately. The
    // composer's send button stays responsive for the next prompt as soon
    // as performSend kicks off.
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
  }, [activeAgent, pendingPermission, pendingQuestion, performSend, portAvailable, routeSessionId, selectedAgent, selectedModel, selectedReasoning, sendMessage, session, pending]);

  // Replay a previously failed send. Reuses the entry's text /
  // images / id so the bubble stays in place — the failed banner
  // either disappears on success or updates with the new error.
  const handleRetrySend = useCallback((entryId: string) => {
    if (!session) return;
    // #529: same stale-route guard as handleSend.
    if (routeSessionId !== undefined && session.id !== routeSessionId) return;
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
  }, [failedSends, performSend, routeSessionId, session, pending]);

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
    if (routeSessionId !== undefined && session.id !== routeSessionId) return;
    if (pendingPermission || pendingQuestion) return;
    // OpenCode rejects POST /session/{id}/shell while the session is
    // streaming an assistant response. Rather than fire-and-fail
    // silently, queue the command and run it once the turn completes
    // (flushQueuedShell, driven by SessionDetail's isRunning
    // transition). The composer shows what we're waiting for.
    if (isRunningRef.current) {
      const queued: QueuedShell = { command, sessionID: session.id };
      queuedShellRef.current = queued;
      setQueuedShell(queued);
      return;
    }
    await runShellNow(command);
  }, [isRunningRef, pendingPermission, pendingQuestion, portAvailable, routeSessionId, runShellNow, session]);

  const cancelQueuedShell = useCallback(() => {
    queuedShellRef.current = null;
    setQueuedShell(null);
  }, []);

  const flushQueuedShell = useCallback(() => {
    const queued = queuedShellRef.current;
    if (!queued) return;
    if (isRunningRef.current) return; // still busy — wait for the next idle
    // Drop it either way: an idle edge is the one chance to run it, and a
    // command left in the ref would otherwise fire on some later idle edge
    // after the user navigated back — a delayed surprise.
    queuedShellRef.current = null;
    setQueuedShell(null);
    if (!session || queued.sessionID !== session.id) return;
    if (routeSessionId !== undefined && session.id !== routeSessionId) return;
    void runShellNow(queued.command);
  }, [isRunningRef, routeSessionId, runShellNow, session]);

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

    if (command === 'worktree' || command === 'wt') {
      openWorktreeForm({
        projectDir: session.directory,
        branch: args.trim() || undefined,
        // Inherit this session's always-allow permissions (#101).
        parentSessionId: session.id,
      });
      return;
    }

    if (command === 'restart-opencode') {
      const flags = args.trim() ? args.trim().toLowerCase().split(/\s+/) : [];
      if (flags.some(flag => flag !== 'all' && flag !== 'now') || new Set(flags).size !== flags.length) {
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

    if (command === 'undo' || command === 'redo') {
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
      return;
    }

    if (!portAvailable) return;

    if (command === 'compact') {
      await handleCompact();
      return;
    }

    if (command === 'fork') {
      if (!caps.fork) return;
      setShowForkPicker(true);
      return;
    }

    if (command === 'move') {
      if (!caps.move) return;
      setShowMovePicker(true);
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
  }, [activeAgent, archiveSession, caps.fork, caps.move, createSession, launchOpencodeInTmux, tmuxAvailable, seedNewSession, handleCompact, handleNewSession, handleTmuxShortcut, handleVSCodeShortcut, navigate, navigateToSession, openWorktreeForm, portAvailable, recentSessionsRef, messagesRef, partsRef, refreshThread, selectedAgent, selectedModel, session, setShowForkPicker, setShowDisconnectedToast, setShowMovePicker, setShowRenameModal, setShowRenameToast, setRestartToastMessage, reloadCapabilities, setCopyToastMessage, pending]);

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
