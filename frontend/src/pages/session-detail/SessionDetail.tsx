import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api, type Session, type Message, type Part, type SessionDetail } from '../../lib/api';
import { cleanTitle, shortPath, relativeTime } from '../../lib/format';
import { projectRootForDirectory } from '../../lib/worktrees';
import { useHeaderInfo, usePageTitle } from '../../lib/headerContext';
import { OcmanRuntimeProvider } from '../../components/OcmanRuntimeProvider';
import { AssistantThread } from '../../components/AssistantThread';
import { Composer, type AttachedImage } from '../../components/assistant/Composer';
import { QuestionPrompt, type PendingQuestion } from '../../components/session/QuestionPrompt';
import { PermissionPrompt } from '../../components/session/PermissionPrompt';
import { StatusBadge } from '../../components/StatusBadge';
import { PlatformBadge } from '../../components/PlatformBadge';
import { ShortPath, GitStatusLine } from '../../components/SessionTable';
import { BackendStats } from '../../components/BackendStats';
import { SidebarResizer } from '../../components/SidebarResizer';
import { RightPanel } from '../../components/RightPanel';
import { ErrorBoundary } from '../../components/ErrorBoundary';
import { RateLimitBanner } from '../../components/RateLimitBanner';
import { useUiStore } from '../../lib/uiStore';
import { useTmux } from '../../lib/useTmux';
import { useApiStore } from '../../lib/apiStore';
import { useGitInfo } from '../../lib/useGitInfo';
import { usePlatformCapabilities } from '../../lib/useCapabilities';
import { recheckFaviconNotify } from '../../lib/useFaviconNotify';
import { openVSCode } from '../../lib/shortcuts';
import { hashSession, hashMessagesAndParts } from '../../lib/sessionHash';
import { createSessionWithLaunch, type LaunchStatus } from '../../lib/createSessionWithLaunch';
import { isSessionRelevant } from '../../lib/promptRouting';
import {
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  deriveActiveModelAndAgent,
} from '../../lib/sessionStatus';
import { computeSidebarHash, rollupGroupStatus } from '../../lib/sidebarHelpers';
import {
  extractPendingPermission,
  extractPendingQuestion,
  extractPendingQuestionFromParts,
  hasPendingQuestionInParts,
  type PendingPermission,
} from '../../lib/sseHelpers';
import {
  listFailedSends,
  recordFailedSend,
  removeFailedSend,
  type FailedSend,
} from '../../lib/failedSends';
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

const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;



function ArchiveIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 3.5h12v2H2zm1 3h10v6H3zm3 2.5h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function ArchiveFilterIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2.5 3h11l-4.25 4.9v3.35l-2.5 1.25V7.9z" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// Icon used for the "projects" sidebar-view toggle. Stack of horizontal bars
// evokes a grouped-list.
function ProjectsViewIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2" y="3" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="4" y="7" width="10" height="2.2" rx="0.6" fill="currentColor" opacity="0.75" />
      <rect x="4" y="11" width="10" height="2.2" rx="0.6" fill="currentColor" opacity="0.75" />
    </svg>
  );
}

// Icon used when the sidebar is *in* projects view — shows a flat list, hinting
// that clicking will return to the flat "recent" view.
function RecentViewIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2" y="3" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="2" y="7" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="2" y="11" width="12" height="2.2" rx="0.6" fill="currentColor" />
    </svg>
  );
}

