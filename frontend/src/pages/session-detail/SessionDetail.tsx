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

import { useState, useEffect, useLayoutEffect, useCallback, useRef, useMemo } from 'react';
import { useSearchParams } from 'react-router-dom';
import { createPortal, flushSync } from 'react-dom';
import { useStickyNavigate } from '../../lib/useStickyNavigate';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api, sessionExportMarkdownUrl } from '../../lib/api';
import type { SessionWarning } from '../../lib/api';
import { cleanTitle, shortPath } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { canLaunchSession } from './launchGate';
import type { MessageBookmark } from '../../lib/messageBookmarks';
import { useMessageBookmarks } from './useMessageBookmarks';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { OcmanRuntimeProvider } from '../../components/OcmanRuntimeProvider';
import { AssistantThread } from '../../components/AssistantThread';
import { ShareLinkModal } from '../../components/ShareExportMenu';
import { Composer } from '../../components/assistant/Composer';
import { QuestionPrompt } from '../../components/session/QuestionPrompt';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { RightPanel } from '../../components/RightPanel';
import { SessionTerminalDock } from '../../components/SessionTerminalDock';
import { ErrorBoundary, type FallbackRender } from '../../components/ErrorBoundary';
import { RateLimitBanner } from '../../components/RateLimitBanner';
import { PermissionModeLock } from '../../components/PermissionModeLock';
import { SessionWarningBanner } from '../../components/SessionWarningBanner';
import { useUiStore } from '../../lib/uiStore';
import { useTmux } from '../../lib/useTmux';
import { useApiStore } from '../../lib/apiStore';
import { useGitInfo } from '../../lib/useGitInfo';
import { usePlatformCapabilities, useWorktreeSessions } from '../../lib/useCapabilities';
import { listFailedSends, type FailedSend } from '../../lib/failedSends';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { recheckFaviconNotify } from '../../lib/useFaviconNotify';
import { createSessionWithLaunch } from '../../lib/createSessionWithLaunch';
import {
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  deriveActiveModelAndAgent,
  agentModelRef,
} from '../../lib/sessionStatus';
import { computeTurnStats, latestTurnModel } from '../../lib/turnStats';
import { rollupGroupStatus } from '../../lib/sidebarHelpers';
import {
  extractPendingPermission,
  extractPendingQuestion,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
} from '../../lib/sseHelpers';
import { isSessionRelevant, mcpChildIdsOf } from '../../lib/promptRouting';
import { useProjects } from '../../lib/queries';
import { useSubagentTracking } from './useSubagentTracking';
import { useTmuxActions } from './useTmuxActions';
import { useSessionStatus } from './useSessionStatus';
import { useSidebarSessions } from './useSidebarSessions';
import { useSessionCapabilities } from './useSessionCapabilities';
import {
  usePromptHandlers,
  storePendingQuestion,
  loadPendingQuestion,
  clearPendingQuestion,
} from './usePromptHandlers';
import { useSessionShortcuts } from './useSessionShortcuts';
import { usePaletteCommands } from './usePaletteCommands';
import { SseStatusIndicator } from './SseStatusIndicator';
import { remoteLog } from '../../lib/remoteLog';
import { isRecoverableThreadBoundaryError } from './threadBoundaryRecovery';
import { findFirstUnreadMessageId, countUnreadMessages } from './unreadMarker';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';
import { SessionToasts } from './SessionToasts';
import { SessionSidebar, type SidebarProjectGroup } from './SessionSidebar';
import { RenameModal } from './RenameModal';
import { ForkPicker } from './ForkPicker';
import { MovePathDialog, MovePicker } from './MovePicker';
import { useSessionActions } from './useSessionActions';
import { useMessageQueue } from '../../lib/useMessageQueue';
import { useSession } from './useSession';
import { usePendingSend, materializePending } from './usePendingSend';
import { useAutoApprove } from '../../lib/useAutoApprove';
import { ThreadSkeleton } from '../../components/Skeleton';

