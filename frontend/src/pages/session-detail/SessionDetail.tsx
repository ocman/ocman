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
import { Link, useSearchParams } from 'react-router-dom';
import { flushSync } from 'react-dom';
import { useStickyNavigate } from '../../lib/useStickyNavigate';
import { useSyncRef } from '../../lib/useSyncRef';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api } from '../../lib/api';
import { cleanTitle } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { canLaunchSession } from './launchGate';
import type { MessageBookmark } from '../../lib/messageBookmarks';
import { useMessageBookmarks } from './useMessageBookmarks';
import { OcmanRuntimeProvider } from '../../components/OcmanRuntimeProvider';
import { AssistantThread } from '../../components/AssistantThread';
import { ShareLinkModal } from '../../components/ShareExportMenu';
import { Composer, type ComposerHandle } from '../../components/assistant/Composer';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { RightPanel } from '../../components/RightPanel';
import { SessionTerminalDock } from '../../components/SessionTerminalDock';
import { ErrorBoundary, type FallbackRender } from '../../components/ErrorBoundary';
import { RateLimitBanner } from '../../components/RateLimitBanner';
import { PermissionModeLock } from '../../components/PermissionModeLock';
import { SessionWarningBanner } from '../../components/SessionWarningBanner';
import { McpAuthBanner } from '../../components/McpAuthBanner';
import { FactoryPlanApproval } from '../../components/FactoryPlanApproval';
import { useUiStore } from '../../lib/uiStore';
import { useTmux } from '../../lib/useTmux';
import { useApiStore } from '../../lib/apiStore';
import { useGitInfo } from '../../lib/useGitInfo';
import { usePlatformCapabilities, useOpencodeLaunch } from '../../lib/useCapabilities';
import { createSessionWithLaunch } from '../../lib/createSessionWithLaunch';
import {
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  aggregateSessionTreeStats,
} from '../../lib/sessionStatus';
import { useSubagentTracking } from './useSubagentTracking';
import { useTmuxActions } from './useTmuxActions';
import { useSessionStatus } from './useSessionStatus';
import { useSidebarSessions } from './useSidebarSessions';
import { useSidebarProjectGroups } from './useSidebarProjectGroups';
import { useSessionCapabilities } from './useSessionCapabilities';
import { useComposerModel } from './useComposerModel';
import { usePromptHandlers } from './usePromptHandlers';
import { usePromptSync } from './usePromptSync';
import { useSessionSeen } from './useSessionSeen';
import { useSessionShortcuts } from './useSessionShortcuts';
import { usePaletteCommands } from './usePaletteCommands';
import { SseStatusIndicator } from './SseStatusIndicator';
import { remoteLog } from '../../lib/remoteLog';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';
import { useUnreadMarker } from './useUnreadMarker';
import { sessionWarningKey, useSessionWarnings } from './useSessionWarnings';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';
import { SessionToasts } from './SessionToasts';
import { SessionActionsMenu } from './SessionActionsMenu';
import { HeaderPortal, MobileHeaderControls } from './MobileHeaderControls';
import { useMobilePanel } from './useMobilePanel';
import { SessionModals, type MessageJumpHistory } from './SessionModals';
import { SessionSidebar } from './SessionSidebar';
import { useSessionActions } from './useSessionActions';
import { useMessageQueue } from '../../lib/useMessageQueue';
import { platformMessageCount, useSession } from './useSession';
import { usePendingSend } from './usePendingSend';
import { useFailedSendRehydrate } from './useFailedSendRehydrate';
import { useAutoApprove } from '../../lib/useAutoApprove';
import { ThreadSkeleton } from '../../components/Skeleton';

