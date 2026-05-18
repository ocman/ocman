// SessionDetail page — the orchestrator.
//
// Rewritten on top of `useSession` (single reducer, one SSE stream)
// and `usePendingSend` (optimistic user bubble outside the view).
// See spec/sse-rewrite/architecture.md for the design rationale.
//
// The page is large because it owns orchestration: sidebar, header,
// palette commands, shortcuts, tmux, capabilities, prompt handling,
// etc. The SSE/state pipeline itself is now small and centralised in
// `useSession`; the page is mostly props plumbing from there to the
// individual UI surfaces.

import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { flushSync } from 'react-dom';
import { useStickyNavigate } from '../../lib/useStickyNavigate';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api } from '../../lib/api';
import { cleanTitle, shortPath } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { OcmanRuntimeProvider } from '../../components/OcmanRuntimeProvider';
import { AssistantThread } from '../../components/AssistantThread';
import { Composer } from '../../components/assistant/Composer';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { RightPanel } from '../../components/RightPanel';
import { ErrorBoundary, type FallbackRender } from '../../components/ErrorBoundary';
import { RateLimitBanner } from '../../components/RateLimitBanner';
import { useUiStore } from '../../lib/uiStore';
import { useTmux } from '../../lib/useTmux';
import { useApiStore } from '../../lib/apiStore';
import { useGitInfo } from '../../lib/useGitInfo';
import { usePlatformCapabilities } from '../../lib/useCapabilities';
import { listFailedSends, type FailedSend } from '../../lib/failedSends';
import { recheckFaviconNotify } from '../../lib/useFaviconNotify';
import { createSessionWithLaunch, type LaunchStatus } from '../../lib/createSessionWithLaunch';
import {
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  deriveActiveModelAndAgent,
} from '../../lib/sessionStatus';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import {
  extractPendingPermission,
  extractPendingQuestion,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
} from '../../lib/sseHelpers';
import { isSessionRelevant } from '../../lib/promptRouting';
import { useSubagentTracking } from './useSubagentTracking';
import { useTmuxActions } from './useTmuxActions';
import { useSessionStatus } from './useSessionStatus';
import { useSidebarSessions } from './useSidebarSessions';
import { useSessionCapabilities } from './useSessionCapabilities';
import {
  usePromptHandlers,
  storePendingQuestion,
  loadPendingQuestion,
} from './usePromptHandlers';
import { useSessionShortcuts } from './useSessionShortcuts';
import { usePaletteCommands } from './usePaletteCommands';
import { SseStatusIndicator } from './SseStatusIndicator';
import { remoteLog } from '../../lib/remoteLog';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';
import { SessionToasts } from './SessionToasts';
import { SessionSidebar, type SidebarProjectGroup } from './SessionSidebar';
import { RenameModal } from './RenameModal';
import { useSessionActions } from './useSessionActions';
import { useSession } from './useSession';
import { usePendingSend, materializePending } from './usePendingSend';

/** Memory bound on the in-memory message list. */
const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;
const THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS = 5_000;

/**
 * Props for the inner SessionDetail component.
 *
 * `id` is threaded from the wrapper in `./index.tsx` rather than read
 * via `useParams()` here. Bypassing the param subscription forces a
 * re-render whenever the URL changes — see index.tsx for the full
 * rationale.
 */
export interface SessionDetailProps {
  id: string | undefined;
}

