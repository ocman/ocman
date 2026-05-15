import { useCallback, useState } from 'react';
import type { Dispatch, SetStateAction, MutableRefObject } from 'react';
import { flushSync } from 'react-dom';
import { api, type Message, type Part, type PlatformCapabilities } from '../../lib/api';
import type { AttachedImage } from '../../components/assistant/Composer';
import type { SubagentTokenMap } from './useSubagentTracking';
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
  setMessages: Dispatch<SetStateAction<Message[]>>;
  setParts: Dispatch<SetStateAction<Part[]>>;
  setSubagentTokens: Dispatch<SetStateAction<SubagentTokenMap>>;
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
  handleRetrySend: (tempId: string) => void;
  handleDismissFailedSend: (tempId: string) => void;
  handleShell: (command: string) => Promise<void>;
  handleAbort: () => Promise<void>;
  handleVSCodeShortcut: () => void;
  handleCommand: (command: string, args: string) => Promise<void>;
}

/**
 * Encapsulates the session-level send/command/shell/abort actions plus the
 * failed-send list. Extracted from SessionDetail so the component can focus
 * on layout and orchestration.
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
  setMessages,
  setParts,
  setSubagentTokens,
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

  // Internal send that accepts an explicit tempId. Used by both the public
  // handleSend (fresh prompt) and handleRetrySend (replay of a previously
  // failed send) so the optimistic bubble id stays stable across retries.
  const performSend = useCallback(async (
    tempId: string,
    text: string,
    images: AttachedImage[] | undefined,
    model: string | undefined,
    agent: string | undefined,
    reasoning: string | undefined,
  ) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;

    // Clear subagent token tracking for the new run window.
    setSubagentTokens(new Map());
    setAwaitingAssistantResponse(true);

    try {
      await sendMessage(session.id, text, images, model, agent, reasoning);
      // Success — drop any prior failed entry for this id (only relevant on
      // retry; recording is otherwise idempotent). SSE will deliver the
      // real message + assistant response incrementally.
      setFailedSends(prev => prev.filter(e => e.id !== tempId));
      removeFailedSend(session.id, tempId);
    } catch (e) {
      setAwaitingAssistantResponse(false);
      console.error('Failed to send message', e);
      const msg = e instanceof Error ? e.message : '';
      // When the error is a missing OpenCode instance, surface a toast with
      // a launch action instead of polluting the conversation thread. Roll
      // back the optimistic bubble so retry doesn't apply here either —
      // the user wants to launch OpenCode, not resubmit blindly.
      if (msg.includes('no running OpenCode instance')) {
        setMessages(prev => prev.filter(m => m.id !== tempId));
        setParts(prev => prev.filter(p => p.messageId !== tempId));
        setFailedSends(prev => prev.filter(e => e.id !== tempId));
        removeFailedSend(session.id, tempId);
        setShowDisconnectedToast(true);
        return;
      }
      // Mark the optimistic bubble as failed and persist enough context to
      // replay the send on Retry — even across a page refresh.
      const failed: FailedSend = {
        id: tempId,
        text,
        images,
        model,
        agent,
        reasoning,
        error: msg || 'Unknown error',
        failedAt: Date.now(),
      };
      setFailedSends(prev => {
        const idx = prev.findIndex(e => e.id === tempId);
        if (idx >= 0) return prev.map((e, i) => (i === idx ? failed : e));
        return [...prev, failed];
      });
      recordFailedSend(session.id, failed);
    }
  }, [pendingPermission, pendingQuestion, portAvailable, sendMessage, session, setFailedSends, setMessages, setParts, setSubagentTokens, setShowDisconnectedToast]);

  const handleSend = useCallback(async (text: string, images?: AttachedImage[]) => {
    if (!session || !portAvailable) return;
    // Belt-and-suspenders: the composer is normally unmounted while a
    // permission/question prompt is active (see the ternary in the render
    // tree), but an Enter keystroke can still land on the old composer
    // during the re-render / focus-transfer race after an SSE event.
    // Refuse to submit anything while a prompt is awaiting response so
    // the user's reply doesn't accidentally ship as a new user message.
    if (pendingPermission || pendingQuestion) return;

    // Optimistically add user message immediately
    const tempId = 'temp-' + Date.now();
    const optimisticMsg: Message = {
      id: tempId,
      sessionId: session.id,
      timeCreated: Date.now(),
      data: { role: 'user' },
    };
    const optimisticParts: Part[] = [];
    if (text) {
      optimisticParts.push({
        id: 'part-' + tempId,
        messageId: tempId,
        sessionId: session.id,
        data: { type: 'text', text } as unknown as string,
      });
    }
    if (images) {
      images.forEach((img, i) => {
        optimisticParts.push({
          id: `part-${tempId}-img-${i}`,
          messageId: tempId,
          sessionId: session.id,
          data: { type: 'file', mime: img.mime, url: img.url } as unknown as string,
        });
      });
    }
    setMessages(prev => [...prev, optimisticMsg]);
    setParts(prev => [...prev, ...optimisticParts]);

    await performSend(
      tempId,
      text,
      images,
      selectedModel || activeModel || undefined,
      selectedAgent || activeAgent || undefined,
      selectedReasoning || undefined,
    );
  }, [activeAgent, activeModel, pendingPermission, pendingQuestion, performSend, portAvailable, selectedAgent, selectedModel, selectedReasoning, session, setMessages, setParts]);

  // Replay a previously failed send. Reuses the same optimistic message id
  // so the bubble stays in place — the failed banner just disappears on
  // success or updates with a new error message on a second failure.
  // Falls back to the entry's persisted text/images so refresh-rehydrated
  // ghost messages remain retryable even though their parts were never in
  // the original `messages` array.
  const handleRetrySend = useCallback((tempId: string) => {
    if (!session) return;
    const entry = failedSends.find(e => e.id === tempId);
    if (!entry) return;
    void performSend(
      tempId,
      entry.text,
      entry.images,
      entry.model,
      entry.agent,
      entry.reasoning,
    );
  }, [failedSends, performSend, session]);

  // Drop a failed send (without retrying). Removes both the persisted
  // entry and the ghost optimistic message it was attached to.
  const handleDismissFailedSend = useCallback((tempId: string) => {
    if (!session) return;
    setFailedSends(prev => prev.filter(e => e.id !== tempId));
    removeFailedSend(session.id, tempId);
    setMessages(prev => prev.filter(m => m.id !== tempId));
    setParts(prev => prev.filter(p => p.messageId !== tempId));
  }, [session, setFailedSends, setMessages, setParts]);

  // handleShell sends a `!`-prefixed composer submission to the
  // platform's raw shell endpoint (OpenCode: POST /session/{id}/shell),
  // bypassing the LLM. The composer has already stripped the leading
  // `!` and trimmed whitespace; we forward verbatim.
  //
  // Agent attribution mirrors handleSend: prefer the user's currently
  // selected composer agent, fall back to the session's active agent,
  // and finally to "build" (OpenCode's universal default — its /shell
  // endpoint requires a non-empty agent). The backend re-applies the
  // same default when we send blank, but resolving here keeps the
  // synthesised assistant message attributed to the agent the user
  // sees highlighted in the composer.
  // Optimistic rendering is skipped: SSE will deliver the synthesised
  // assistant message + bash tool output as soon as OpenCode flushes it.
  const handleShell = useCallback(async (command: string) => {
    if (!session || !portAvailable) return;
    if (pendingPermission || pendingQuestion) return;
    const agent = selectedAgent || activeAgent || 'build';
    try {
      await api.runShell(session.id, command, agent);
    } catch (e) {
      console.error('Failed to run shell command', e);
      const errId = 'error-' + Date.now();
      const errMsg: Message = {
        id: errId,
        sessionId: session.id,
        timeCreated: Date.now(),
        data: { role: 'assistant', finish: 'error' },
      };
      const errPart: Part = {
        id: 'part-' + errId,
        messageId: errId,
        sessionId: session.id,
        data: {
          type: 'text',
          text: `**Failed to run shell command:** ${e instanceof Error ? e.message : 'Unknown error'}`,
        } as unknown as string,
      };
      setMessages(prev => [...prev, errMsg]);
      setParts(prev => [...prev, errPart]);
    }
  }, [activeAgent, pendingPermission, pendingQuestion, portAvailable, selectedAgent, session, setMessages, setParts]);

  const handleAbort = useCallback(async () => {
    if (!session || !portAvailable || !caps.abort) return;
    try {
      await abortSession(session.id);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to abort session', e);
    }
  }, [abortSession, caps.abort, portAvailable, session]);

  const handleVSCodeShortcut = useCallback(() => {
    if (!session) return;
    openVSCode(session.directory);
  }, [session]);

  const handleCommand = useCallback(async (command: string, args: string) => {
    if (!session) return;

    // /archive is a local ocman action — it works even when the agent isn't running.
    if (command === 'archive') {
      // Pick the session at idx+1 (directly below) or idx-1 (directly above)
      // from the displayed sidebar list, captured before the API call.
      const recentSessions = recentSessionsRef.current;
      const idx = recentSessions.findIndex(s => s.id === session.id);
      const nextSession = recentSessions[idx + 1] ?? recentSessions[idx - 1];
      try {
        await archiveSession(session.platform, session.id, session.timeUpdated, true);
      } catch (e) {
        console.error('Failed to archive session', e);
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

    // /wt is a local ocman action too: it opens the worktree-creation
    // modal prefilled from the current session's directory, optionally
    // seeding the branch from the command args (`/wt feature/login`).
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
      // Like /new, but archives the current session after the new one is
      // created. Create first so a failed archive still leaves the user on
      // a usable new session.
      let newId: string | undefined;
      const clearTitle = args.trim() || undefined;
      try {
        const res = await createSessionWithLaunch(
          { createSession, launchOpencodeInTmux, tmuxAvailable, onStatusChange: () => {} },
          { directory: session.directory, title: clearTitle },
        );
        newId = res.id;
      } catch (e) {
        console.error('Failed to create session', e);
        return;
      }
      try {
        await archiveSession(session.platform, session.id, session.timeUpdated, true);
      } catch (e) {
        console.error('Failed to archive session', e);
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
          console.error('Failed to rename session', e);
        }
      } else {
        setShowRenameModal(true);
      }
      return;
    }

    // Optimistic user message showing the command
    const tempId = 'temp-' + Date.now();
    const optimisticMsg: Message = {
      id: tempId,
      sessionId: session.id,
      timeCreated: Date.now(),
      data: { role: 'user' },
    };
    const displayText = args ? `/${command} ${args}` : `/${command}`;
    const optimisticParts: Part[] = [{
      id: 'part-' + tempId,
      messageId: tempId,
      sessionId: session.id,
      data: { type: 'text', text: displayText } as unknown as string,
    }];
    setMessages(prev => [...prev, optimisticMsg]);
    setParts(prev => [...prev, ...optimisticParts]);

    try {
      await api.executeCommand(
        session.id,
        command,
        args,
        selectedModel || activeModel || undefined,
        selectedAgent || activeAgent || undefined,
      );
      // SSE events will deliver the command response incrementally.
    } catch (e) {
      console.error('Failed to execute command', e);
      const errId = 'error-' + Date.now();
      const errMsg: Message = {
        id: errId,
        sessionId: session.id,
        timeCreated: Date.now(),
        data: { role: 'assistant', finish: 'error' },
      };
      const errPart: Part = {
        id: 'part-' + errId,
        messageId: errId,
        sessionId: session.id,
        data: {
          type: 'text',
          text: `**Failed to execute command:** ${e instanceof Error ? e.message : 'Unknown error'}`,
        } as unknown as string,
      };
      setMessages(prev => [...prev, errMsg]);
      setParts(prev => [...prev, errPart]);
    }
  }, [activeAgent, activeModel, archiveSession, createSession, launchOpencodeInTmux, tmuxAvailable, seedNewSession, handleCompact, handleNewSession, handleTmuxShortcut, handleVSCodeShortcut, navigate, navigateToSession, openWorktreeForm, portAvailable, recentSessionsRef, selectedAgent, selectedModel, session, setMessages, setParts, setShowRenameModal, setShowRenameToast]);

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