/** Memory bound on the in-memory message list. */
const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;
const ALL_MESSAGES_LIMIT = 2_147_483_647;
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
  const composerRef = useRef<ComposerHandle>(null);
  const openModelPicker = useCallback(() => composerRef.current?.openModelPicker(), []);
  const openAgentPicker = useCallback(() => composerRef.current?.openAgentPicker(), []);
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
  const factoryEpicID = searchParams.get('factoryEpic') ?? '';
  const [scrollToMessageBookmark, setScrollToMessageBookmark] = useState<{ sessionId: string; id: string; tick: number } | null>(null);
  // Route changes must win over in-flight streaming work. flushSync
  // forces React Router's location update to commit immediately so
  // the SSE lifecycle (keyed off the route id) tears down before
  // any further work races the click.
  const navigateToSession = useCallback((nextId: string) => {
    flushSync(() => {
      navigate(`/session/${nextId}`);
    });
  }, [navigate]);

  const { mobilePanel, toggleMobileSidebar, toggleMobileDetails, closeMobilePanel } = useMobilePanel(id);
  // Selecting a session from the drawer should reveal the conversation.
  // The drawer click path closes synchronously here (no flicker frame);
  // useMobilePanel's id-keyed effect covers every other navigation source
  // (command palette, auto-redirect, closed-session reopen).
  const navigateFromSidebar = useCallback((nextId: string) => {
    closeMobilePanel();
    navigateToSession(nextId);
  }, [closeMobilePanel, navigateToSession]);

  // The new SSE pipeline. Owns the EventSource, the reducer, the
  // initial fetch + reload + loadMore, the cache mirror, and the
  // memory bound. Returns the rendered view plus a small set of
  // lifecycle signals.
  const protectedMessageId = scrollToMessageBookmark && scrollToMessageBookmark.sessionId === id
    ? scrollToMessageBookmark.id
    : null;
  const view = useSession(id, {
    debug: debugMode,
    maxMessages: MAX_RETAINED_MESSAGES,
    trimTo: TRIMMED_RETAINED_MESSAGES,
    protectedMessageId,
  });
  const {
    session,
    messages,
    parts,
    pendingPermission,
    pendingQuestion,
    checkingPermissionId,
    judgeStartsAt,
    judgeReasoning,
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
    changesDirtyTick,
    reload,
    loadMore,
    hydrateHistory,
    clearPrompt,
    setPendingPermission,
    setPendingQuestion,
    patchSession,
  } = view;

  // Tracks an in-flight send and failed-send retry state. It is never
  // materialised into the thread; server messages are the source of truth.
  const pending = usePendingSend(id);
  // Auto-clear pending when SSE delivers the real user message.
  // Runs in an effect (not render) so the pending → null setState
  // is properly batched and React doesn't see a setState during
  // another component's render phase. observeMessages is a stable
  // useCallback inside usePendingSend, so the effect only re-runs
  // when messages itself changes identity (i.e., a real SSE
  // update).
  const observeMessages = pending.observeMessages;
  useEffect(() => {
    observeMessages(messages);
  }, [messages, observeMessages]);

  const { firstUnreadMessageId, unreadMessageCount } = useUnreadMarker(session, messages);
  const { visibleSessionWarnings, dismissSessionWarning } = useSessionWarnings(session);

  const handleScrollToMessageBookmark = useCallback((bookmark: MessageBookmark) => {
    const updateScrollRequest = () => {
      setScrollToMessageBookmark((current) => ({
        sessionId: bookmark.sessionId,
        id: bookmark.id,
        tick: (current?.tick ?? 0) + 1,
      }));
    };
    if (bookmark.sessionId === id) {
      updateScrollRequest();
      return;
    }
    flushSync(() => {
      updateScrollRequest();
      navigate(`/session/${bookmark.sessionId}`);
    });
  }, [id, navigate]);

  const sseActive = sseStatus === 'live';

  // Capability flags for the owning platform.
  const caps = usePlatformCapabilities(session?.platform);
  const worktreesSupported = useOpencodeLaunch();

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
    reloadCapabilities,
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
    subagentTokens,
    setSubagentTokens,
    taskLiveOutput,
  } = useSubagentTracking(parts, id);
  useSessionSeen({ session, patchSession });

  // Sidebar state, archive/pin handlers, archived toggle, collapsed groups.
  const collapsedProjects = useUiStore((state) => state.collapsedProjects);
  const abortControllerRef = useRef<AbortController | null>(null);
  const resetSessionIdRef = useRef<string | undefined>(undefined);
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
    // Fall back to the URL id so the sidebar's initial load fires even
    // when no session resolves (the `new` sentinel after archiving the
    // last one) — session?.id would be undefined and skip the load.
    sessionId: session?.id ?? id,
    collapsedProjects,
    sidebarView: 'projects',
    abortSignalRef: abortControllerRef,
    navigate,
  });
  const sessionTree = view.sessionTree;
  const parentSession = session?.parentId
    ? sessionTree.find((candidate) => candidate.id === session.parentId)
      ?? recentSessions.find((candidate) => candidate.id === session.parentId)
    : undefined;
  const promptSessionIds = useMemo(() => {
    const ids = new Set(id ? [id] : []);
    for (let added = true; added;) {
      added = false;
      for (const candidate of sessionTree) {
        if (!candidate.parentId || !ids.has(candidate.parentId) || ids.has(candidate.id)) continue;
        ids.add(candidate.id);
        added = true;
      }
    }
    return [...ids];
  }, [id, sessionTree]);
  const gitInfoRemoteId = session?.remoteId
    ?? recentSessions.find((candidate) => candidate.id === id)?.remoteId
    ?? 'local';
  const { infos: siblingGitInfos } = useGitInfo(
    recentSessions
      .filter((candidate) => (candidate.remoteId || 'local') === gitInfoRemoteId)
      .map((candidate) => candidate.directory)
      .filter(Boolean),
    gitInfoRemoteId,
  );
  const {
    bookmarkedMessageIds,
    selectedMessageBookmarkKey,
    messageBookmarkGroups,
    handleToggleMessageBookmark,
    handleRemoveMessageBookmark,
  } = useMessageBookmarks({ id, session, messages, parts, recentSessions });

  // Tmux state.
  const tmux = useTmux();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  // Surface a failed OpenCode launch (previously logged only) via the
  // restart toast, so the user isn't left with a button that appears to
  // do nothing. Declared here so useTmuxActions can report into it.
  const [restartToastMessage, setRestartToastMessage] = useState<string | null>(null);
  const tmuxActions = useTmuxActions(tmux, session?.directory, setRestartToastMessage, {
    reload,
    isLive: () => portAvailableRef.current,
  });
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

  // Auto-approve enabled/disabled state. The actual judge runs server-side;
  // checking/approval state arrives via SSE (ocman.permission.checking and
  // ocman.permission.auto-approved) and is reflected through the reducer.
  const autoApprove = useAutoApprove({
    sessionId: session?.id ?? '',
    capable: caps.autoApprove && portAvailable,
  });

  // Whether the backend judge is currently evaluating the pending permission.
  const autoApproveChecking =
    pendingPermission !== null &&
    checkingPermissionId === pendingPermission.permissionId;

  // Toast / modal state.
  const [showShareModal, setShowShareModal] = useState(false);
  const [showRenameModal, setShowRenameModal] = useState(false);
  const [showForkPicker, setShowForkPicker] = useState(false);
  const [showMovePicker, setShowMovePicker] = useState(false);
  const [showMovePathDialog, setShowMovePathDialog] = useState(false);
  const [showMessageJumpPicker, setShowMessageJumpPicker] = useState(false);
  const [messageJumpHistory, setMessageJumpHistory] = useState<MessageJumpHistory | null>(null);
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [copyToastMessage, setCopyToastMessage] = useState<string | null>(null);
  const [sendRetryDelaySeconds, setSendRetryDelaySeconds] = useState<number | null>(null);
  const [threadBoundaryResetNonce, setThreadBoundaryResetNonce] = useState(0);

  const archiveSession = useApiStore((state) => state.archiveSession);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const seedNewSession = useApiStore((state) => state.seedNewSession);

  const sidebarWidth = useUiStore((state) => state.sidebarWidth);
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

    if (resetSessionIdRef.current !== id) {
      resetSessionIdRef.current = id;
      setSelectedModel('');
      setSelectedAgent('');
      setSelectedReasoning('');
    }

    getWhisperStatus().then((s) => setWhisperAvailable(s.available)).catch(() => setWhisperAvailable(false));
    if (id) refreshModels(signal);

    return () => controller.abort();
  }, [id, getWhisperStatus, refreshModels, setSelectedAgent, setSelectedModel, setSelectedReasoning]);
  /* eslint-enable react-hooks/set-state-in-effect */

  const { failedSends, setFailedSends } = useFailedSendRehydrate({
    id,
    sessionLoaded: !!session,
    messages,
    parts,
    pending,
  });

  usePromptSync({
    id,
    session,
    parts,
    recentSessions,
    portAvailable,
    promptSessionIds,
    pendingPermission,
    pendingQuestion,
    clearPrompt,
    setPendingPermission,
    setPendingQuestion,
    setPermissionError,
  });

  // Aggregate token/cost stats.
  const liveTokens = useMemo(() => computeLiveTokens(messages), [messages]);
  const tokenStats = useMemo(
    () => mergeTokenStats(session, liveTokens),
    [session, liveTokens],
  );
  const sessionTreeStats = useMemo(
    () => session ? aggregateSessionTreeStats(
      session,
      [...sessionTree, ...recentSessions],
      tokenStats,
    ) : undefined,
    [session, sessionTree, recentSessions, tokenStats],
  );

  const {
    activeAgent,
    activeModel,
    composerModels,
    handleModelChange,
    handleAgentChange,
  } = useComposerModel({
    id,
    session,
    messages,
    parts,
    modelOptions,
    agents,
    setSelectedModel,
    setSelectedAgent,
    setSelectedReasoning,
  });

  const handleNewSessionInDirectory = useCallback(async (directory: string, remoteId?: string, platform?: string, title?: string) => {
    // Prefer the target project's own platform/host (e.g. a remote
    // project group) over the currently-open session's, so a "+" on a
    // remote project actually targets that remote instead of falling
    // back to the local adapter.
    //
    // Only inherit the open session's platform when the target is the
    // same project — otherwise a "+" on a *different* project (whose
    // group didn't carry a platform) leaks the current session's
    // (possibly remote) platform onto it, mis-targeting the host.
    const sameProject = !!session && projectRootForDirectory(directory) === projectRootForDirectory(session.directory);
    const targetPlatform = platform ?? (sameProject ? session?.platform : undefined);
    try {
      const res = await createSessionWithLaunch(
        {
          createSession,
          launchOpencodeInTmux,
          tmuxAvailable: tmux.available,
        },
        { directory, fallbackDirectory: projectRootForDirectory(directory), platform: targetPlatform, remoteId, title },
      );
      if (res.id) {
        const sessionDirectory = res.directory ?? directory;
        seedNewSession(res.id, sessionDirectory, targetPlatform ?? '', title, remoteId);
        navigateToSession(res.id);
      }
    } catch (e) {
      remoteLog.error('Failed to create session', e);
      setShowCreateSessionErrorToast(true);
    }
  }, [createSession, launchOpencodeInTmux, tmux.available, navigateToSession, seedNewSession, session, setShowCreateSessionErrorToast]);

  const handleNewSession = useCallback(async (title?: string) => {
    if (!session) return;
    await handleNewSessionInDirectory(session.directory, session.remoteId, session.platform, title);
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
      remoteLog.error('Failed to compact session', e);
    }
  }, [activeAgent, activeModel, caps.compact, portAvailable, selectedAgent, selectedModel, session, setSelectedAgent]);

  // Kept in sync with `isRunning` (computed below) so handleShell can
  // decide whether to queue a `!`-prefixed shell command. The ref
  // breaks the ordering cycle: useSessionActions runs before
  // `isRunning` exists, but only reads the ref at call time.
  const isRunningRef = useRef(false);

  // Latest transcript, mirrored into refs so `/export` can read it
  // without re-binding handleCommand on every message/part update.
  const messagesRef = useRef(messages);
  const partsRef = useRef(parts);
  useEffect(() => {
    messagesRef.current = messages;
    partsRef.current = parts;
  }, [messages, parts]);

  // Follow-up message queue (#58): prompts submitted while the agent is
  // mid-turn queue server-side and drain one per turn. Shared across
  // clients via the ocman.queue.updated broadcast (reliable full-state).
  const { queue: queuedMessages, refresh: refreshMessageQueue, remove: removeQueuedMessage, move: moveQueuedMessage } =
    useMessageQueue(session?.id, session?.platform);

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
    queuedShellCommand,
    cancelQueuedShell,
    flushQueuedShell,
  } = useSessionActions({
    session,
    routeSessionId: id,
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
    setShowForkPicker,
    setShowMovePicker,
    setShowRenameToast,
    setShowDisconnectedToast,
    setRestartToastMessage,
    reloadCapabilities,
    setCopyToastMessage,
    refreshThread: reload,
    refreshMessageQueue,
  });

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

  // Alt+J / Alt+K: navigate between recent sessions.
  const jumpToSession = useCallback((direction: 1 | -1) => {
    const sessions = recentSessionsRef.current;
    const currentIndex = sessions.findIndex((s) => s.id === id);
    if (currentIndex === -1) return;
    const target = sessions[currentIndex + direction];
    if (target) navigateToSession(target.id);
  }, [id, navigateToSession, recentSessionsRef]);

  // Refs for the palette dispatcher / shortcut handlers.
  usePaletteCommands({
    sessionRef: useSyncRef(session),
    archiveSessionRef: useSyncRef(archiveSession),
    navigateRef: useSyncRef(navigate),
    portAvailableRef,
    capsRef: useSyncRef(caps),
    selectedModelRef: useSyncRef(selectedModel),
    activeModelRef: useSyncRef(activeModel),
    tmux,
    setSelectedReasoning,
    setShowRenameModal,
    openModelPicker,
    openAgentPicker,
  });

  useSessionShortcuts({
    session,
    portAvailable,
    matchingTmuxSession,
    jumpToSession,
    handleTmuxShortcut,
    handleVSCodeShortcut,
    handleNewSession,
    openModelPicker,
    openMessageJumpPicker: () => {
      setShowMessageJumpPicker(true);
      if (!session) return;
      void api.session(session.id, ALL_MESSAGES_LIMIT, 0, undefined, session.platform)
        .then((detail) => {
          const messageIds = new Set(detail.messages.map((message) => message.id));
          const partIds = new Set(detail.parts.map((part) => part.id));
          setMessageJumpHistory({
            sessionId: session.id,
            messages: [...detail.messages, ...messages.filter((message) => !messageIds.has(message.id))],
            parts: [...detail.parts, ...parts.filter((part) => !partIds.has(part.id))],
          });
        })
        .catch((error) => remoteLog.error('Failed to load message jump history', error));
    },
  });

  const hasMore = platformMessageCount(messages) < totalMessages;
  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  const sessionId = session?.id;
  const permissionControl = useMemo(
    () => (caps.permissionRules && portAvailable && sessionId ? <PermissionModeLock sessionId={sessionId} /> : null),
    [caps.permissionRules, portAvailable, sessionId],
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

  // Keep the ref handleShell reads in sync, and flush any queued
  // shell command when the assistant turn finishes (true → false).
  useEffect(() => {
    isRunningRef.current = isRunning;
    if (!isRunning) flushQueuedShell();
  }, [isRunning, flushQueuedShell]);

  const { displayStatus, liveTokensPerSecond } = useSessionStatus({
    lastMsg,
    messages,
    subagentTokens,
    setSubagentTokens,
    sessionStatus: session?.status,
    awaitingAssistantResponse,
    isRunning,
    pendingPermission,
    pendingQuestion,
  });
  // No status mirror into recentSessions. `displayStatus` layers a send
  // affordance and the errored-tail signal on top of the reported
  // status, and writing it into shared state let a page-local view
  // overwrite the authoritative value for every other consumer of the
  // row. The sidebar already gets the settled status straight from the
  // `ocman.session.changed` patch (see useSidebarSessions); the active
  // row layers those on for display only.

  // Flag for the composer's "launch session" button.
  const launchHintActive = canLaunchSession({
    portAvailable,
    hasPendingPrompt,
    tmuxAvailable: tmux.available,
    liveConnectionHint: !!caps.liveConnectionHint,
    directory: session?.directory,
  });

  // Sidebar project groupings + project reorder/archive handlers.
  const {
    allProjects,
    sidebarProjectGroups,
    handleReorderProjects,
    handleArchiveProjectFromSidebar,
  } = useSidebarProjectGroups({ id, recentSessions, displayStatus });

  return (
    <Toast.Provider swipeDirection="right">
      <div
        className={`session-layout${mobilePanel === 'sidebar' ? ' mobile-sidebar-open' : ''}${mobilePanel === 'details' ? ' mobile-details-open' : ''}`}
        data-testid="session-layout"
      >
        <MobileHeaderControls
          mobilePanel={mobilePanel}
          toggleMobileSidebar={toggleMobileSidebar}
          toggleMobileDetails={toggleMobileDetails}
        />
        <SessionSidebar
          activeId={id}
          sidebarWidth={sidebarWidth}
          showArchivedRecent={showArchivedRecent}
          setShowArchivedRecent={setShowArchivedRecent}
          loadingRecentSessions={loadingRecentSessions}
          recentSessions={recentSessions}
          sidebarProjectGroups={sidebarProjectGroups}
          onReorderProjects={handleReorderProjects}
          archivingSessionIds={archivingSessionIds}
          collapsedProjectSet={collapsedProjectSet}
          toggleCollapsedProject={toggleCollapsedProject}
          siblingGitInfos={siblingGitInfos}
          activeDisplayStatus={displayStatus}
          debugMode={debugMode}
          pendingTmuxSession={pendingTmuxSession}
          pickerPos={pickerPos}
          pickerRef={pickerRef}
          tmux={tmux}
          onNavigateToSession={navigateFromSidebar}
          onArchiveSession={handleArchiveSession}
          onPinSession={handlePinSession}
          onClientSelect={handleClientSelect}
          onNewSessionInDirectory={handleNewSessionInDirectory}
          onArchiveProject={handleArchiveProjectFromSidebar}
        />
        <div className="session-main" data-testid="session-main">
          {session && mobilePanel !== 'sidebar' && <HeaderPortal>
            <SessionActionsMenu
              sessionId={session.id}
              tmuxAvailable={tmux.available}
              matchingTmuxSession={matchingTmuxSession}
              portAvailable={portAvailable}
              liveConnectionHint={caps.liveConnectionHint}
              launchingOpencode={launchingOpencode}
              onNewSession={() => { void handleNewSession(); }}
              onShare={() => setShowShareModal(true)}
              onTmuxSwitch={handleTmuxSwitch}
              onLaunchOpencode={() => { void handleLaunchOpencode(); }}
              onOpenVSCode={handleVSCodeShortcut}
            />
          </HeaderPortal>}
          {session && showShareModal && (
            <ShareLinkModal sessionId={session.id} onClose={() => setShowShareModal(false)} />
          )}
          {loading ? (
            <ThreadSkeleton rows={5} />
          ) : loadError ? (
            <div className="oc-error-banner" data-testid="error-banner" style={{ margin: 24 }}>
              {loadError}
              <button onClick={() => { void reload(); }}>Retry</button>
            </div>
          ) : id === 'new' && !session ? (
            <div className="oc-empty-detail" data-testid="empty-detail" style={{ margin: 24, opacity: 0.7 }}>
              <p>No session open.</p>
              <p>Pick a session from the sidebar, or press <kbd>⌘K</kbd> and run <code>/new</code> to start one.</p>
            </div>
          ) : session && (
            <OcmanRuntimeProvider
              key={session.id}
              messages={messages}
              parts={parts}
              sessionId={session.id}
              platformId={session.platform}
              canSend={portAvailable && caps.composer}
              pendingAgent={selectedAgent || activeAgent || undefined}
              agents={agents}
              modelEntries={modelEntries}
              taskLiveOutput={taskLiveOutput}
              projectDirectory={session.directory}
              failedSends={failedSends}
              onRetryFailedSend={handleRetrySend}
              onDismissFailedSend={handleDismissFailedSend}
            >
              {session.parentId && (
                <aside className="oc-parent-session-note" role="note">
                  <i className="bi bi-arrow-return-left" aria-hidden="true" />
                  <span>Child session of</span>
                  <Link to={`/session/${encodeURIComponent(session.parentId)}`}>
                    {cleanTitle(parentSession?.title) || 'Parent session'}
                  </Link>
                </aside>
              )}
              <ErrorBoundary
                name="session:thread"
                resetKey={`${session.id}:${threadBoundaryResetNonce}`}
                fallbackRender={renderThreadBoundaryFallback}
              >
                <AssistantThread
                  hasMore={hasMore}
                  loadingMore={loadingMore}
                  onLoadMore={loadMore}
                  bookmarkedMessageIds={bookmarkedMessageIds}
                  onToggleMessageBookmark={handleToggleMessageBookmark}
                  scrollToMessageId={scrollToMessageBookmark?.sessionId === session.id ? scrollToMessageBookmark.id : null}
                  scrollToMessageTick={scrollToMessageBookmark?.sessionId === session.id ? scrollToMessageBookmark.tick : 0}
                  composer={(
                    <ErrorBoundary name="session:composer" inline resetKey={session.id}>
                      <FactoryPlanApproval epicID={factoryEpicID} platformID={session.platform} sessionID={session.id} />
                      {firstUnreadMessageId && unreadMessageCount > 0 && (
                        <button
                          type="button"
                          className="oc-jump-unread"
                          data-testid="jump-to-first-unread"
                          onClick={() => setScrollToMessageBookmark({
                            sessionId: session.id,
                            id: firstUnreadMessageId,
                            tick: Date.now(),
                          })}
                          title="Scroll to the first message you haven't seen yet"
                        >
                          <i className="bi bi-arrow-up" aria-hidden="true" />
                          {' '}
                          {unreadMessageCount} new message{unreadMessageCount === 1 ? '' : 's'}
                        </button>
                      )}
                      {pendingPermission && caps.respondPermission ? (
                        <PermissionPrompt
                          permission={pendingPermission}
                          onReply={handlePermissionReply}
                          disabled={answeringPermission}
                          error={permissionError}
                          autoApproveCapable={caps.autoApprove}
                          autoApproveEnabled={autoApprove.enabled}
                          autoApproveChecking={autoApproveChecking}
                          judgeStartsAt={judgeStartsAt}
                          judgeReasoning={judgeReasoning}
                          onEnableAutoApprove={() => autoApprove.setEnabled(true)}
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
                          composerRef={composerRef}
                          onSend={handleSend}
                          onRetryChange={setSendRetryDelaySeconds}
                          onCommand={handleCommand}
                          onShell={handleShell}
                          shellExec={caps.shellExec}
                          queuedShellCommand={queuedShellCommand}
                          onCancelQueuedShell={cancelQueuedShell}
                          queuedMessages={queuedMessages}
                          onRemoveQueuedMessage={removeQueuedMessage}
                          onMoveQueuedMessage={moveQueuedMessage}
                          onAbort={handleAbort}
                          isRunning={isRunning}
                          disabled={!portAvailable || hasPendingPrompt}
                          disabledHint={hasPendingPrompt
                            ? 'Respond to the pending prompt above before sending a new message.'
                            : caps.liveConnectionHint}
                          whisperAvailable={whisperAvailable}
                          models={composerModels}
                          modelEntries={modelEntries}
                          selectedModel={selectedModel}
                          onModelChange={handleModelChange}
                          onToggleFavorite={handleToggleFavorite}
                          onRefreshModels={refreshModels}
                          activeAgent={activeAgent}
                          selectedAgent={selectedAgent}
                          onAgentChange={handleAgentChange}
                          agents={agents}
                          agentsLoaded={agentsLoaded}
                          contextTokens={session?.contextTokenCount || undefined}
                          activeDurationMs={session?.activeDurationMs}
                          timeCreated={session?.timeCreated}
                          durationMs={session?.durationMs}
                          sessionId={session?.id}
                          tokensPerSecond={liveTokensPerSecond ?? undefined}
                          tokenStats={tokenStats}
                          estimatedCost={sessionTree.find((item) => item.id === session?.id && item.platform === session?.platform)?.totalEstCost ?? session?.totalEstCost}
                          sessionTreeStats={sessionTreeStats}
                          selectedReasoning={selectedReasoning}
                          onReasoningChange={setSelectedReasoning}
                          onLaunchRequest={launchHintActive ? () => { void handleLaunchOpencode(); } : undefined}
                          launching={launchingOpencode}
                          directory={session?.directory}
                          newConversation={totalMessages === 0}
                          worktreesSupported={worktreesSupported}
                          permissionControl={permissionControl}
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
              <SessionModals
                session={session}
                messages={messages}
                parts={parts}
                allProjects={allProjects}
                recentSessions={recentSessions}
                messageJumpHistory={messageJumpHistory}
                pending={pending}
                showRenameModal={showRenameModal}
                setShowRenameModal={setShowRenameModal}
                showForkPicker={showForkPicker}
                setShowForkPicker={setShowForkPicker}
                showMessageJumpPicker={showMessageJumpPicker}
                setShowMessageJumpPicker={setShowMessageJumpPicker}
                showMovePicker={showMovePicker}
                setShowMovePicker={setShowMovePicker}
                showMovePathDialog={showMovePathDialog}
                setShowMovePathDialog={setShowMovePathDialog}
                patchSession={patchSession}
                navigateToSession={navigateToSession}
                hydrateHistory={hydrateHistory}
                onRenamed={() => setShowRenameToast(true)}
                onScrollToMessage={(messageId) => setScrollToMessageBookmark({
                  sessionId: session.id,
                  id: messageId,
                  tick: Date.now(),
                })}
              />
            </OcmanRuntimeProvider>
          )}
          {session && (
            <SessionTerminalDock
              tmuxAvailable={tmux.available}
              directory={session.directory}
              remoteId={session.remoteId}
            />
          )}
        </div>
        {id && (
          <RightPanel
            sessionId={id}
            platformId={session?.platform}
            directory={session?.directory}
            dirtyTick={changesDirtyTick}
            session={session ?? undefined}
            messageBookmarkGroups={messageBookmarkGroups}
            selectedMessageBookmarkKey={selectedMessageBookmarkKey}
            onRemoveMessageBookmark={handleRemoveMessageBookmark}
            onScrollToMessageBookmark={handleScrollToMessageBookmark}
          />
        )}
        {session && (
          <>
            {visibleSessionWarnings.map((warning) => (
              <SessionWarningBanner
                key={sessionWarningKey(session.id, warning)}
                warning={warning}
                onDismiss={() => dismissSessionWarning(warning)}
              />
            ))}
            {session.notice && (
              <RateLimitBanner
                key={session.id}
                notice={session.notice}
                onChangeModel={caps.composer && !hasPendingPrompt ? openModelPicker : undefined}
              />
            )}
            <McpAuthBanner sessionId={session.id} platformId={session.platform} />
          </>
        )}
        <SessionToasts
          showRenameToast={showRenameToast}
          setShowRenameToast={setShowRenameToast}
          restartToastMessage={restartToastMessage}
          setRestartToastMessage={setRestartToastMessage}
          showCreateSessionErrorToast={showCreateSessionErrorToast}
          setShowCreateSessionErrorToast={setShowCreateSessionErrorToast}
          showDisconnectedToast={showDisconnectedToast}
          setShowDisconnectedToast={setShowDisconnectedToast}
          copyToastMessage={copyToastMessage}
          setCopyToastMessage={setCopyToastMessage}
          sendRetryDelaySeconds={sendRetryDelaySeconds}
          setSendRetryDelaySeconds={setSendRetryDelaySeconds}
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