export function SessionDetail({ id }: SessionDetailProps) {
  const navigate = useStickyNavigate();
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
  // Route changes must win over in-flight streaming work. flushSync
  // forces React Router's location update to commit immediately so
  // the SSE lifecycle (keyed off the route id) tears down before
  // any further work races the click.
  const navigateToSession = useCallback((nextId: string) => {
    flushSync(() => {
      navigate(`/session/${nextId}`);
    });
  }, [navigate]);

  // The new SSE pipeline. Owns the EventSource, the reducer, the
  // initial fetch + reload + loadMore, the cache mirror, and the
  // memory bound. Returns the rendered view plus a small set of
  // lifecycle signals.
  const view = useSession(id, {
    debug: debugMode,
    maxMessages: MAX_RETAINED_MESSAGES,
    trimTo: TRIMMED_RETAINED_MESSAGES,
  });
  const {
    session,
    messages: rawMessages,
    parts: rawParts,
    pendingPermission,
    pendingQuestion,
    status: sseStatus,
    loading,
    loadingMore,
    loadError,
    totalMessages,
    sseReconnecting,
    sseReconnectAttempt,
    sseNextRetryAt,
    retryNow: sseRetryNow,
    sseDebugEvents,
    recentWorkEventAt,
    changesDirtyTick,
    reload,
    loadMore,
    clearPrompt,
    setPendingPermission,
    setPendingQuestion,
    patchSession,
  } = view;

  // Optimistic user-send slot. Lives outside the SessionView; the
  // bubble is materialised at render time, never written to the
  // reducer's `messages` array.
  const pending = usePendingSend(id);
  // Auto-clear pending when SSE delivers the real user message.
  // Runs in an effect (not render) so the pending → null setState
  // is properly batched and React doesn't see a setState during
  // another component's render phase. observeMessages is a stable
  // useCallback inside usePendingSend, so the effect only re-runs
  // when rawMessages itself changes identity (i.e., a real SSE
  // update).
  const observeMessages = pending.observeMessages;
  useEffect(() => {
    observeMessages(rawMessages);
  }, [rawMessages, observeMessages]);

  // Materialise pending into the messages/parts that flow into the
  // converter. When `pending` is null these are the raw view arrays
  // (reference-equal).
  const { messages, parts } = useMemo(() => {
    if (!session) return { messages: rawMessages, parts: rawParts };
    return materializePending(session.id, pending.pending, rawMessages, rawParts);
  }, [session, rawMessages, rawParts, pending.pending]);

  const sseActive = sseStatus === 'live';

  // Capability flags for the owning platform.
  const caps = usePlatformCapabilities(session?.platform);

  const [whisperAvailable, setWhisperAvailable] = useState(false);

  // Per-session capability state — port availability, agent
  // catalog, model picker, selected model/agent/reasoning.
  const {
    portAvailable,
    setPortAvailable,
    portAvailableRef,
    agentsLoaded,
    agents,
    modelOptions,
    modelEntries,
    selectedModel,
    setSelectedModel,
    selectedAgent,
    setSelectedAgent,
    selectedReasoning,
    setSelectedReasoning,
    refreshModels,
    handleToggleFavorite,
  } = useSessionCapabilities({
    id,
    platform: session?.platform,
    liveConnection: session?.liveConnection ?? false,
    directory: session?.directory,
  });

  // SSE connectivity by itself is not enough to enable the composer:
  // mocked/test EventSource endpoints and read-only event streams can
  // open even when the platform reports `liveConnection=false`. Keep
  // the composer gate tied to the session's write-capable live
  // connection bit; that bit is still mirrored into portAvailable by
  // useSessionCapabilities when the platform reports it.
  useEffect(() => {
    if (sseActive && session?.liveConnection) setPortAvailable(true);
  }, [sseActive, session?.liveConnection, setPortAvailable]);

  // Subagent tracking — TPS / inline stdout / known subagent ids.
  const {
    subagentSessionIdsRef,
    subagentTokens,
    setSubagentTokens,
    taskLiveOutput,
  } = useSubagentTracking(parts, id);
  const { setInfo } = useHeaderInfo();
  usePageTitle(cleanTitle(session?.title) || 'Session');

  // Sidebar state, archive/pin handlers, archived toggle, collapsed groups.
  const collapsedProjects = useUiStore((state) => state.collapsedProjects);
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
  const abortControllerRef = useRef<AbortController | null>(null);
  const {
    recentSessions,
    recentSessionsRef,
    loadingRecentSessions,
    archivingSessionIds,
    showArchivedRecent,
    setShowArchivedRecent,
    showArchivedRecentRef,
    handleArchiveSession,
    handlePinSession,
    collapsedProjectSet,
  } = useSidebarSessions({
    id,
    sessionId: session?.id,
    collapsedProjects,
    abortSignalRef: abortControllerRef,
    navigate,
  });
  const { infos: siblingGitInfos } = useGitInfo(
    recentSessions.map((s) => s.directory).filter(Boolean),
  );

  // Tmux state.
  const tmux = useTmux();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const tmuxActions = useTmuxActions(tmux, session?.directory);
  const {
    matchingTmuxSession,
    pendingTmuxSession,
    pickerPos,
    pickerRef,
    handleTmuxSwitch,
    handleClientSelect,
    handleLaunchOpencode,
    launchingOpencode,
    handleTmuxShortcut,
  } = tmuxActions;

  // Prompt-handler hook (POSTs the replies, manages in-flight flags
  // and per-prompt error messages). Routes the post-success clear
  // through the reducer's clearPrompt action.
  const {
    answeringPermission,
    permissionError,
    setPermissionError,
    answeringQuestion,
    questionError,
    handlePermissionReply,
    handleQuestionReply,
    handleQuestionReject,
  } = usePromptHandlers({
    session,
    portAvailable,
    caps,
    pendingPermission,
    pendingQuestion,
    clearPrompt,
  });

  // Toast / modal state.
  const [showRenameModal, setShowRenameModal] = useState(false);
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [threadBoundaryResetNonce, setThreadBoundaryResetNonce] = useState(0);
  const [createLaunchStatus, setCreateLaunchStatus] = useState<LaunchStatus>('idle');
  const [failedSends, setFailedSends] = useState<FailedSend[]>([]);

  const archiveSession = useApiStore((state) => state.archiveSession);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const seedNewSession = useApiStore((state) => state.seedNewSession);
  const listPermissions = useApiStore((state) => state.listPermissions);
  const listQuestions = useApiStore((state) => state.listQuestions);

  const sidebarWidth = useUiStore((state) => state.sidebarWidth);
  const sidebarView = useUiStore((state) => state.sidebarView);
  const toggleSidebarView = useUiStore((state) => state.toggleSidebarView);
  const toggleCollapsedProject = useUiStore((state) => state.toggleCollapsedProject);
  const threadBoundaryRecoveryRef = useRef<{ sessionId: string | undefined; message: string; at: number } | null>(null);

  useEffect(() => {
    showArchivedRecentRef.current = showArchivedRecent;
  }, [showArchivedRecent, showArchivedRecentRef]);

  // Per-session-change side effects: rehydrate failed sends,
  // refresh whisper, refresh models, reset model/agent selection.
  // The view reducer + cache seed are handled inside useSession.
  // The setState calls below are intentional resets keyed on
  // `id`; they're the canonical "reset state on key change" pattern
  // and can't be expressed as derived state.
  /* eslint-disable react-hooks/set-state-in-effect */
  useEffect(() => {
    abortControllerRef.current?.abort();
    const controller = new AbortController();
    abortControllerRef.current = controller;
    const signal = controller.signal;

    setSelectedModel('');
    setSelectedAgent('');
    setSelectedReasoning('');
    setFailedSends(id ? listFailedSends(id) : []);

    getWhisperStatus().then((s) => setWhisperAvailable(s.available)).catch(() => setWhisperAvailable(false));
    if (id) refreshModels(signal);

    return () => controller.abort();
  }, [id, getWhisperStatus, refreshModels, setSelectedAgent, setSelectedModel, setSelectedReasoning]);
  /* eslint-enable react-hooks/set-state-in-effect */

  // Auto-rehydrate the pending bubble for any failed send that
  // survived a page refresh. The user's text+images are persisted
  // in localStorage; we replay them into the pending slot so the
  // bubble re-appears with its retry banner.
  //
  // Skip entries whose text already appears as a real user message
  // (the prompt actually reached the server and SSE delivered it).
  const rehydratedRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    rehydratedRef.current = new Set();
  }, [id]);
  useEffect(() => {
    if (!session || failedSends.length === 0) return;
    if (pending.pending) return; // a fresh send is already in flight
    const realUserTexts = new Set(
      messages
        .filter((m) => m.data?.role === 'user')
        .flatMap((m) =>
          parts
            .filter((p) => p.messageId === m.id)
            .map((p) => {
              try {
                const pd = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
                return pd?.type === 'text' ? (pd.text || '') : '';
              } catch {
                return '';
              }
            })
            .filter(Boolean),
        ),
    );
    const ghost = failedSends.find((e) =>
      !rehydratedRef.current.has(e.id) && !realUserTexts.has(e.text),
    );
    if (!ghost) return;
    rehydratedRef.current.add(ghost.id);
    pending.begin(ghost.text, ghost.images, {
      model: ghost.model,
      agent: ghost.agent,
      reasoning: ghost.reasoning,
    });
    pending.fail(ghost.error);
  }, [session, failedSends, messages, parts, pending]);

  // Mirror pending prompt flags into the sidebar row so the badge
  // lights up/clears immediately from SSE.
  useEffect(() => {
    if (!id) return;
    patchRecentSession(id, {
      pendingPermission: pendingPermission !== null,
      pendingQuestion: pendingQuestion !== null,
    });
  }, [id, pendingPermission, pendingQuestion, patchRecentSession]);

  // Reverse sync: when the sidebar poll discovers a prompt we don't
  // know about (missed SSE event), fetch the data so the dialog
  // appears.
  const sidebarCurrentSession = recentSessions.find((s) => s.id === id);
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;
  useEffect(() => {
    if (!id) return;
    if (sidebarHasPerm && pendingPermission === null) {
      listPermissions(id)
        .then((perms) => {
          for (const raw of perms) {
            const p = raw as Record<string, unknown>;
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            const promptSid = typeof p.sessionID === 'string' ? p.sessionID : '';
            if (!isSessionRelevant(promptSid, id, subagentSessionIdsRef.current)) continue;
            setPendingPermission(perm);
            setPermissionError(null);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
    if (sidebarHasQuestion && pendingQuestion === null) {
      listQuestions(id)
        .then((questions) => {
          for (const raw of questions) {
            const q = raw as Record<string, unknown>;
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            const questionSid = typeof q.sessionID === 'string' ? q.sessionID : '';
            if (!isSessionRelevant(questionSid, id, subagentSessionIdsRef.current)) continue;
            storePendingQuestion(id, question);
            setPendingQuestion(question);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
  }, [id, sidebarHasPerm, sidebarHasQuestion, pendingPermission, pendingQuestion,
    listPermissions, listQuestions, setPermissionError, setPendingPermission,
    setPendingQuestion, subagentSessionIdsRef]);

  // Mark session as seen on entry.
  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;
  useEffect(() => {
    if (!sessionSeenId || !sessionSeenPlatform) return;
    void markSessionSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated)
      .then(() => {
        patchSession({ seen: true });
        patchRecentSession(sessionSeenId, { seen: true });
        recheckFaviconNotify();
      })
      .catch((err) => console.error('Failed to mark session seen', err));
  }, [markSessionSeen, sessionSeenId, sessionSeenPlatform, sessionSeenUpdated, patchRecentSession, patchSession]);

  // Restore pending question from sessionStorage when navigating
  // back to a page whose parts still show a pending question tool.
  useEffect(() => {
    if (pendingQuestion || !session?.id || !portAvailable) return;
    const pendingFromParts = extractPendingQuestionFromParts(parts, session.id);
    if (pendingFromParts) {
      storePendingQuestion(session.id, pendingFromParts);
      setPendingQuestion(pendingFromParts);
      return;
    }
    if (!hasPendingQuestionInParts(parts, session.id)) return;
    const stored = loadPendingQuestion(session.id);
    if (stored) setPendingQuestion(stored);
  }, [parts, session?.id, portAvailable, pendingQuestion, setPendingQuestion]);

  // Aggregate token/cost stats.
  const liveTokens = useMemo(() => computeLiveTokens(messages), [messages]);
  const tokenStats = useMemo(
    () => mergeTokenStats(session, liveTokens),
    [session, liveTokens],
  );

  // Header info.
  useEffect(() => {
    if (!session) return;
    const s = session;
    setInfo({
      sessionTitle: cleanTitle(s.title) || 'Untitled',
      sessionPlatform: s.platform,
      sessionProject: shortPath(s.directory),
      sessionProjectFull: s.directory,
    });
    return () => setInfo({});
  }, [session, setInfo]);

  const { activeModel, activeAgent } = useMemo(
    () => deriveActiveModelAndAgent(messages, session),
    [messages, session],
  );

  // eslint-disable-next-line react-hooks/preserve-manual-memoization -- React Compiler can't prove the deps cover the closure; the manual list matches the legacy hook exactly.
  const handleNewSessionInDirectory = useCallback(async (directory: string, title?: string) => {
    try {
      const res = await createSessionWithLaunch(
        {
          createSession,
          launchOpencodeInTmux,
          tmuxAvailable: tmux.available,
          onStatusChange: setCreateLaunchStatus,
        },
        { directory, title },
      );
      if (res.id) {
        seedNewSession(res.id, directory, session?.platform ?? '', title);
        navigateToSession(res.id);
      }
    } catch (e) {
      console.error('Failed to create session', e);
      setShowCreateSessionErrorToast(true);
    }
  }, [createSession, launchOpencodeInTmux, tmux.available, navigateToSession, seedNewSession, session?.platform]);

  const handleNewSession = useCallback(async (title?: string) => {
    if (!session) return;
    await handleNewSessionInDirectory(session.directory, title);
  }, [session, handleNewSessionInDirectory]);

  const handleCompact = useCallback(async () => {
    if (!session || !portAvailable || !caps.compact) return;
    const model = selectedModel || activeModel || '';
    const slashIdx = model.indexOf('/');
    const providerID = slashIdx > 0 ? model.slice(0, slashIdx) : '';
    const modelID = slashIdx > 0 ? model.slice(slashIdx + 1) : model;
    const agentBeforeCompact = selectedAgent || activeAgent || '';
    try {
      await api.compactSession(session.id, providerID, modelID);
      if (agentBeforeCompact) setSelectedAgent(agentBeforeCompact);
    } catch (e) {
      console.error('Failed to compact session', e);
    }
  }, [activeAgent, activeModel, caps.compact, portAvailable, selectedAgent, selectedModel, session, setSelectedAgent]);

  const {
    awaitingAssistantResponse,
    setAwaitingAssistantResponse,
    handleSend,
    handleRetrySend,
    handleDismissFailedSend,
    handleShell,
    handleAbort,
    handleVSCodeShortcut,
    handleCommand,
  } = useSessionActions({
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
    tmuxAvailable: tmux.available,
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
  });

  void setSubagentTokens; // exposed for the legacy hook; not needed
                          // by the new pipeline since SSE handles
                          // subagent token tracking inside useSession.

  const handleThreadBoundaryRetry = useCallback((error: Error, force = false) => {
    const now = Date.now();
    const previous = threadBoundaryRecoveryRef.current;
    if (
      !force
      && previous
      && previous.sessionId === id
      && previous.message === error.message
      && now - previous.at < THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS
    ) {
      return false;
    }

    threadBoundaryRecoveryRef.current = { sessionId: id, message: error.message, at: now };
    remoteLog.warn('SessionDetail auto-recovering thread boundary', {
      sessionId: id,
      message: error.message,
    });
    setThreadBoundaryResetNonce((value) => value + 1);
    void reload();
    return true;
  }, [id, reload]);

  const renderThreadBoundaryFallback = useCallback<FallbackRender>(({ error, reset }) => {
    const previous = threadBoundaryRecoveryRef.current;
    const autoRecover = isRecoverableThreadBoundaryError(error)
      && (!previous
        || previous.sessionId !== id
        || previous.message !== error.message
        || Date.now() - previous.at >= THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS);

    return (
      <ThreadBoundaryFallback
        error={error}
        reset={reset}
        autoRecover={autoRecover}
        onReload={() => {
          handleThreadBoundaryRetry(error, !autoRecover);
        }}
      />
    );
  }, [handleThreadBoundaryRetry, id]);

  const handleModelChange = useCallback((model: string) => {
    setSelectedModel(model);
    setSelectedReasoning('');
  }, [setSelectedModel, setSelectedReasoning]);

  // Alt+J / Alt+K: navigate between recent sessions.
  const jumpToSession = useCallback((direction: 1 | -1) => {
    const sessions = recentSessionsRef.current;
    const currentIndex = sessions.findIndex((s) => s.id === id);
    if (currentIndex === -1) return;
    const target = sessions[currentIndex + direction];
    if (target) navigateToSession(target.id);
  }, [id, navigateToSession, recentSessionsRef]);

  // Refs for the palette dispatcher / shortcut handlers.
  const sessionRef = useRef(session);
  useEffect(() => { sessionRef.current = session; }, [session]);
  const selectedModelRef = useRef(selectedModel);
  useEffect(() => { selectedModelRef.current = selectedModel; }, [selectedModel]);
  const activeModelRef = useRef(activeModel);
  useEffect(() => { activeModelRef.current = activeModel; }, [activeModel]);
  const capsRef = useRef(caps);
  useEffect(() => { capsRef.current = caps; }, [caps]);
  const archiveSessionRef = useRef(archiveSession);
  useEffect(() => { archiveSessionRef.current = archiveSession; }, [archiveSession]);
  const navigateRef = useRef(navigate);
  useEffect(() => { navigateRef.current = navigate; }, [navigate]);

  usePaletteCommands({
    sessionRef,
    archiveSessionRef: archiveSessionRef as React.MutableRefObject<(platform: string, id: string, timeUpdated: number, archive: boolean) => Promise<unknown>>,
    navigateRef: navigateRef as React.MutableRefObject<(to: string | number) => void>,
    portAvailableRef,
    capsRef,
    selectedModelRef,
    activeModelRef,
    tmux,
    setSelectedReasoning,
    setShowRenameModal,
  });

  useSessionShortcuts({
    session,
    portAvailable,
    matchingTmuxSession,
    jumpToSession,
    handleTmuxShortcut,
    handleVSCodeShortcut,
    handleNewSession,
  });

  const hasMore = messages.length < totalMessages;
  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  const composerModels = useMemo(
    () => Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model))),
    [activeModel, session?.defaultModel, modelOptions],
  );
  const showSseNotice = portAvailable && !sseActive;
  const showSseDebug = debugMode && sseDebugEvents.length > 0;

  useEffect(() => {
    setAwaitingAssistantResponse(false);
  }, [id, setAwaitingAssistantResponse]);

  // Clear "awaiting first assistant response" once the turn visibly
  // advances or terminates.
  useEffect(() => {
    if (lastMsg?.data?.role === 'assistant') {
      setAwaitingAssistantResponse(false);
      return;
    }
    if (session?.status === 'done' || session?.status === 'error') {
      setAwaitingAssistantResponse(false);
    }
  }, [lastMsg, session?.status, setAwaitingAssistantResponse]);

  const hasPendingPrompt = pendingPermission !== null || pendingQuestion !== null;
  const isRunning = isSessionRunning(lastMsg, session?.status, awaitingAssistantResponse);

  const { optimisticStatus, liveTokensPerSecond } = useSessionStatus({
    lastMsg,
    messages,
    subagentTokens,
    setSubagentTokens,
    sessionStatus: session?.status,
    awaitingAssistantResponse,
    recentWorkEventAt,
    isRunning,
    pendingPermission,
    pendingQuestion,
  });
  useEffect(() => {
    if (!id) return;
    patchRecentSession(id, { status: optimisticStatus });
  }, [id, optimisticStatus, patchRecentSession]);

  // Stable handler + flag for the composer's "launch opencode" hint.
  const handleLaunchHintClick = useCallback(() => setShowDisconnectedToast(true), []);
  const launchHintActive = !portAvailable && !hasPendingPrompt && tmux.available && !!caps.liveConnectionHint;

  // Sidebar project groupings.
  const sidebarProjectGroups = useMemo<SidebarProjectGroup[]>(() => {
    const buckets = new Map<string, typeof recentSessions>();
    for (const s of recentSessions) {
      const key = projectRootForDirectory(s.directory || '');
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    const effectiveStatus = (s: typeof recentSessions[0]): typeof s.status =>
      s.id === id ? optimisticStatus : s.status;
    const rollup = (sessions: typeof recentSessions) => rollupGroupStatus(sessions, effectiveStatus);

    const groups: SidebarProjectGroup[] = Array.from(buckets.entries()).map(([directory, sessions]) => {
      const sorted = [...sessions].sort((a, b) => b.timeUpdated - a.timeUpdated);
      return {
        directory,
        sessions: sorted,
        lastUpdated: sorted[0]?.timeUpdated ?? 0,
        aggregate: rollup(sorted),
      };
    });
    groups.sort((a, b) => b.lastUpdated - a.lastUpdated);

    const pinnedSessions = recentSessions
      .filter((s) => s.pinned)
      .sort((a, b) => b.pinnedAt - a.pinnedAt);
    if (pinnedSessions.length > 0) {
      groups.unshift({
        directory: '__pinned__',
        sessions: pinnedSessions,
        lastUpdated: pinnedSessions[0]?.timeUpdated ?? 0,
        aggregate: rollup(pinnedSessions),
        isPinned: true,
      });
    }

    return groups;
  }, [recentSessions, id, optimisticStatus]);

  return (
    <Toast.Provider swipeDirection="right">
      <div className="session-layout" data-testid="session-layout">
        <SessionSidebar
          activeId={id}
          sidebarWidth={sidebarWidth}
          sidebarView={sidebarView}
          toggleSidebarView={toggleSidebarView}
          showArchivedRecent={showArchivedRecent}
          setShowArchivedRecent={setShowArchivedRecent}
          loadingRecentSessions={loadingRecentSessions}
          recentSessions={recentSessions}
          sidebarProjectGroups={sidebarProjectGroups}
          archivingSessionIds={archivingSessionIds}
          collapsedProjectSet={collapsedProjectSet}
          toggleCollapsedProject={toggleCollapsedProject}
          siblingGitInfos={siblingGitInfos}
          optimisticStatus={optimisticStatus}
          debugMode={debugMode}
          pendingTmuxSession={pendingTmuxSession}
          pickerPos={pickerPos}
          pickerRef={pickerRef}
          tmux={tmux}
          onNavigateToSession={navigateToSession}
          onArchiveSession={handleArchiveSession}
          onPinSession={handlePinSession}
          onClientSelect={handleClientSelect}
          onNewSessionInDirectory={handleNewSessionInDirectory}
        />
        <div className="session-main">
          {session && (
            <div className="session-detail-actions">
              {tmux.available && matchingTmuxSession && (
                <button
                  className="session-sidebar-new"
                  onClick={(e) => handleTmuxSwitch(e, matchingTmuxSession.name)}
                  title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
                  style={{ fontSize: 11, fontFamily: "'SF Mono', Consolas, monospace" }}
                >tmux</button>
              )}
              {tmux.available && !portAvailable && caps.liveConnectionHint && (
                <button
                  type="button"
                  className="session-sidebar-new"
                  onClick={() => { void handleLaunchOpencode(); }}
                  disabled={launchingOpencode}
                  title="Launch opencode --port 0 in a new tmux window"
                  style={{ fontSize: 11, fontFamily: "'SF Mono', Consolas, monospace" }}
                >{launchingOpencode ? '…' : 'launch'}</button>
              )}
              <button
                type="button"
                className="session-sidebar-new"
                onClick={handleVSCodeShortcut}
                title="Open in VS Code (V)"
                aria-label="Open in VS Code"
                style={{ textDecoration: 'none', fontSize: 11 }}
              >&lt;/&gt;</button>
              <button
                className="session-sidebar-new"
                onClick={() => { void handleNewSession(); }}
                title="New session"
                aria-label="New session"
              >+</button>
            </div>
          )}
          {loading ? (
            <div className="oc-loading" data-testid="loading-spinner">
              <div className="oc-spinner" />
              Loading conversation...
            </div>
          ) : loadError ? (
            <div className="oc-error-banner" data-testid="error-banner" style={{ margin: 24 }}>
              {loadError}
              <button onClick={() => { void reload(); }}>Retry</button>
            </div>
          ) : session && (
            <OcmanRuntimeProvider
              key={session.id}
              messages={messages}
              parts={parts}
              sessionId={session.id}
              canSend={portAvailable && caps.composer}
              pendingAgent={selectedAgent || activeAgent || undefined}
              agents={agents}
              taskLiveOutput={taskLiveOutput}
              projectDirectory={session.directory}
              failedSends={failedSends}
              onRetryFailedSend={handleRetrySend}
              onDismissFailedSend={handleDismissFailedSend}
            >
              <ErrorBoundary
                name="session:thread"
                resetKey={`${session.id}:${threadBoundaryResetNonce}`}
                fallbackRender={renderThreadBoundaryFallback}
              >
                <AssistantThread
                  hasMore={hasMore}
                  loadingMore={loadingMore}
                  onLoadMore={loadMore}
                  composer={(
                    <ErrorBoundary name="session:composer" inline resetKey={session.id}>
                      {session.notice?.kind === 'rate_limit' && (
                        <RateLimitBanner notice={session.notice} />
                      )}
                      {pendingPermission && portAvailable && caps.respondPermission ? (
                        <PermissionPrompt
                          permission={pendingPermission}
                          onReply={handlePermissionReply}
                          disabled={answeringPermission}
                          error={permissionError}
                        />
                      ) : pendingQuestion && portAvailable && caps.respondQuestion ? (
                        <QuestionPrompt
                          question={pendingQuestion}
                          onReply={handleQuestionReply}
                          onReject={handleQuestionReject}
                          disabled={answeringQuestion}
                          error={questionError}
                        />
                      ) : caps.composer ? (
                        <Composer
                          onSend={handleSend}
                          onCommand={handleCommand}
                          onShell={handleShell}
                          shellExec={caps.shellExec}
                          onAbort={handleAbort}
                          isRunning={isRunning}
                          disabled={!portAvailable || hasPendingPrompt}
                          disabledHint={hasPendingPrompt
                            ? 'Respond to the pending prompt above before sending a new message.'
                            : caps.liveConnectionHint}
                          whisperAvailable={whisperAvailable}
                          models={composerModels}
                          modelEntries={modelEntries}
                          activeModel={activeModel}
                          selectedModel={selectedModel}
                          onModelChange={handleModelChange}
                          onToggleFavorite={handleToggleFavorite}
                          onRefreshModels={refreshModels}
                          activeAgent={activeAgent}
                          selectedAgent={selectedAgent}
                          onAgentChange={setSelectedAgent}
                          agents={agents}
                          agentsLoaded={agentsLoaded}
                          contextTokens={session?.contextTokenCount || undefined}
                          sessionId={session?.id}
                          tokensPerSecond={liveTokensPerSecond ?? undefined}
                          tokenStats={tokenStats}
                          selectedReasoning={selectedReasoning}
                          onReasoningChange={setSelectedReasoning}
                          onLaunchRequest={launchHintActive ? handleLaunchHintClick : undefined}
                        />
                      ) : null}
                    </ErrorBoundary>
                  )}
                  footer={showSseNotice || showSseDebug ? (
                    <>
                      {showSseNotice && (
                        <SseStatusIndicator
                          active={sseActive}
                          reconnecting={sseReconnecting}
                          attempt={sseReconnectAttempt}
                          nextRetryAt={sseNextRetryAt}
                          onRetryNow={sseRetryNow}
                        />
                      )}
                      {showSseDebug && (
                        <details className="oc-sse-debug">
                          <summary>SSE debug ({sseDebugEvents.length})</summary>
                          <div className="oc-sse-debug-list">
                            {[...sseDebugEvents].reverse().map((evt, idx) => (
                              <div key={evt.at + ':' + idx} className="oc-sse-debug-item">
                                <span className="oc-sse-debug-meta">{new Date(evt.at).toLocaleTimeString()} [{evt.event}]</span>
                                <pre className="oc-sse-debug-data">{evt.data}</pre>
                              </div>
                            ))}
                          </div>
                        </details>
                      )}
                    </>
                  ) : undefined}
                />
              </ErrorBoundary>
              {showRenameModal && session && (
                <RenameModal
                  sessionId={session.id}
                  initialTitle={session.title || ''}
                  onClose={() => setShowRenameModal(false)}
                  onRenamed={(newTitle) => {
                    patchSession({ title: newTitle });
                    setShowRenameToast(true);
                  }}
                />
              )}
            </OcmanRuntimeProvider>
          )}
        </div>
        {id && (
          <RightPanel
            sessionId={id}
            platformId={session?.platform}
            directory={session?.directory}
            dirtyTick={changesDirtyTick}
            session={session ?? undefined}
          />
        )}
        <SessionToasts
          showRenameToast={showRenameToast}
          setShowRenameToast={setShowRenameToast}
          createLaunchStatus={createLaunchStatus}
          showCreateSessionErrorToast={showCreateSessionErrorToast}
          setShowCreateSessionErrorToast={setShowCreateSessionErrorToast}
          showDisconnectedToast={showDisconnectedToast}
          setShowDisconnectedToast={setShowDisconnectedToast}
          tmuxAvailable={tmux.available}
          liveConnectionHint={!!caps.liveConnectionHint}
          hasDirectory={!!session?.directory}
          launchingOpencode={launchingOpencode}
          onLaunch={handleLaunchOpencode}
        />
      </div>
    </Toast.Provider>
  );
}
