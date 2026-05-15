import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { flushSync } from 'react-dom';
import { useStickyNavigate } from '../../lib/useStickyNavigate';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api, type SessionDetail } from '../../lib/api';
import { cleanTitle, shortPath } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { OcmanRuntimeProvider } from '../../components/OcmanRuntimeProvider';
import { AssistantThread } from '../../components/AssistantThread';
import { Composer } from '../../components/assistant/Composer';
import { QuestionPrompt, type PendingQuestion } from '../../components/session/QuestionPrompt';
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
import { hashSession, hashMessagesAndParts } from '../../lib/sessionHash';
import { createSessionWithLaunch, type LaunchStatus } from '../../lib/createSessionWithLaunch';
import {
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  deriveActiveModelAndAgent,
} from '../../lib/sessionStatus';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import {
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
  type PendingPermission,
} from '../../lib/sseHelpers';
import { useSyncRef } from '../../lib/useSyncRef';
import { useSubagentTracking } from './useSubagentTracking';
import { useTmuxActions } from './useTmuxActions';
import { useSessionStatus } from './useSessionStatus';
import { useSidebarSessions } from './useSidebarSessions';
import { useSessionMessages } from './useSessionMessages';
import { useSessionCapabilities } from './useSessionCapabilities';
import {
  usePromptHandlers,
  storePendingQuestion,
  loadPendingQuestion,
} from './usePromptHandlers';
import { useSessionShortcuts } from './useSessionShortcuts';
import { useSessionSSE } from './useSessionSSE';
import { usePaletteCommands } from './usePaletteCommands';
import { useGhostInjection } from './useGhostInjection';
import { usePendingPromptSync } from './usePendingPromptSync';
import { SseStatusIndicator } from './SseStatusIndicator';
import { remoteLog } from '../../lib/remoteLog';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';
import { SessionToasts } from './SessionToasts';
import { SessionSidebar, type SidebarProjectGroup } from './SessionSidebar';
import { RenameModal } from './RenameModal';
import { useSessionActions } from './useSessionActions';

const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;
const THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS = 5_000;

/**
 * Props for the inner SessionDetail component.
 *
 * `id` is threaded in from the wrapper in `./index.tsx` rather than
 * being read via `useParams()` here. Bypassing the param subscription
 * forces a re-render whenever the URL changes — function-component
 * identity equality only short-circuits when ALL inputs are equal,
 * so a new id prop guarantees React schedules an update even if
 * react-router's context propagation is contended (which we have
 * observed in practice under sustained SSE activity).
 */
export interface SessionDetailProps {
  id: string | undefined;
}