/**
 * Portal helper: mounts its children into the `#header-actions-slot`
 * div rendered by the top-level `<Header>` (see App.tsx). The slot
 * lives under the project name in the page header; rendering here
 * keeps the action strip (tmux / launch / VS Code / new session)
 * stacked under the project label instead of floating over the
 * conversation thread.
 *
 * Subscribes to the external DOM (the slot element lives outside
 * this component's subtree). `useLayoutEffect` runs after `<Header>`
 * has committed its DOM but before paint, so the buttons appear on
 * the first frame without flicker.
 */
function HeaderActionsPortal({ children }: { children: React.ReactNode }) {
  const [target, setTarget] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing with an external DOM node owned by <Header />; documented as a legitimate use of setState-in-effect.
    setTarget(document.getElementById('header-actions-slot'));
  }, []);
  if (!target) return null;
  return createPortal(children, target);
}

/** Memory bound on the in-memory message list. */
const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;
const THREAD_BOUNDARY_AUTO_RECOVERY_COOLDOWN_MS = 5_000;

function sessionWarningKey(sessionId: string, warning: SessionWarning): string {
  return `${sessionId}:${warning.kind}:${warning.message}:${(warning.ports ?? []).join(',')}`;
}

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
    messages: rawMessages,
    parts: rawParts,
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

  // Snapshot of the user's last-seen cutoff for the current session.
  // Used to compute the "first unread" marker and the "N new
  // messages" jump pill. Captured once per session id; subsequent
  // updates (markSessionSeen, SSE) do NOT move the cutoff so the
  // marker stays at the same message even after the persisted seen
  // state has been bumped forward. Resets on session navigation.
  //
  // Stored as state (not a ref) so the eslint react-hooks/refs rule
  // doesn't trip on render-time reads. The state initialisation
  // tracks the active session id alongside the cutoff so we can
  // detect navigation without an effect (the setState-during-render
  // pattern React supports for derived state).
  const [unreadCutoffState, setUnreadCutoffState] = useState<{ sessionId: string; cutoff: number } | null>(null);
  if (session && unreadCutoffState?.sessionId !== session.id) {
    setUnreadCutoffState({
      sessionId: session.id,
      cutoff: session.seenTimeUpdated || 0,
    });
  }
  const unreadCutoff = unreadCutoffState?.sessionId === session?.id
    ? (unreadCutoffState?.cutoff ?? 0)
    : 0;
  const firstUnreadMessageId = useMemo(
    () => session ? findFirstUnreadMessageId(messages, unreadCutoff) : null,
    [session, messages, unreadCutoff],
  );
  const unreadMessageCount = useMemo(
    () => firstUnreadMessageId ? countUnreadMessages(messages, unreadCutoff) : 0,
    [messages, firstUnreadMessageId, unreadCutoff],
  );
  const [dismissedSessionWarnings, setDismissedSessionWarnings] = useState<Set<string>>(() => new Set());
  const visibleSessionWarnings = useMemo(() => {
    if (!session) return [];
    return (session.warnings ?? []).filter((warning) => (
      !dismissedSessionWarnings.has(sessionWarningKey(session.id, warning))
    ));
  }, [session, dismissedSessionWarnings]);
  const dismissSessionWarning = useCallback((warning: SessionWarning) => {
    if (!session) return;
    const key = sessionWarningKey(session.id, warning);
    setDismissedSessionWarnings((current) => {
      if (current.has(key)) return current;
      const next = new Set(current);
      next.add(key);
      return next;
    });
  }, [session]);

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
  const worktreesSupported = useWorktreeSessions();

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
  const projectOrder = useUiStore((state) => state.projectOrder);
  const setProjectOrder = useUiStore((state) => state.setProjectOrder);
  // All known projects — the sidebar "projects" view lists every
  // unarchived project, even ones with no session in the recent window.
  const projectsQuery = useProjects();
  const allProjects = projectsQuery.data;
  const archiveProject = useApiStore((state) => state.archiveProject);
  // Optimistically-archived project roots: hides the group immediately
  // while /api/projects refetches (project-archive state isn't carried
  // on the session payloads driving the sidebar).
  const [archivedProjectRoots, setArchivedProjectRoots] = useState<Set<string>>(() => new Set());
  const patchRecentSession = useApiStore((state) => state.patchRecentSession);
  const abortControllerRef = useRef<AbortController | null>(null);
  const resetSessionIdRef = useRef<string | undefined>(undefined);
  const modelSeededSessionRef = useRef<string | undefined>(undefined);
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
  const { infos: siblingGitInfos } = useGitInfo(
    recentSessions.map((s) => s.directory).filter(Boolean),
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
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [copyToastMessage, setCopyToastMessage] = useState<string | null>(null);
  const [threadBoundaryResetNonce, setThreadBoundaryResetNonce] = useState(0);
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
      modelSeededSessionRef.current = undefined;
      setSelectedModel('');
      setSelectedAgent('');
      setSelectedReasoning('');
      setFailedSends(id ? listFailedSends(id) : []);
    }

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

  // Reverse sync: when the session REST response or the sidebar poll
  // reports a pending prompt we don't yet have in state, fetch the
  // full detail so the dialog appears.
  //
  // Two sources signal "a prompt exists":
  //   1. session.pendingPermission / session.pendingQuestion (boolean)
  //      from the initial /api/session/{id} fetch — fires immediately on
  //      page load or session switch, without waiting for a sidebar poll.
  //   2. sidebarHasPerm / sidebarHasQuestion from the /api/sessions poll
  //      — catches prompts that arrive while the SSE stream is open but
  //      the permission.asked event was somehow missed.
  const sidebarCurrentSession = recentSessions.find((s) => s.id === id);
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;
  const restHasPerm = session?.pendingPermission ?? false;
  const restHasQuestion = session?.pendingQuestion ?? false;
  useEffect(() => {
    if (!id) return;
    // Relevance set = Task-tool subagents (from parts) ∪ ocman
    // MCP/worktree children (from the session list's parentID overlay).
    // The latter covers children that carry no OpenCode parent_id and
    // are never referenced by the parent's parts (#268 regression).
    const relevantIds = new Set(subagentSessionIdsRef.current);
    for (const childId of mcpChildIdsOf(id, recentSessionsRef.current)) {
      relevantIds.add(childId);
    }
    if ((restHasPerm || sidebarHasPerm) && pendingPermission === null) {
      listPermissions(id)
        .then((perms) => {
          for (const raw of perms) {
            const p = raw as Record<string, unknown>;
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            const promptSid = typeof p.sessionID === 'string' ? p.sessionID : '';
            if (!isSessionRelevant(promptSid, id, relevantIds)) continue;
            setPendingPermission(perm);
            setPermissionError(null);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
    if ((restHasQuestion || sidebarHasQuestion) && pendingQuestion === null) {
      listQuestions(id)
        .then((questions) => {
          for (const raw of questions) {
            const q = raw as Record<string, unknown>;
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            const questionSid = typeof q.sessionID === 'string' ? q.sessionID : '';
            if (!isSessionRelevant(questionSid, id, relevantIds)) continue;
            storePendingQuestion(id, question);
            setPendingQuestion(question);
            break;
          }
        })
        .catch(() => { /* sidebar will retry */ });
    }
  }, [id, restHasPerm, restHasQuestion, sidebarHasPerm, sidebarHasQuestion,
    pendingPermission, pendingQuestion,
    listPermissions, listQuestions, setPermissionError, setPendingPermission,
    setPendingQuestion, subagentSessionIdsRef, recentSessionsRef]);

  // Poll-driven dismissal fallback. When a question is answered
  // outside ocman (e.g. directly in the OpenCode CLI), OpenCode does
  // not reliably emit a `question.replied` SSE event, so the reducer
  // never clears the prompt and it stays on screen indefinitely.
  //
  // We can't trust the sidebar's pendingQuestion flag here: the
  // sidebar poll merges with `live.pendingQuestion || server` (see
  // useSidebarSessions), so once SSE sets it true it never flips back
  // to false. Instead, poll OpenCode's authoritative live `/question`
  // list directly: when the currently-pending requestId is no longer
  // in it, the question has been answered/cancelled somewhere and the
  // prompt must come down. Matching on the requestId (rather than an
  // empty list) avoids dismissing a freshly-asked follow-up question.
  const pendingQuestionRequestId = pendingQuestion?.requestId ?? null;
  useEffect(() => {
    if (!id || !pendingQuestionRequestId || !portAvailable) return;
    let cancelled = false;

    const check = () => {
      listQuestions(id)
        .then((questions) => {
          if (cancelled) return;
          const stillPending = questions.some((raw) => {
            const q = extractPendingQuestion({ type: 'question.asked', properties: raw as Record<string, unknown> });
            return q?.requestId === pendingQuestionRequestId;
          });
          if (stillPending) return;
          setPendingQuestion(null);
          clearPendingQuestion(id);
        })
        .catch(() => { /* leave the prompt up; the next tick retries */ });
    };

    check();
    const timer = window.setInterval(() => {
      if (!document.hidden) check();
    }, 3000);
    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [id, pendingQuestionRequestId, portAvailable, listQuestions, setPendingQuestion]);

  // Mark session as seen on entry. Opening a session also unarchives it
  // server-side (handleSession), so optimistically clear the archived flag
  // in the sidebar row too — otherwise the row stays hidden/greyed until
  // the next /api/sessions poll catches up.
  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;
  useEffect(() => {
    if (!sessionSeenId || !sessionSeenPlatform) return;
    patchSession({ seen: true, archived: false });
    patchRecentSession(sessionSeenId, { seen: true, archived: false });
    void markSessionSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated)
      .then(() => {
        recheckFaviconNotify();
      })
      .catch((err) => remoteLog.error('Failed to mark session seen', err));
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
      sessionId: s.id,
      sessionTitle: cleanTitle(s.title) || 'Untitled',
      sessionPlatform: s.platform,
      sessionProject: shortPath(s.directory),
      sessionProjectFull: s.directory,
      sessionRemoteId: s.remoteId,
      sessionRemoteName: s.remoteName,
      sessionRemoteStale: s.stale,
    });
    return () => setInfo({});
  }, [session, setInfo]);

  // The model the session is currently on, used to pre-seed the
  // composer when no explicit selection has been made yet. Prefer the
  // model behind the most recent turn (what OpenCode will keep using),
  // falling back to the session's default model.
  const turnStatsMap = useMemo(() => computeTurnStats(messages, parts), [messages, parts]);
  const { activeAgent } = useMemo(
    () => deriveActiveModelAndAgent(messages, session),
    [messages, session],
  );
  const activeModel = useMemo(
    () =>
      latestTurnModel(messages, turnStatsMap) ||
      getProjectModel(session?.directory || '') ||
      session?.defaultModel ||
      '',
    [messages, turnStatsMap, session?.directory, session?.defaultModel],
  );

  // Pre-seed the composer's model with the session's current model on
  // session open. The composer's `selectedModel` is the single source
  // of truth for the next message; seed exactly once per session so
  // later assistant responses cannot move the composer selection.
  useEffect(() => {
    if (!id) return;
    if (modelSeededSessionRef.current === id) return;
    if (!activeModel) return;
    setSelectedModel(activeModel);
    modelSeededSessionRef.current = id;
  }, [activeModel, id, setSelectedModel]);

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
        seedNewSession(res.id, sessionDirectory, targetPlatform ?? '', title);
        navigateToSession(res.id);
      }
    } catch (e) {
      remoteLog.error('Failed to create session', e);
      setShowCreateSessionErrorToast(true);
    }
  }, [createSession, launchOpencodeInTmux, tmux.available, navigateToSession, seedNewSession, session]);

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
  const { queue: queuedMessages, remove: removeQueuedMessage, move: moveQueuedMessage } =
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
    setCopyToastMessage,
  });

  const handleMoveDestination = useCallback((directory: string) => {
    if (!session) return;
    pending.begin(`/move ${directory}`);
    api.moveSession(session.id, directory)
      .then(() => {
        pending.clear();
        patchSession({ directory });
        patchRecentSession(session.id, { directory });
      })
      .catch((error) => {
        remoteLog.error('Failed to move session', error);
        pending.fail(error instanceof Error ? error.message : 'Unknown error');
      });
  }, [patchRecentSession, patchSession, pending, session]);

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
    modelSeededSessionRef.current = id;
    setSelectedModel(model);
    setSelectedReasoning('');
    if (session?.directory) saveProjectModel(session.directory, model);
  }, [id, session?.directory, setSelectedModel, setSelectedReasoning]);

  // Switching to an agent that defines a model selects that model in
  // the composer (and thus respects it on send). A later manual model
  // change overrides it; switching agents again re-applies the new
  // agent's model. Agents without a model leave the selection as-is.
  const handleAgentChange = useCallback((agent: string) => {
    modelSeededSessionRef.current = id;
    setSelectedAgent(agent);
    const info = agents.find((a) => a.name === agent);
    const agentModel = agentModelRef(info);
    if (agentModel) {
      setSelectedModel(agentModel);
      setSelectedReasoning('');
    }
  }, [agents, id, setSelectedAgent, setSelectedModel, setSelectedReasoning]);

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
  const permissionControl = useMemo(
    () => (caps.permissionRules && portAvailable && session?.id ? <PermissionModeLock sessionId={session.id} /> : null),
    [caps.permissionRules, portAvailable, session?.id],
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

  // Flag for the composer's "launch session" button.
  const launchHintActive = canLaunchSession({
    portAvailable,
    hasPendingPrompt,
    tmuxAvailable: tmux.available,
    liveConnectionHint: !!caps.liveConnectionHint,
    directory: session?.directory,
  });

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
      const remote = sorted.find((s) => s.remoteId && s.remoteId !== 'local');
      return {
        directory,
        sessions: sorted,
        lastUpdated: sorted[0]?.timeUpdated ?? 0,
        aggregate: rollup(sorted),
        remoteId: remote?.remoteId,
        remoteName: remote?.remoteName,
        platform: remote?.platform,
      };
    });

    // Drop groups for projects the user just archived (optimistic, before
    // the /api/projects refetch lands) — applies to session-bearing groups
    // too, since session payloads don't carry project-archive state.
    const visibleGroups = archivedProjectRoots.size === 0
      ? groups
      : groups.filter((g) => !archivedProjectRoots.has(g.directory));

    // Add empty groups for known unarchived projects that have no
    // session in the recent poll window, so the projects view lists
    // every active project. Archived projects (incl. server-side
    // auto-archived stale ones) stay hidden.
    for (const p of allProjects ?? []) {
      if (p.archived) continue;
      const root = projectRootForDirectory(p.directory);
      if (buckets.has(root) || archivedProjectRoots.has(root)) continue;
      buckets.set(root, []);
      visibleGroups.push({
        directory: root,
        sessions: [],
        lastUpdated: p.lastUsed,
        aggregate: rollup([]),
        remoteId: p.remoteId,
        remoteName: p.remoteName,
        platform: p.platform,
      });
    }
    // Sort project groups alphabetically by their short display path
    // (no longer by activity), then apply the user's saved manual
    // drag-and-drop order: directories present in projectOrder come
    // first (in that order); any project not yet ordered (new or never
    // dragged) keeps its alphabetical position at the end.
    visibleGroups.sort((a, b) =>
      shortPath(a.directory).localeCompare(shortPath(b.directory), undefined, {
        sensitivity: 'base',
      }),
    );
    if (projectOrder.length > 0) {
      const rank = new Map(projectOrder.map((dir, i) => [dir, i]));
      visibleGroups.sort((a, b) => {
        const ra = rank.get(a.directory);
        const rb = rank.get(b.directory);
        if (ra === undefined && rb === undefined) return 0; // keep alphabetical
        if (ra === undefined) return 1; // unordered after ordered
        if (rb === undefined) return -1;
        return ra - rb;
      });
    }

    const pinnedSessions = recentSessions
      .filter((s) => s.pinned)
      .sort((a, b) => b.pinnedAt - a.pinnedAt);
    if (pinnedSessions.length > 0) {
      visibleGroups.unshift({
        directory: '__pinned__',
        sessions: pinnedSessions,
        lastUpdated: pinnedSessions[0]?.timeUpdated ?? 0,
        aggregate: rollup(pinnedSessions),
        isPinned: true,
      });
    }

    return visibleGroups;
  }, [recentSessions, id, optimisticStatus, projectOrder, allProjects, archivedProjectRoots]);

  // Persist a new drag-and-drop order of the (non-pinned) project
  // groups. The synthetic "__pinned__" group is excluded — it always
  // stays at the top regardless of the saved order.
  const handleReorderProjects = useCallback(
    (orderedDirectories: string[]) => {
      setProjectOrder(orderedDirectories.filter((d) => d && d !== '__pinned__'));
    },
    [setProjectOrder],
  );

  // Archive a project from the sidebar: hide its group immediately, then
  // persist + refetch /api/projects. Revert the optimistic hide on error.
  const handleArchiveProjectFromSidebar = useCallback(
    (directory: string) => {
      const root = projectRootForDirectory(directory);
      if (!root) return;
      setArchivedProjectRoots((prev) => new Set(prev).add(root));
      archiveProject(root, true)
        .then(() => projectsQuery.refetch())
        .catch((err) => {
          remoteLog.error('Failed to archive project', err);
          setArchivedProjectRoots((prev) => {
            const next = new Set(prev);
            next.delete(root);
            return next;
          });
        });
    },
    [archiveProject, projectsQuery],
  );

  return (
    <Toast.Provider swipeDirection="right">
      <div className="session-layout" data-testid="session-layout">
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
          onArchiveProject={handleArchiveProjectFromSidebar}
        />
        <div className="session-main">
          {session && <HeaderActionsPortal>
            <details className="oc-project-menu header-actions-menu">
              <summary
                className="oc-project-menu-trigger"
                title="Session actions"
                aria-label="Session actions"
              >⋯</summary>
              <div className="oc-project-menu-list" role="menu">
                <button
                  type="button"
                  role="menuitem"
                  className="oc-project-menu-item"
                  onClick={(e) => {
                    (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                    void handleNewSession();
                  }}
                  title="New session"
                >New session</button>

                <div className="oc-project-menu-separator" role="separator" />

                <a
                  role="menuitem"
                  className="oc-project-menu-item"
                  href={sessionExportMarkdownUrl(session.id)}
                  download={`conversation-${session.id}.md`}
                  onClick={(e) => {
                    (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                  }}
                >Download Markdown</a>
                <button
                  type="button"
                  role="menuitem"
                  className="oc-project-menu-item"
                  onClick={(e) => {
                    (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                    // Defer so the menu unmounts before print snapshots the page.
                    window.setTimeout(() => window.print(), 50);
                  }}
                >Print / Save as PDF</button>
                <button
                  type="button"
                  role="menuitem"
                  className="oc-project-menu-item"
                  onClick={(e) => {
                    (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                    setShowShareModal(true);
                  }}
                >Share link…</button>

                <div className="oc-project-menu-separator" role="separator" />

                {tmux.available && matchingTmuxSession && (
                  <button
                    type="button"
                    role="menuitem"
                    className="oc-project-menu-item"
                    onClick={(e) => {
                      (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                      handleTmuxSwitch(e, matchingTmuxSession.name);
                    }}
                    title={`Switch tmux to ${shortPath(matchingTmuxSession.name)} (T)`}
                  >Switch tmux</button>
                )}
                {tmux.available && !portAvailable && caps.liveConnectionHint && (
                  <button
                    type="button"
                    role="menuitem"
                    className="oc-project-menu-item"
                    onClick={(e) => {
                      (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                      void handleLaunchOpencode();
                    }}
                    disabled={launchingOpencode}
                    title="Launch opencode --port 0 in a new tmux window"
                  >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</button>
                )}
                <button
                  type="button"
                  role="menuitem"
                  className="oc-project-menu-item"
                  onClick={(e) => {
                    (e.currentTarget.closest('details') as HTMLDetailsElement | null)?.removeAttribute('open');
                    handleVSCodeShortcut();
                  }}
                  title="Open in VS Code (V)"
                >Open in VS Code</button>
              </div>
            </details>
          </HeaderActionsPortal>}
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
                      {visibleSessionWarnings.map((warning) => (
                        <SessionWarningBanner
                          key={sessionWarningKey(session.id, warning)}
                          warning={warning}
                          onDismiss={() => dismissSessionWarning(warning)}
                        />
                      ))}
                      {session.notice && (
                        <RateLimitBanner notice={session.notice} />
                      )}
                      {pendingPermission && portAvailable && caps.respondPermission ? (
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
                          onSend={handleSend}
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
                          sessionId={session?.id}
                          tokensPerSecond={liveTokensPerSecond ?? undefined}
                          tokenStats={tokenStats}
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
              {showRenameModal && session && (
                <RenameModal
                  sessionId={session.id}
                  initialTitle={session.title || ''}
                  onClose={() => setShowRenameModal(false)}
                  onRenamed={(newTitle) => {
                    patchSession({ title: newTitle });
                    patchRecentSession(session.id, { title: newTitle });
                    setShowRenameToast(true);
                  }}
                />
              )}
              {showForkPicker && session && (
                <ForkPicker
                  open
                  messages={messages}
                  parts={parts}
                  onClose={() => setShowForkPicker(false)}
                  onSelect={(messageID) => {
                    setShowForkPicker(false);
                    pending.begin('/fork');
                    api.forkSession(session.id, messageID)
                      .then(({ id: forkedID }) => {
                        pending.clear();
                        navigateToSession(forkedID);
                      })
                      .catch((error) => {
                        remoteLog.error('Failed to fork session', error);
                        pending.fail(error instanceof Error ? error.message : 'Unknown error');
                      });
                  }}
                />
              )}
              {showMovePicker && session && (
                <MovePicker
                  open
                  currentDirectory={session.directory}
                  directories={[
                    ...(allProjects ?? [])
                      .filter((project) => (project.remoteId || 'local') === (session.remoteId || 'local'))
                      .map((project) => project.directory),
                    ...recentSessions
                      .filter((recent) => (recent.remoteId || 'local') === (session.remoteId || 'local'))
                      .map((recent) => recent.directory),
                  ]}
                  onClose={() => setShowMovePicker(false)}
                  onCustom={() => setShowMovePathDialog(true)}
                  onSelect={(directory) => {
                    setShowMovePicker(false);
                    handleMoveDestination(directory);
                  }}
                />
              )}
              {showMovePathDialog && session && (
                <MovePathDialog
                  onClose={() => setShowMovePathDialog(false)}
                  onSelect={(directory) => {
                    setShowMovePathDialog(false);
                    handleMoveDestination(directory);
                  }}
                />
              )}
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