export function SessionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
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
  // collapsed-projects fold-out. The hook owns recentSessions; the
  // page-level cross-cutting effects (status mirror, permission
  // mirror, seen mirror, SSE-derived sidebar updates) write through
  // the exposed setRecentSessions and lastSiblingsHashRef.
  const collapsedProjects = useUiStore((state) => state.collapsedProjects);
  const {
    recentSessions,
    setRecentSessions,
    recentSessionsRef,
    loadingRecentSessions,
    archivingSessionIds,
    showArchivedRecent,
    setShowArchivedRecent,
    showArchivedRecentRef,
    lastSiblingsHashRef,
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
  const [renameTitle, setRenameTitle] = useState('');
  const [showRenameToast, setShowRenameToast] = useState(false);
  const [showCreateSessionErrorToast, setShowCreateSessionErrorToast] = useState(false);
  const [showDisconnectedToast, setShowDisconnectedToast] = useState(false);
  const [createLaunchStatus, setCreateLaunchStatus] = useState<LaunchStatus>('idle');
  // Sends that failed on the client (network error, 5xx, etc.). Each entry
  // is keyed by the optimistic message id and holds enough context to
  // replay the send on Retry. Persisted via lib/failedSends so the user
  // can refresh the page without losing their prompt.
  const [failedSends, setFailedSends] = useState<FailedSend[]>([]);
  const archiveSession = useApiStore((state) => state.archiveSession);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const sendMessage = useApiStore((state) => state.sendMessage);
  const listPermissions = useApiStore((state) => state.listPermissions);
  const listQuestions = useApiStore((state) => state.listQuestions);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const updateCachedSession = useApiStore((state) => state.updateCachedSession);
  const sidebarWidth = useUiStore((state) => state.sidebarWidth);
  const sidebarView = useUiStore((state) => state.sidebarView);
  const toggleSidebarView = useUiStore((state) => state.toggleSidebarView);
  const toggleCollapsedProject = useUiStore((state) => state.toggleCollapsedProject);

  // Refs for values used by the scoped-command dispatch so the effect
  // only re-runs when `paletteCommand` changes (fixes the P0 hot loop).
  const tmuxRef = useRef(tmux);
  useEffect(() => { tmuxRef.current = tmux; }, [tmux]);
  const archiveSessionRef = useRef(archiveSession);
  useEffect(() => { archiveSessionRef.current = archiveSession; }, [archiveSession]);
  const navigateRef = useRef(navigate);
  useEffect(() => { navigateRef.current = navigate; }, [navigate]);

  const paletteCommand = useUiStore((s) => s.paletteCommand);
  useEffect(() => {
    if (!paletteCommand || paletteCommand.kind !== 'scoped') return;
    useUiStore.getState().closePalette();

    const el = document.querySelector('.oc-composer-input') as HTMLTextAreaElement | null;
    if (!el) return;

    const cmd = paletteCommand;
    const t = tmuxRef.current;
    if (cmd.id === 'scoped.model') {
      el.value = '/model ';
      el.dispatchEvent(new CustomEvent('oc-model-picker-open', { detail: '' }));
      el.focus();
    } else if (cmd.id === 'scoped.agent') {
      el.value = '/agent ';
      el.dispatchEvent(new CustomEvent('oc-agent-picker-open', { detail: '' }));
      el.focus();
    } else if (cmd.id === 'scoped.variant') {
      setSelectedReasoning('');
    } else if (cmd.id === 'scoped.tmux' && t.available && t.sessions.length > 0) {
      t.switchSession(t.sessions[0].name).catch(console.error);
    } else if (cmd.id === 'scoped.vscode' && sessionRef.current) {
      openVSCode(sessionRef.current.directory);
    } else if (cmd.id === 'scoped.archive' && sessionRef.current) {
      const s = sessionRef.current;
      archiveSessionRef.current(s.platform, s.id, s.timeUpdated, true).then(() => navigateRef.current(-1));
    } else if (cmd.id === 'scoped.rename' && sessionRef.current) {
      setShowRenameModal(true);
    } else if (cmd.id === 'scoped.new-project') {
      useUiStore.getState().openProjectPalette();
    } else if (cmd.id === 'scoped.compact' && sessionRef.current && portAvailableRef.current && capsRef.current.compact) {
      const s = sessionRef.current;
      const model = selectedModelRef.current || activeModelRef.current || '';
      const slashIdx = model.indexOf('/');
      const providerID = slashIdx > 0 ? model.slice(0, slashIdx) : '';
      const modelID = slashIdx > 0 ? model.slice(slashIdx + 1) : model;
      api.compactSession(s.id, providerID, modelID).catch(console.error);
    }
  }, [paletteCommand]);


  useEffect(() => {
    showArchivedRecentRef.current = showArchivedRecent;
  }, [showArchivedRecent]);

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
    injectedGhostIdsRef.current = new Set();
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
  }, [getWhisperStatus, id, load, refreshModels]);

  // Re-inject ghost user-message bubbles for failed sends that survived a
  // refresh. The optimistic messages are component-local (never written to
  // the DB), so on cold load they're absent from `messages` even though
  // the persisted entry is back in `failedSends`. Skip entries whose text
  // already appears as a real user message in the loaded thread — that
  // means the request actually reached the server and SSE delivered it,
  // so the failed banner would be a confusing duplicate.
  //
  // Guard: track which ghost IDs we've already injected so the effect
  // is idempotent even when `messages` / `parts` change (which they do
  // as a result of the injection itself). Without this guard the effect
  // can cascade: inject → setMessages → effect re-fires → inject again
  // if the timing races with SSE or the memory-trimming effect.
  //
  // IMPORTANT: `messages` and `parts` are read via refs (messagesRef,
  // partsRef) instead of being listed as dependencies. This breaks the
  // cascade where ghost injection appends to `messages`, the memory-
  // trimming effect trims `messages`, and the changed `messages` re-
  // triggers ghost injection — an infinite loop that hits React's
  // maximum update depth. The effect only needs to fire when `session`
  // or `failedSends` change; the current messages/parts are consulted
  // for deduplication but should not trigger re-runs.
  const injectedGhostIdsRef = useRef<Set<string>>(new Set());
  useEffect(() => {
    if (!session || failedSends.length === 0) return;
    const currentMessages = messagesRef.current;
    const currentParts = partsRef.current;
    const existingIds = new Set(currentMessages.map(m => m.id));
    const realUserTexts = new Set(
      currentMessages
        .filter(m => m.data?.role === 'user')
        .flatMap(m => currentParts
          .filter(p => p.messageId === m.id)
          .map(p => {
            try {
              const pd = typeof p.data === 'string' ? JSON.parse(p.data) : p.data;
              return pd?.type === 'text' ? (pd.text || '') : '';
            } catch {
              return '';
            }
          })
          .filter(Boolean)),
    );
    const ghostsToInject = failedSends.filter(e => {
      if (existingIds.has(e.id)) return false;
      if (injectedGhostIdsRef.current.has(e.id)) return false;
      if (e.text && realUserTexts.has(e.text)) return false;
      return true;
    });
    if (ghostsToInject.length === 0) return;

    const newMsgs: Message[] = [];
    const newParts: Part[] = [];
    for (const entry of ghostsToInject) {
      injectedGhostIdsRef.current.add(entry.id);
      newMsgs.push({
        id: entry.id,
        sessionId: session.id,
        timeCreated: entry.failedAt,
        data: { role: 'user' },
      });
      if (entry.text) {
        newParts.push({
          id: 'part-' + entry.id,
          messageId: entry.id,
          sessionId: session.id,
          data: { type: 'text', text: entry.text } as unknown as string,
        });
      }
      if (entry.images) {
        entry.images.forEach((img, i) => {
          newParts.push({
            id: `part-${entry.id}-img-${i}`,
            messageId: entry.id,
            sessionId: session.id,
            data: { type: 'file', mime: img.mime, url: img.url } as unknown as string,
          });
        });
      }
    }
    setMessages(prev => [...prev, ...newMsgs]);
    setParts(prev => [...prev, ...newParts]);
    // Drop the rehydrated entries that were filtered out (request actually
    // succeeded on the server) so the persistent store stays clean.
    const droppedIds = failedSends
      .filter(e => !ghostsToInject.includes(e) && !existingIds.has(e.id))
      .map(e => e.id);
    if (droppedIds.length > 0) {
      setFailedSends(prev => prev.filter(e => !droppedIds.includes(e.id)));
      droppedIds.forEach(idToDrop => removeFailedSend(session.id, idToDrop));
    }
  // eslint-disable-next-line react-hooks/exhaustive-deps -- messagesRef/partsRef are stable refs; listing messages/parts here would create an infinite loop with the memory-trimming effect.
  }, [session, failedSends]);

  useEffect(() => {
    if (messages.length <= MAX_RETAINED_MESSAGES) return;

    const retainedMessages = messages.slice(-TRIMMED_RETAINED_MESSAGES);
    const droppedCount = messages.length - retainedMessages.length;
    const retainedMessageIds = new Set(retainedMessages.map((message) => message.id));
    droppedMessageCountRef.current += droppedCount;

    setMessages(retainedMessages);
    setParts((prev) => prev.filter((part) => retainedMessageIds.has(part.messageId)));
  }, [messages]);

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

  // Mirror the current session's pending-prompt state from SSE into the
  // sidebar list entry so its badge lights up/clears immediately, without
  // waiting for the 10-second background poll to /api/sessions. The sibling
  // entries still rely on the poll, since their pending state is owned by
  // the backend (fetched from each running OpenCode instance).
  const hasPendingPrompt = pendingPermission !== null || pendingQuestion !== null;
  useEffect(() => {
    if (!id) return;
    setRecentSessions(prev => {
      let changed = false;
      const next = prev.map(s => {
        if (s.id !== id) return s;
        const newPerm = pendingPermission !== null;
        const newQuestion = pendingQuestion !== null;
        if (s.pendingPermission === newPerm && s.pendingQuestion === newQuestion) return s;
        changed = true;
        return { ...s, pendingPermission: newPerm, pendingQuestion: newQuestion };
      });
      if (changed) {
        // Keep the hash cache in sync so the next poll still diffs correctly.
        lastSiblingsHashRef.current = computeSidebarHash(next);
        return next;
      }
      return prev;
    });
  }, [id, pendingPermission, pendingQuestion, hasPendingPrompt]);

  // Reverse sync: sidebar poll → detail view. The sidebar polls
  // /api/sessions every 3 seconds and the backend computes
  // pendingPermission / pendingQuestion from live OpenCode instances.
  // If the sidebar discovers a prompt that the detail view doesn't
  // know about (e.g. SSE event was missed), trigger a fetch of the
  // actual permission/question data so the prompt dialog appears.
  const sidebarCurrentSession = recentSessions.find(s => s.id === id);
  const sidebarHasPerm = sidebarCurrentSession?.pendingPermission ?? false;
  const sidebarHasQuestion = sidebarCurrentSession?.pendingQuestion ?? false;
  useEffect(() => {
    if (!id) return;
    // Only fetch if the sidebar says there's a prompt but the detail
    // view doesn't have one yet. Avoids redundant fetches when SSE
    // already delivered the event.
    if (sidebarHasPerm && pendingPermission === null) {
      listPermissions(id).then((perms) => {
        for (const p of perms) {
          const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
          if (!perm) continue;
          const props = p as Record<string, unknown>;
          const promptSid = typeof props.sessionID === 'string' ? props.sessionID : '';
          if (!isSessionRelevant(promptSid, id, subagentSessionIdsRef.current)) continue;
          setPendingPermission(perm);
          setPermissionError(null);
          break;
        }
      }).catch(() => { /* sidebar will retry on next poll */ });
    }
    if (sidebarHasQuestion && pendingQuestion === null) {
      listQuestions(id).then((questions) => {
        for (const q of questions) {
          const question = extractPendingQuestion({ type: 'question.asked', properties: q });
          if (!question) continue;
          const props = q as Record<string, unknown>;
          const questionSid = typeof props.sessionID === 'string' ? props.sessionID : '';
          if (!isSessionRelevant(questionSid, id, subagentSessionIdsRef.current)) continue;
          storePendingQuestion(id, question);
          setPendingQuestion((prev) => prev ?? question);
          break;
        }
      }).catch(() => { /* sidebar will retry on next poll */ });
    }
  }, [id, sidebarHasPerm, sidebarHasQuestion, pendingPermission, pendingQuestion, listPermissions, listQuestions]);

  const sessionSeenId = session?.id;
  const sessionSeenPlatform = session?.platform;
  const sessionSeenUpdated = session?.timeUpdated || 0;

  useEffect(() => {
    if (!sessionSeenId || !sessionSeenPlatform) return;
    void markSessionSeen(sessionSeenPlatform, sessionSeenId, sessionSeenUpdated)
      .then(() => {
        setSession(prev => prev && prev.id === sessionSeenId ? { ...prev, seen: true } : prev);
        setRecentSessions(prev => prev.map(s => (s.id === sessionSeenId ? { ...s, seen: true } : s)));
        recheckFaviconNotify();
      })
      .catch(err => console.error('Failed to mark session seen', err));
  }, [markSessionSeen, sessionSeenId, sessionSeenPlatform, sessionSeenUpdated]);

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
    sseActive,
    sseDebugEvents,
    setSseDebugEvents,
  } = useSessionSSE({
    sessionId: session?.id,
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
  });

  // Compute aggregate token/cost stats from the messages array so the header
  // stays up-to-date from SSE events without needing a server round-trip.
  // Use the larger of server-provided totals and locally-computed totals.
  // The server value covers all messages including paginated-out ones;
  // the local value picks up incremental SSE updates before the next load().
  const liveTokens = computeLiveTokens(messages);
  const tokenStats = mergeTokenStats(session, liveTokens);

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

  const { activeModel, activeAgent } = deriveActiveModelAndAgent(messages, session);

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

    try {
      await sendMessage(session.id, text, images, model, agent, reasoning);
      // Success — drop any prior failed entry for this id (only relevant on
      // retry; recording is otherwise idempotent). SSE will deliver the
      // real message + assistant response incrementally.
      setFailedSends(prev => prev.filter(e => e.id !== tempId));
      removeFailedSend(session.id, tempId);
    } catch (e) {
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
  }, [pendingPermission, pendingQuestion, portAvailable, sendMessage, session]);

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
  }, [activeAgent, activeModel, pendingPermission, pendingQuestion, performSend, portAvailable, selectedAgent, selectedModel, selectedReasoning, session]);

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
  }, [session]);

  // When the user picks a different model, clear the reasoning selection
  // because the new model may not support the same variants.
  const handleModelChange = useCallback((model: string) => {
    setSelectedModel(model);
    setSelectedReasoning('');
  }, []);

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
  }, [activeAgent, activeModel, caps.compact, portAvailable, selectedAgent, selectedModel, session]);

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
      if (res.id) navigate(`/session/${res.id}`);
    } catch (e) {
      console.error('Failed to create session', e);
      setShowCreateSessionErrorToast(true);
    }
  }, [createSession, launchOpencodeInTmux, tmux.available, navigate]);

  const handleNewSession = useCallback(async (title?: string) => {
    if (!session) return;
    await handleNewSessionInDirectory(session.directory, title);
  }, [session, handleNewSessionInDirectory]);

  const handleCommand = useCallback(async (command: string, args: string) => {
    if (!session) return;

    // /archive is a local ocman action — it works even when the agent isn't running.
    if (command === 'archive') {
      // Pick the session at idx+1 (directly below) or idx-1 (directly above)
      // from the displayed sidebar list, captured before the API call.
      const idx = recentSessions.findIndex(s => s.id === session.id);
      const nextSession = recentSessions[idx + 1] ?? recentSessions[idx - 1];
      try {
        await archiveSession(session.platform, session.id, session.timeUpdated, true);
      } catch (e) {
        console.error('Failed to archive session', e);
        return;
      }
      navigate(nextSession ? `/session/${nextSession.id}` : '/');
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
      try {
        const res = await createSessionWithLaunch(
          {
            createSession,
            launchOpencodeInTmux,
            tmuxAvailable: tmux.available,
            onStatusChange: setCreateLaunchStatus,
          },
          { directory: session.directory, title: args.trim() || undefined },
        );
        newId = res.id;
      } catch (e) {
        console.error('Failed to create session', e);
        setShowCreateSessionErrorToast(true);
        return;
      }
      try {
        await archiveSession(session.platform, session.id, session.timeUpdated, true);
      } catch (e) {
        console.error('Failed to archive session', e);
      }
      if (newId) navigate(`/session/${newId}`);
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
      console.log('Rename command triggered', { args, sessionId: session.id, title: session.title });
      if (args.trim()) {
        try {
          console.log('Calling renameSession API');
          await api.renameSession(session.id, args.trim());
          console.log('API call successful, updating state');
          setSession(prev => prev ? { ...prev, title: args.trim() } : prev);
          setShowRenameToast(true);
        } catch (e) {
          console.error('Failed to rename session', e);
        }
      } else {
        console.log('Showing rename modal');
        setRenameTitle(session.title || '');
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
  }, [activeAgent, activeModel, archiveSession, createSession, launchOpencodeInTmux, openWorktreeForm, tmux.available, handleCompact, handleNewSession, navigate, portAvailable, recentSessions, selectedAgent, selectedModel, session]);

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
  }, [activeAgent, pendingPermission, pendingQuestion, portAvailable, selectedAgent, session]);

  const abortSession = useApiStore((state) => state.abortSession);

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

  // Alt+J / Alt+K: navigate between recent sessions. Handlers read from refs
  // so they can capture the latest recentSessions without re-registering.
  const jumpToSession = useCallback((direction: 1 | -1) => {
    const sessions = recentSessionsRef.current;
    const currentIndex = sessions.findIndex((s) => s.id === id);
    if (currentIndex === -1) return;
    const target = sessions[currentIndex + direction];
    if (target) navigate(`/session/${target.id}`);
  }, [id, navigate]);

  // Keep the page-level refs that the palette dispatcher reads.
  // They mirror values used by both the dispatcher and (indirectly,
  // through useSessionShortcuts) the shortcut handlers.
  const sessionRef = useRef(session);
  useEffect(() => { sessionRef.current = session; }, [session]);
  const selectedModelRef = useRef(selectedModel);
  useEffect(() => { selectedModelRef.current = selectedModel; }, [selectedModel]);
  const activeModelRef = useRef(activeModel);
  useEffect(() => { activeModelRef.current = activeModel; }, [activeModel]);
  const capsRef = useRef(caps);
  useEffect(() => { capsRef.current = caps; }, [caps]);

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
  const composerModels = Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model)));
  const showSseNotice = portAvailable && !sseActive;
  const showSseDebug = debugMode && sseDebugEvents.length > 0;

  // The assistant is still working if the last message is from the
  // user (assistant hasn't replied yet) or from the assistant with no
  // finish reason and no error (still streaming). See
  // `isSessionRunning` for the canonical predicate.
  const isRunning = isSessionRunning(lastMsg);

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
    isRunning,
    pendingPermission,
    pendingQuestion,
  });

  useEffect(() => {
    if (!id) return;
    setRecentSessions(prev => {
      let changed = false;
      const next = prev.map(s => {
        if (s.id !== id) return s;
        if (s.status === optimisticStatus) return s;
        changed = true;
        return { ...s, status: optimisticStatus };
      });
      if (changed) {
        // Keep the hash cache in sync so the next poll still diffs correctly.
        lastSiblingsHashRef.current = computeSidebarHash(next);
        return next;
      }
      return prev;
    });
  }, [id, optimisticStatus]);

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
  const sidebarProjectGroups = useMemo(() => {
    // Bucket sessions by *project root*, not raw cwd, so that worktrees
    // of the same repo live under one parent. projectRootForDirectory
    // folds <prefix>/.worktrees/<repo>/<slug> back to <prefix>/<repo>;
    // anything outside that layout passes through unchanged, so
    // unrelated projects keep their own groups.
    const buckets = new Map<string, Session[]>();
    for (const s of recentSessions) {
      const key = projectRootForDirectory(s.directory || '');
      const existing = buckets.get(key);
      if (existing) existing.push(s);
      else buckets.set(key, [s]);
    }

    // Override the page session's recorded status with our optimistic
    // value so the sidebar dot starts/stops pulsing in lock-step with
    // the assistant's turn boundary, without waiting for the next poll.
    const effectiveStatus = (s: Session): Session['status'] =>
      s.id === id ? optimisticStatus : s.status;
    const rollup = (sessions: Session[]) => rollupGroupStatus(sessions, effectiveStatus);

    const groups: { directory: string; sessions: Session[]; lastUpdated: number; aggregate: ReturnType<typeof rollup>; isPinned?: boolean }[] = Array.from(buckets.entries()).map(([directory, sessions]) => {
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

  // Keep the active session's sidebar row visible. The list doesn't reorder
  // to follow the cursor, so when the user switches sessions (or flips
  // views) the active row may be off-screen in a long list. We scroll it
  // into view with `nearest` block alignment so we don't yank the viewport
  // unless it's actually necessary. Skipped while the recent-sessions poll
  // is mid-flight for the initial load — the DOM may not yet contain a row
  // for `id`.
  const sidebarListRef = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!id) return;
    const container = sidebarListRef.current;
    if (!container) return;
    // Run on the next frame so any just-expanded group has finished laying
    // out before we measure offsets.
    const raf = requestAnimationFrame(() => {
      const active = container.querySelector('[aria-selected="true"]') as HTMLElement | null;
      if (!active) return;
      const cTop = container.scrollTop;
      const cBot = cTop + container.clientHeight;
      const aTop = active.offsetTop;
      const aBot = aTop + active.offsetHeight;
      if (aTop < cTop || aBot > cBot) {
        active.scrollIntoView({ block: 'nearest', behavior: 'smooth' });
      }
    });
    return () => cancelAnimationFrame(raf);
  }, [id, sidebarView, recentSessions]);

  return (
    <Toast.Provider swipeDirection="right">
      <div className="session-layout" data-testid="session-layout">
        <div className="session-sidebar" data-testid="session-sidebar" style={{ width: sidebarWidth }}>
        <SidebarResizer />
        <div className="session-sidebar-header">
          <span className="session-sidebar-heading" data-testid="sidebar-heading">
            <span>{sidebarView === 'projects' ? 'Projects' : 'Recent sessions'}</span>
          </span>
          <div className="session-sidebar-header-actions">
            <button
              type="button"
              className={`session-sidebar-new${sidebarView === 'projects' ? ' active' : ''}`}
              onClick={toggleSidebarView}
              title={sidebarView === 'projects' ? 'Show recent sessions' : 'Group by project'}
              aria-label={sidebarView === 'projects' ? 'Show recent sessions' : 'Group by project'}
            >{sidebarView === 'projects' ? <RecentViewIcon /> : <ProjectsViewIcon />}</button>
            <button
              type="button"
              className={`session-sidebar-new${showArchivedRecent ? ' active' : ''}`}
              onClick={() => {
                setShowArchivedRecent(current => !current);
              }}
              title={showArchivedRecent ? 'Hide archived sessions' : 'Include archived sessions'}
              aria-label={showArchivedRecent ? 'Hide archived sessions' : 'Include archived sessions'}
            ><ArchiveFilterIcon /></button>
          </div>
        </div>
        {pendingTmuxSession && pickerPos && (
          <div
            ref={pickerRef}
            className="tmux-client-popover"
            style={{ top: pickerPos.top, left: pickerPos.left }}
          >
            <div className="tmux-client-picker-header">
              <span>Select tmux client</span>
            </div>
            {tmux.clients.map(c => (
              <div
                key={c.tty}
                className="tmux-client-picker-item"
                onClick={() => handleClientSelect(c.tty)}
              >
                <span className="tmux-client-tty">{c.tty}</span>
                <span className="tmux-client-session">{shortPath(c.session)}</span>
                <span className="tmux-client-size">{c.width}&times;{c.height}</span>
              </div>
            ))}
          </div>
        )}
        <div className="session-sidebar-list" ref={sidebarListRef}>
          {loadingRecentSessions && <div className="session-sidebar-loader"><div className="oc-spinner" /></div>}
          {(() => {
            // Shared row renderer — used by both the flat and grouped views so
            // all live-status / archive / navigation behaviour stays identical.
            // For the currently-viewed session we trust the SSE-derived status
            // over the last poll (OpenCode's DB can lag SSE by several seconds;
            // using the poll value here would leave the sidebar pulse running
            // after the composer has already gone idle).
            const renderRow = (sib: Session, inGroup: boolean) => {
              const displayStatus = sib.id === id ? optimisticStatus : sib.status;
              // When a row sits inside a project group, surface the
              // worktree distinction (if any) next to the platform
              // badge so siblings stay distinguishable. The group
              // header already shows the project root; we only add a
              // hint when the session's actual cwd diverges from it
              // (i.e. it's a worktree, not the main checkout).
              const projectRoot = projectRootForDirectory(sib.directory || '');
              const worktreeHint = inGroup && sib.directory && sib.directory !== projectRoot
                ? sib.directory.slice(projectRoot.length).replace(/^\/+/, '')
                : '';
              return (
                <div
                  key={sib.id}
                  role="button"
                  tabIndex={0}
                  aria-selected={sib.id === id}
                  className={`session-sidebar-item ${sib.id === id ? 'active' : ''}${archivingSessionIds.has(sib.id) ? ' archiving' : ''}${inGroup ? ' in-group' : ''}`}
                  onClick={() => navigate(`/session/${sib.id}`)}
                  onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`/session/${sib.id}`); } }}
                >
                  <StatusBadge
                    status={displayStatus}
                    compact
                    seen={(displayStatus === 'waiting' || displayStatus === 'error') && sib.seen}
                    pending={sib.pendingPermission || sib.pendingQuestion}
                    titleOverride={sib.notice?.message}
                  />
                  <span className="session-sidebar-item-body">
                    <span className="session-sidebar-title">{cleanTitle(sib.title) || 'Untitled'}</span>
                    {!inGroup && (
                      <span className="session-sidebar-project">
                        <PlatformBadge platform={sib.platform} variant="plain" />
                        <span className="session-sidebar-project-path">
                          <ShortPath path={sib.directory} />
                        </span>
                      </span>
                    )}
                    {inGroup && (
                      <span className="session-sidebar-project">
                        <PlatformBadge platform={sib.platform} variant="plain" />
                        {worktreeHint && (
                          <span
                            className="session-sidebar-project-path"
                            title={sib.directory}
                          >
                            {worktreeHint}
                          </span>
                        )}
                      </span>
                    )}
                    <GitStatusLine info={siblingGitInfos[sib.directory]} />
                  </span>
                  <span className="session-sidebar-meta">
                    <span className="session-sidebar-time" title={new Date(sib.timeUpdated).toLocaleString()}>{relativeTime(sib.timeUpdated)}</span>
                    <span className="session-sidebar-actions">
                      <button
                        type="button"
                        className={`session-pin-btn session-sidebar-pin-btn${sib.pinned ? ' pinned' : ''}`}
                        onClick={(e) => handlePinSession(e, sib)}
                        title={sib.pinned ? 'Unpin session' : 'Pin session'}
                        aria-label={sib.pinned ? 'Unpin session' : 'Pin session'}
                      >
                        <i className={`bi ${sib.pinned ? 'bi-pin-fill' : 'bi-pin'}`} aria-hidden="true" />
                      </button>
                      <button
                        type="button"
                        className="session-archive-btn session-sidebar-archive-btn"
                        onClick={(e) => handleArchiveSession(e, sib)}
                        title="Archive session"
                        aria-label="Archive session"
                        disabled={archivingSessionIds.has(sib.id)}
                      >
                        <ArchiveIcon />
                      </button>
                    </span>
                  </span>
                </div>
              );
            };

            if (sidebarView === 'projects') {
              return sidebarProjectGroups.map(group => {
                // The "Pinned" group is always expanded and has a
                // distinct header (pin icon, no collapse, no "+").
                if (group.isPinned) {
                  const agg = group.aggregate;
                  const dotStatus =
                    agg.kind === 'error' ? 'error'
                      : agg.kind === 'busy' ? 'busy'
                        : agg.kind === 'waiting' ? 'waiting'
                          : 'done';
                  const dotPending = agg.kind === 'pending';
                  return (
                    <div key="__pinned__" className="session-sidebar-group session-sidebar-group-pinned">
                      <div className="session-sidebar-group-header-row">
                        <div className="session-sidebar-group-header" title="Pinned sessions">
                          <span className="session-sidebar-group-status">
                            <StatusBadge status={dotStatus} compact pending={dotPending} />
                          </span>
                          <i className="bi bi-pin-fill session-sidebar-pinned-icon" aria-hidden="true" />
                          <span className="session-sidebar-group-label">Pinned</span>
                          <span className="session-sidebar-group-count">{group.sessions.length}</span>
                        </div>
                      </div>
                      {group.sessions.map(sib => renderRow(sib, false))}
                    </div>
                  );
                }

                const collapsed = collapsedProjectSet.has(group.directory);
                const label = group.directory ? shortPath(group.directory) : '(unknown)';
                // Replace the chevron with a compact status dot that
                // surfaces the rolled-up aggregate: the same visual
                // vocabulary as per-session rows (pending "!", error "!",
                // busy pulse, idle neutral), so a collapsed header tells
                // you at a glance which project needs attention. The
                // header still toggles on click — collapse state is
                // conveyed by the `aria-expanded` attribute (and a
                // subtle CSS indent) rather than a chevron.
                const agg = group.aggregate;
                const dotStatus =
                  agg.kind === 'error' ? 'error'
                    : agg.kind === 'busy' ? 'busy'
                      : agg.kind === 'waiting' ? 'waiting'
                        : 'done';
                const dotPending = agg.kind === 'pending';
                const aggTitle =
                  agg.kind === 'pending'
                    ? `${agg.count} session${agg.count === 1 ? '' : 's'} waiting for your response`
                    : agg.kind === 'error'
                      ? `${agg.count} session${agg.count === 1 ? '' : 's'} with unseen errors`
                      : agg.kind === 'busy'
                        ? `${agg.count} running`
                        : agg.kind === 'waiting'
                          ? `${agg.count} unread`
                          : `${group.sessions.length} session${group.sessions.length === 1 ? '' : 's'}`;
                return (
                  <div key={group.directory || '__empty__'} className="session-sidebar-group">
                    <div className="session-sidebar-group-header-row">
                      <button
                        type="button"
                        className={`session-sidebar-group-header${collapsed ? ' collapsed' : ''}`}
                        aria-expanded={!collapsed}
                        title={group.directory || 'Unknown project'}
                        onClick={() => toggleCollapsedProject(group.directory)}
                      >
                        <span className="session-sidebar-group-status" title={aggTitle}>
                          <StatusBadge status={dotStatus} compact pending={dotPending} />
                        </span>
                        <span className="session-sidebar-group-label">{label}</span>
                        <span className="session-sidebar-group-count" title={aggTitle}>{group.sessions.length}</span>
                      </button>
                      {group.directory && (
                        <button
                          type="button"
                          className="session-sidebar-group-new"
                          onClick={(e) => {
                            e.stopPropagation();
                            void handleNewSessionInDirectory(group.directory);
                          }}
                          title={`New session in ${label}`}
                          aria-label={`New session in ${label}`}
                        >+</button>
                      )}
                    </div>
                    {!collapsed && group.sessions.map(sib => renderRow(sib, true))}
                  </div>
                );
              });
            }

            // Flat view: pinned sessions at the top, then the rest.
            // Pinned sessions are deduplicated (shown only in the
            // pinned section, not repeated in the chronological list).
            const pinnedFlat = recentSessions
              .filter(s => s.pinned)
              .sort((a, b) => b.pinnedAt - a.pinnedAt);
            const unpinnedFlat = recentSessions.filter(s => !s.pinned);
            return (
              <>
                {pinnedFlat.map(sib => renderRow(sib, false))}
                {pinnedFlat.length > 0 && unpinnedFlat.length > 0 && (
                  <div className="session-sidebar-divider" />
                )}
                {unpinnedFlat.map(sib => renderRow(sib, false))}
              </>
            );
          })()}
        </div>
        <BackendStats />
      </div>
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
            <ErrorBoundary name="session:thread" resetKey={session.id}>
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
                  onLaunchRequest={
                    !portAvailable && !hasPendingPrompt && tmux.available && caps.liveConnectionHint
                      ? () => setShowDisconnectedToast(true)
                      : undefined
                  }
                />
              ) : null}
                </ErrorBoundary>
              )}
              footer={showSseNotice || showSseDebug ? (
                <>
                  {showSseNotice && (
                    <div className="oc-sse-indicator">Live updates unavailable -- polling every 10s</div>
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
            {showRenameModal && (
              <div className="oc-rename-backdrop" onClick={() => setShowRenameModal(false)}>
                <div className="oc-rename-dialog" onClick={e => e.stopPropagation()}>
                  <h3>Rename Session</h3>
                  <input
                    className="oc-rename-input"
                    type="text"
                    value={renameTitle}
                    onChange={e => setRenameTitle(e.target.value)}
                    placeholder="Session title"
                    autoFocus
                    onFocus={e => e.target.select()}
                    onKeyDown={async e => {
                      if (e.key === 'Enter' && session) {
                        try {
                          await api.renameSession(session.id, renameTitle.trim());
                          setSession(prev => prev ? { ...prev, title: renameTitle.trim() } : prev);
                          setShowRenameModal(false);
                          setShowRenameToast(true);
                        } catch (err) {
                          console.error('Failed to rename session', err);
                        }
                      }
                      if (e.key === 'Escape') setShowRenameModal(false);
                    }}
                  />
                  <div className="oc-rename-actions">
                    <button
                      className="oc-rename-btn oc-rename-btn-submit"
                      onClick={async () => {
                        if (!session) return;
                        try {
                          await api.renameSession(session.id, renameTitle.trim());
                          setSession(prev => prev ? { ...prev, title: renameTitle.trim() } : prev);
                          setShowRenameModal(false);
                          setShowRenameToast(true);
                        } catch (err) {
                          console.error('Failed to rename session', err);
                        }
                      }}
                    >
                      Rename
                    </button>
                    <button className="oc-rename-btn oc-rename-btn-cancel" onClick={() => setShowRenameModal(false)}>
                      Cancel
                    </button>
                  </div>
                </div>
              </div>
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
      <Toast.Root className="oc-toast-root" open={showRenameToast} onOpenChange={setShowRenameToast} duration={2000}>
        <Toast.Description className="oc-toast-description">
          Session renamed
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        className="oc-toast-root"
        open={createLaunchStatus !== 'idle'}
        duration={Infinity}
      >
        <Toast.Description className="oc-toast-description">
          {createLaunchStatus === 'launching'
            ? 'Launching opencode in tmux…'
            : 'Waiting for opencode to start…'}
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        className="oc-toast-root error"
        open={showCreateSessionErrorToast}
        onOpenChange={setShowCreateSessionErrorToast}
        duration={3500}
      >
        <Toast.Description className="oc-toast-description">
          Failed to create session
        </Toast.Description>
      </Toast.Root>
      <Toast.Root
        className="oc-toast-root error"
        open={showDisconnectedToast}
        onOpenChange={setShowDisconnectedToast}
        duration={8000}
      >
        <Toast.Description className="oc-toast-description">
          <div className="oc-toast-body">
            <span>OpenCode is not running for this session.</span>
            {tmux.available && caps.liveConnectionHint && session?.directory && (
              <button
                type="button"
                className="oc-toast-action"
                disabled={launchingOpencode}
                onClick={() => {
                  setShowDisconnectedToast(false);
                  void handleLaunchOpencode();
                }}
              >{launchingOpencode ? 'Launching…' : 'Launch opencode'}</button>
            )}
          </div>
        </Toast.Description>
      </Toast.Root>
      <Toast.Viewport className="oc-toast-viewport" />
    </div>
    </Toast.Provider>
  );
}