export function SessionDetail({ id }: SessionDetailProps) {
  // Use the sticky-navigate wrapper so diagnostic flags like ?debug
  // survive when the user clicks across sessions. Without this the
  // search string gets dropped on the first navigation and the
  // remoteLog instrumentation silently turns off.
  const navigate = useStickyNavigate();
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
  const activeSessionIdRef = useRef<string | undefined>(id);
  activeSessionIdRef.current = id;
  // Route changes must win over in-flight streaming work. Wrapping
  // session navigation in flushSync forces React Router's location
  // update to commit immediately, after which the old session's
  // SSE lifecycle is torn down (keyed off the route id) and stale
  // work is cancelled instead of continuing to race the click.
  const navigateToSession = useCallback((nextId: string) => {
    flushSync(() => {
      navigate(`/session/${nextId}`);
    });
  }, [navigate]);
  const debugModeRef = useRef(debugMode);
  debugModeRef.current = debugMode;
  // Read the cache once at mount time via getState() — we want a snapshot
  // for the initial render, not a reactive subscription.
  const initialCached = id ? useApiStore.getState().getCachedSession(id) : null;
  const [session, setSession] = useState<(SessionDetail['session'] & { defaultAgent?: string; defaultModel?: string }) | null>(
    initialCached
      ? {
          ...initialCached.session,
          contextTokenCount: initialCached.session.contextTokenCount ?? initialCached.contextTokenCount,
          defaultAgent: initialCached.defaultAgent,
          defaultModel: initialCached.defaultModel,
        }
      : null,
  );
  // The remaining message-state declarations (messages, parts,
  // totalMessages, loading, loadingMore, loadError, switching,
  // changesDirtyTick) plus load() / loadMore() live in
  // useSessionMessages. Refs the hook needs (lastSessionHashRef,
  // abortSignalRef, droppedMessageCountRef) are declared further
  // down and threaded in via the options object — they're created
  // here so the SSE / cache mirror effects can reset them.
  const lastSessionHashRef = useRef('');
  const abortControllerRef = useRef<AbortController | null>(null);
  const droppedMessageCountRef = useRef(0);
  const {
    messages,
    setMessages,
    parts,
    setParts,
    totalMessages,
    setTotalMessages,
    loading,
    setLoading,
    loadingMore,
    loadError,
    setLoadError,
    switching,
    setSwitching,
    changesDirtyTick,
    setChangesDirtyTick,
    lastHashRef,
    load,
    loadMore,
  } = useSessionMessages({
    id,
    initialCached,
    setSession,
    lastSessionHashRef,
    abortSignalRef: abortControllerRef,
    droppedMessageCountRef,
    activeSessionIdRef,
  });

  // Stable refs for messages/parts — used by the ghost-injection effect
  // so it can read the latest values without listing them as deps (which
  // would create a cascade with the memory-trimming effect that also
  // depends on and mutates `messages`).
  const messagesRef = useSyncRef(messages);
  const partsRef = useSyncRef(parts);

  // Capability flags for the owning platform. Used to *hide* affordances
  // the platform doesn't support (composer, abort, compact, ...). Falls
  // back to all-false before /api/capabilities resolves, which keeps UI
  // dormant — preferable to flashing controls the platform can't honour.
  const caps = usePlatformCapabilities(session?.platform);

  // `portAvailable` represents transient reachability of the running
  // platform process (e.g. OpenCode on a discovered --port). Capability
  // flags describe what the platform supports in principle; an action
  // should generally be enabled iff both are true.
  const [whisperAvailable, setWhisperAvailable] = useState(false);

  // Per-session capability state — port availability, agent
  // catalog, model picker, selected model/agent/reasoning, plus
  // refreshModels and handleToggleFavorite. Encapsulated in
  // useSessionCapabilities; the page exposes setters because the
  // session-change effect resets them on navigation.
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

  // Subagent tracking — token snapshots per subagent message (for
  // the TPS indicator), live stdout per running task (rendered
  // inline in the assistant thread), and the set of known subagent
  // session ids (used by the SSE handler to route subagent prompts
  // back to this page). Encapsulated in useSubagentTracking; the
  // setSubagentTokens setter is exposed because the SSE effect
  // observes subagent token events and writes into the same map.
  const {
    subagentSessionIdsRef,
    subagentTokens,
    setSubagentTokens,
    taskLiveOutput,
  } = useSubagentTracking(parts, id);
  const { setInfo } = useHeaderInfo();
  usePageTitle(cleanTitle(session?.title) || 'Session');

  // Sidebar polling, archive/pin handlers, archived-toggle, and the
  // collapsed-projects fold-out. recentSessions now lives in Zustand so
  // SSE-derived optimistic writes survive navigation without being clobbered
  // by the next poll — any writer calls patchRecentSession and last wins.
  const collapsedProjects = useUiStore((state) => state.collapsedProjects);
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
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
  // Git info for sibling rows. Was populated by the backend's
  // /api/sessions handler via a synchronous fork-fan-out of
  // `git status` per directory; that produced multi-second pauses
  // across unrelated handlers under load (see docs/profiling.md).
  // We now opt in only while the sidebar is rendered, batching all
  // unique sibling dirs into one /api/git/info call refreshed on
  // a slow cadence.
  //
  // The hook normalises the input list internally, so passing a
  // fresh array literal on every render is fine — the effect dep
  // is the canonicalised query string, not the array identity.
  const { infos: siblingGitInfos } = useGitInfo(
    recentSessions.map((s) => s.directory).filter(Boolean),
  );
  // Tracks the currently-rendered session's directory so the session-change
  // effect can compare it against the incoming one without subscribing to
  // `session` (which would cause the effect to fire on every render).
  const currentDirectoryRef = useRef<string | undefined>(session?.directory);

  // Tmux state lives in two layers: useTmux() owns the upstream
  // catalog (sessions/clients/availability) and useTmuxActions wires
  // the per-page interactions (matching session, picker state,
  // launch flow, shortcut). The page consumes both because the
  // palette command dispatcher also reads tmux directly.
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
  // Mirrored so SSE's onopen closure can read the latest value
  // without re-subscribing. Used to gate the reconciliation fetch
  // (step 5 of spec/session-switch-cache).
  const loadErrorRef = useRef<string | null>(null);
  loadErrorRef.current = loadError;
  // Pending state lives at the page level so the SSE handler can
  // mirror it; the post-back side (in-flight flag, error, allow /
  // reply / reject handlers) is encapsulated in usePromptHandlers.
  const [pendingPermission, setPendingPermission] = useState<PendingPermission | null>(null);
  const [pendingQuestion, setPendingQuestion] = useState<PendingQuestion | null>(null);

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
    setPendingPermission,
    pendingQuestion,
    setPendingQuestion,
  });
  const [showRenameModal, setShowRenameModal] = useState(false);
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [threadBoundaryResetNonce, setThreadBoundaryResetNonce] = useState(0);
  const [createLaunchStatus, setCreateLaunchStatus] = useState<LaunchStatus>('idle');
  // Sends that failed on the client (network error, 5xx, etc.). Each entry
  // is keyed by the optimistic message id and holds enough context to
  // replay the send on Retry. Persisted via lib/failedSends so the user
  // can refresh the page without losing their prompt.
  const [failedSends, setFailedSends] = useState<FailedSend[]>([]);
  const archiveSession = useApiStore((state) => state.archiveSession);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const updateCachedSession = useApiStore((state) => state.updateCachedSession);
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

  // Keep the directory ref aligned with the currently-rendered session so the
  // next session-change effect can read the correct previous directory even
  // when the initial render started from a null session (cold load).
  useEffect(() => {
    currentDirectoryRef.current = session?.directory;
  }, [session?.directory]);

  // Re-fetch the session-scoped model list. Used on session entry, when
  // OpenCode becomes reachable, and whenever the user opens the model
  // Reset on session change — abort any in-flight requests from the previous session
  useEffect(() => {
    // Abort previous session's pending requests
    abortControllerRef.current?.abort();
    const controller = new AbortController();
    abortControllerRef.current = controller;
    const signal = controller.signal;

    // Hide the viewport for one paint frame so the fade-in clearly reads as
    // a navigation rather than an in-place content swap. Cleared on the next
    // animation frame (fires before the next paint).
    setSwitching(true);
    const rafId = window.requestAnimationFrame(() => setSwitching(false));

    droppedMessageCountRef.current = 0;
    // If we have a cached snapshot for this session, render it immediately
    // and seed the hash refs so the background load() only re-renders on a
    // real content change. Otherwise fall back to the original wipe-and-load
    // behaviour so the loading state still shows for first visits.
    const cached = id ? useApiStore.getState().getCachedSession(id) : null;
    const previousDirectory = currentDirectoryRef.current;
    let nextDirectory: string | undefined;
    if (cached) {
      const cachedSessionData = {
        ...cached.session,
        contextTokenCount: cached.session.contextTokenCount ?? cached.contextTokenCount,
        defaultAgent: cached.defaultAgent,
        defaultModel: cached.defaultModel,
      };
      setSession(cachedSessionData);
      setMessages(cached.messages);
      setParts(cached.parts);
      setTotalMessages(cached.totalMessages || cached.session.messageCount || 0);
      setLoading(false);
      lastSessionHashRef.current = hashSession(cachedSessionData);
      lastHashRef.current = hashMessagesAndParts(cached.messages, cached.parts);
      nextDirectory = cached.session.directory;
    } else {
      lastHashRef.current = '';
      lastSessionHashRef.current = '';
      setSession(null);
      setMessages([]);
      setParts([]);
      setTotalMessages(0);
      setLoading(true);
    }
    currentDirectoryRef.current = nextDirectory;
    // Port availability and the agent list are per-directory, not per-session.
    // When switching between sessions in the same project we already know the
    // correct values, so preserve them to avoid an agent-color flash while the
    // background refresh runs. Wipe only when the directory actually changes
    // (or is unknown — cold first visit).
    if (!nextDirectory || nextDirectory !== previousDirectory) {
      setPortAvailable(false);
    }
    setSelectedModel('');
    setSelectedAgent('');
    setSelectedReasoning('');
    setPendingPermission(null);
    setPermissionError(null);
    setPendingQuestion(null);
    setSseDebugEvents([]);
    // Rehydrate failed sends for this session from persistent storage. The
    // ghost user-message injection (so the bubble re-appears with its
    // Retry banner after a refresh) happens in a separate effect below
    // once `messages` has been populated by load() — that way we can skip
    // entries whose prompt has already arrived through SSE.
    setFailedSends(id ? listFailedSends(id) : []);
    load(signal);
    // portAvailable is now derived from session.liveConnection (populated
    // by the platform adapter). The state variable is kept because SSE
    // onopen still overrides it to true on a successful connection.
    getWhisperStatus().then(s => setWhisperAvailable(s.available)).catch(() => setWhisperAvailable(false));
    // Fetch the rich session-scoped model list (historical + live-available
    // from /config/providers). Falls back to the plain global usage list when
    // the new endpoint fails so the composer still works on older backends.
    if (id) {
      refreshModels(signal);
    }

    return () => {
      controller.abort();
      window.cancelAnimationFrame(rafId);
    };
  // eslint-disable-next-line react-hooks/exhaustive-deps -- setSseDebugEvents comes from useSessionSSE (declared after this effect); it's a stable useState setter and safe to omit.
  }, [getWhisperStatus, id, load, refreshModels, lastHashRef, setLoading, setMessages, setParts, setPermissionError, setPortAvailable, setSelectedAgent, setSelectedModel, setSelectedReasoning, setSwitching, setTotalMessages]);

  useGhostInjection({
    session,
    failedSends,
    setFailedSends,
    messagesRef,
    partsRef,
    setMessages,
    setParts,
  });

  useEffect(() => {
    if (messages.length <= MAX_RETAINED_MESSAGES) return;

    const retainedMessages = messages.slice(-TRIMMED_RETAINED_MESSAGES);
    const droppedCount = messages.length - retainedMessages.length;
    const retainedMessageIds = new Set(retainedMessages.map((message) => message.id));
    droppedMessageCountRef.current += droppedCount;

    setMessages(retainedMessages);
    setParts((prev) => prev.filter((part) => retainedMessageIds.has(part.messageId)));
  }, [messages, setMessages, setParts]);

  // Mirror live session data into the per-session detail cache so switching
  // away and back renders instantly. `updateCachedSession` no-ops when the
  // session isn't in the cache, so this only runs after the initial load()
  // has seeded an entry. See spec/session-switch-cache.
  useEffect(() => {
    if (!id || !session) return;
    updateCachedSession(id, (prev) => ({
      ...prev,
      session,
      messages,
      parts,
      totalMessages: Math.max(prev.totalMessages ?? 0, totalMessages),
    }));
  }, [id, session, messages, parts, totalMessages, updateCachedSession]);

  const hasPendingPrompt = pendingPermission !== null || pendingQuestion !== null;

  usePendingPromptSync({
    id,
    pendingPermission,
    pendingQuestion,
    setPendingPermission,
    setPendingQuestion,
    setPermissionError,
    recentSessions,
    subagentSessionIdsRef,
    listPermissions,
    listQuestions,
  });

  // Stable handler + flag for the composer's "launch opencode" hint.
  // The Composer is wrapped in React.memo with a hand-written
  // comparator that checks `onLaunchRequest === ` — so passing a
  // fresh arrow on every render would force Composer to re-render on
  // every SSE delta and every keystroke during streaming.
  const handleLaunchHintClick = useCallback(() => setShowDisconnectedToast(true), []);
  const launchHintActive = !portAvailable && !hasPendingPrompt && tmux.available && !!caps.liveConnectionHint;

  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;

  useEffect(() => {
    if (!sessionSeenId || !sessionSeenPlatform) return;
    void markSessionSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated)
      .then(() => {
        setSession(prev => prev && prev.id === sessionSeenId ? { ...prev, seen: true } : prev);
        patchRecentSession(sessionSeenId, { seen: true });
        recheckFaviconNotify();
      })
      .catch(err => console.error('Failed to mark session seen', err));
  }, [markSessionSeen, sessionSeenId, sessionSeenPlatform, sessionSeenUpdated, patchRecentSession]);

  // Restore pending question when navigating to a page.
  // Check sessionStorage for a previously received question (stored when the
  // SSE question.asked event fired), but only if the parts still show the
  // question tool call as pending (not yet answered).
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
    if (stored) {
      setPendingQuestion(stored);
    }
  }, [parts, session?.id, portAvailable, pendingQuestion]);

  // SSE — owns the EventSource lifecycle, reconnection back-off,
  // event parsing, and write-through into the page's message /
  // part / session / prompt / subagent state. The hook reads
  // session id + directory and the various setters; everything
  // else stays in the page so the SSE handler doesn't need to
  // know about composer / sidebar / palette concerns.
  const {
    recentWorkEventAt,
    sseActive,
    sseReconnecting,
    sseReconnectAttempt,
    sseNextRetryAt,
    retryNow: sseRetryNow,
    sseDebugEvents,
    setSseDebugEvents,
  } = useSessionSSE({
    // IMPORTANT: key the SSE lifecycle off the ROUTE id, not the
    // currently-rendered session object. When the user clicks to a
    // different session while the current one is still streaming,
    // `session?.id` can lag behind the route for a render or two
    // (the page still holds the old session snapshot until the
    // session-change effect resets it). If SSE stays keyed to the old
    // session object, its EventSource keeps delivering updates for the
    // previous session after navigation has already started, and those
    // writes race with / delay the route transition. Using the route
    // id tears down the old EventSource immediately on click so stale
    // session work is cancelled with navigation priority.
    sessionId: id,
    directory: session?.directory,
    load,
    abortSignalRef: abortControllerRef,
    loadErrorRef,
    debugModeRef,
    subagentSessionIdsRef,
    setMessages,
    setParts,
    setSession,
    setPortAvailable,
    setPendingPermission,
    setPermissionError,
    setPendingQuestion,
    setSubagentTokens,
    setChangesDirtyTick,
    activeSessionIdRef,
  });

  // Compute aggregate token/cost stats from the messages array so the header
  // stays up-to-date from SSE events without needing a server round-trip.
  // Use the larger of server-provided totals and locally-computed totals.
  // The server value covers all messages including paginated-out ones;
  // the local value picks up incremental SSE updates before the next load().
  // Memoise per-message walks so they don't re-execute on every
  // SSE-driven re-render. With messages.length around 200 and ~10–30
  // renders/sec during streaming we'd otherwise burn thousands of
  // array iterations per second on these three helpers; the work
  // is unchanged on stable input but skipped when the dep tuple
  // hasn't changed identity. `messages` is a fresh array reference
  // on every meaningful update from useSessionMessages, so the
  // memo invalidates exactly once per real change.
  const liveTokens = useMemo(() => computeLiveTokens(messages), [messages]);
  const tokenStats = useMemo(
    () => mergeTokenStats(session, liveTokens),
    [session, liveTokens],
  );

  // Header info: the breadcrumb shows the session title + a platform
  // badge; the right-hand slot holds the project path. The richer
  // per-session metadata (Duration / Messages / Tokens / Changes /
  // Cost) used to live in the header as a stats strip and is now
  // rendered in the right-panel "Session info" pane
  // (SessionInfoSidebar). Project stays in the header because it
  // anchors the page (which folder am I looking at?) and is the one
  // piece of session context worth scanning at a glance from
  // anywhere in the page.
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
        // Seed the cache with a minimal stub so the navigation target
        // renders an empty thread immediately instead of a loading spinner.
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
    // Snapshot the effective agent before compact. OpenCode's summarize adds a
    // message whose agent becomes the new `activeAgent` (derived from the last
    // message with an agent), which would override the user's selection. Pin
    // the pre-compact agent via `selectedAgent` so the next send continues to
    // use it.
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
  });

  const handleThreadBoundaryRetry = useCallback((error: Error, force = false) => {
    const now = Date.now();
    const previous = threadBoundaryRecoveryRef.current;
    if (
      !force
      &&
      previous
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
    setLoadError(null);
    void load();
    return true;
  }, [id, load, setLoadError]);

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

  // When the user picks a different model, clear the reasoning selection
  // because the new model may not support the same variants.
  const handleModelChange = useCallback((model: string) => {
    setSelectedModel(model);
    setSelectedReasoning('');
  }, [setSelectedModel, setSelectedReasoning]);

  // Alt+J / Alt+K: navigate between recent sessions. Handlers read from refs
  // so they can capture the latest recentSessions without re-registering.
  const jumpToSession = useCallback((direction: 1 | -1) => {
    const sessions = recentSessionsRef.current;
    const currentIndex = sessions.findIndex((s) => s.id === id);
    if (currentIndex === -1) return;
    const target = sessions[currentIndex + direction];
    if (target) navigateToSession(target.id);
  }, [id, navigateToSession, recentSessionsRef]);

  // Keep the page-level refs that the palette dispatcher and shortcut handlers read.
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

  // Keyboard shortcuts: Alt+J/K (navigate), Alt+T (tmux), Alt+V
  // (VS Code), Alt+C (new session), Alt+M (model picker). Encapsulated
  // in useSessionShortcuts; the hook owns its own ref-mirrors so the
  // shortcut descriptors don't re-bind on every render.
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
  // Memoise the composer model list — a fresh Set + Array.from every
  // render produced a new array reference that invalidated downstream
  // memoised consumers (e.g. the composer's select options).
  const composerModels = useMemo(
    () => Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model))),
    [activeModel, session?.defaultModel, modelOptions],
  );
  const showSseNotice = portAvailable && !sseActive;
  const showSseDebug = debugMode && sseDebugEvents.length > 0;

  useEffect(() => {
    setAwaitingAssistantResponse(false);
  }, [id, setAwaitingAssistantResponse]);

  // The "awaiting first assistant response" flag is armed when the
  // user submits a prompt and cleared once the turn visibly advances
  // (assistant message arrives) or terminates. This bridges the gap
  // where the last visible message is still the user's optimistic
  // bubble, but the run is already queued/busy.
  useEffect(() => {
    if (lastMsg?.data?.role === 'assistant') {
      setAwaitingAssistantResponse(false);
      return;
    }
    if (session?.status === 'done' || session?.status === 'error') {
      setAwaitingAssistantResponse(false);
    }
  }, [lastMsg, session?.status, setAwaitingAssistantResponse]);

  const isRunning = isSessionRunning(lastMsg, session?.status, awaitingAssistantResponse);

  // Mirror the current session's status from SSE-driven message state into the
  // sidebar list entry so its status dot starts/stops pulsing immediately when
  // a turn begins or ends, without waiting for the 10-second poll to
  // /api/sessions. The derivation mirrors internal/db/types.go exactly so what
  // we set optimistically matches what the next poll will confirm (no flicker).
  // Optimistic status + live tokens-per-second live in
  // useSessionStatus. The hook owns the busy→waiting debounce timer
  // and the per-message TPS computation; the page just reads the
  // results and forwards them to the badge / sidebar mirror.
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
    // Write the SSE-derived status directly into the shared store so it
    // survives navigation and wins over any concurrent stale poll replace.
    patchRecentSession(id, { status: optimisticStatus });
  }, [id, optimisticStatus, patchRecentSession]);

  // Flattened list of sidebar rows for the "projects" view. Each entry is
  // either a project header (with its most-recent-activity timestamp) or a
  // session row belonging to a project. Groups are sorted by most-recent
  // activity descending; sessions within a group are sorted by timeUpdated
  // desc. We intentionally do NOT reorder based on which session is currently
  // viewed — users rely on the stable position of each project to build muscle
  // memory for "where does this session live". Collapsed groups omit their
  // child rows but still emit their header.
  //
  // We also roll up an "aggregate status" per group so a collapsed header
  // can surface the most important child state without the user having to
  // expand it. Priority matches what the user cares about when triaging:
  //   1. pending permission/question (needs you now)
  //   2. error (failed, still unseen)
  //   3. busy (running)
  //   4. waiting (completed, still unseen — the blue "unread" dot)
  //   5. none (idle/done — don't distract)
  // The waiting-and-unseen rung mirrors the per-row dot: a session that
  // finished but hasn't been opened yet keeps its blue indicator until
  // viewed, and the group header has to surface that or it lies about
  // child state.
  // For the currently-viewed session we prefer the SSE-derived
  // `optimisticStatus`, matching the per-row logic above, so the header
  // doesn't lag several seconds behind what the composer is showing.
  const sidebarProjectGroups = useMemo<SidebarProjectGroup[]>(() => {
    // Bucket sessions by *project root*, not raw cwd, so that worktrees
    // of the same repo live under one parent. projectRootForDirectory
    // folds <prefix>/.worktrees/<repo>/<slug> back to <prefix>/<repo>;
    // anything outside that layout passes through unchanged, so
    // unrelated projects keep their own groups.
    const buckets = new Map<string, typeof recentSessions>();
    for (const s of recentSessions) {
      const key = projectRootForDirectory(s.directory || '');
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    // Override the page session's recorded status with our optimistic
    // value so the sidebar dot starts/stops pulsing in lock-step with
    // the assistant's turn boundary, without waiting for the next poll.
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

    // Prepend a "Pinned" group if any sessions are pinned.
    const pinnedSessions = recentSessions
      .filter(s => s.pinned)
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
          {switching ? (
            // Blank paint frame between sessions — lets the fade-in animation
            // play against an empty viewport so navigation reads clearly.
            <div style={{ flex: 1, minHeight: 0 }} />
          ) : loading ? (
            <div className="oc-loading" data-testid="loading-spinner">
              <div className="oc-spinner" />
              Loading conversation...
            </div>
          ) : loadError ? (
            <div className="oc-error-banner" data-testid="error-banner" style={{ margin: 24 }}>
              {loadError}
              <button onClick={() => { setLoadError(null); load(abortControllerRef.current?.signal); }}>Retry</button>
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
              {/* AssistantThread is the most crash-prone region in the
                  page — it renders user-supplied markdown, code blocks via
                  highlight.js, and tool-call parts that arrive in real
                  time over SSE. Isolate it so a malformed message doesn't
                  blank the rest of the page (header, sidebars, recent
                  sessions list). resetKey on session.id clears any stale
                  crash when the user navigates to another session. */}
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
                  /* Composer/prompt slot has its own boundary so a crash
                     in one of these doesn't take the message thread down
                     with it (and vice versa). */
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
                    // Disable while a permission/question prompt is pending so
                    // Enter doesn't submit the draft as a new message. Normally
                    // the prompt replaces the composer in this slot entirely,
                    // but when the platform lacks respondPermission/Question
                    // capability or portAvailable is false the composer still
                    // renders — freezing input there is the cleanest guard.
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
                    setSession(prev => prev ? { ...prev, title: newTitle } : prev);
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
