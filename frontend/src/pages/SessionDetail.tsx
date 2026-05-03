import { useState, useEffect, useCallback, useRef, useMemo } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import * as Toast from '@radix-ui/react-toast';
import './SessionDetail.css';
import { api, type Session, type Message, type Part, type AgentInfo, type SessionModelEntry, type SessionDetail } from '../lib/api';
import { cleanTitle, shortPath, relativeTime } from '../lib/format';
import { projectRootForDirectory } from '../lib/worktrees';
import { useHeaderInfo, usePageTitle } from '../lib/headerContext';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import { AssistantThread } from '../components/AssistantThread';
import { Composer, type AttachedImage } from '../components/assistant/Composer';
import { QuestionPrompt, type PendingQuestion, type QuestionItem } from '../components/session/QuestionPrompt';
import { PermissionPrompt } from '../components/session/PermissionPrompt';
import { StatusBadge } from '../components/StatusBadge';
import { PlatformBadge } from '../components/PlatformBadge';
import { ShortPath, GitStatusLine } from '../components/SessionTable';
import { BackendStats } from '../components/BackendStats';
import { SidebarResizer } from '../components/SidebarResizer';
import { RightPanel } from '../components/RightPanel';
import { ErrorBoundary } from '../components/ErrorBoundary';
import { useUiStore } from '../lib/uiStore';
import { useTmux } from '../lib/useTmux';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { useApiStore } from '../lib/apiStore';
import { useGitInfo } from '../lib/useGitInfo';
import { usePlatformCapabilities } from '../lib/useCapabilities';
import { recheckFaviconNotify } from '../lib/useFaviconNotify';
import { notifyPromptDismissed } from '../lib/useToastNotify';
import { openVSCode } from '../lib/shortcuts';
import { useShortcut } from '../lib/shortcutRegistry';
import { hashSession, hashMessagesAndParts } from '../lib/sessionHash';
import { createSessionWithLaunch, type LaunchStatus } from '../lib/createSessionWithLaunch';
import { isSessionRelevant } from '../lib/promptRouting';
import {
  listFailedSends,
  recordFailedSend,
  removeFailedSend,
  type FailedSend,
} from '../lib/failedSends';

const PAGE_SIZE = 30;
const RECENT_SESSIONS_LIMIT = 15;
const SIDEBAR_RECENT_HOURS = 72;
const ARCHIVE_ANIMATION_MS = 220;
// How often the Recent Sessions sidebar re-polls /api/sessions. Kept low enough
// to feel live, but not so low that we hammer the OpenCode port-discovery +
// per-instance HTTP fan-out on every tick. Polling is paused while the tab is
// hidden.
const SIDEBAR_REFRESH_MS = 3000;
const MAX_RETAINED_MESSAGES = 200;
const TRIMMED_RETAINED_MESSAGES = 150;
const MAX_SUBAGENT_TOKEN_ENTRIES = 256;

// Maximum length for part text/output before truncation (matches backend maxOutputLen).
const MAX_OUTPUT_LEN = 200000;

/** Truncate large string fields in a part to keep memory usage manageable. */
function truncatePartField(value: unknown): unknown {
  if (typeof value === 'string' && value.length > MAX_OUTPUT_LEN) {
    return value.slice(0, MAX_OUTPUT_LEN) + '\n... (truncated)';
  }
  return value;
}

function trimSubagentTokens(
  prev: Map<string, { output: number; created: number }>,
): Map<string, { output: number; created: number }> {
  if (prev.size <= MAX_SUBAGENT_TOKEN_ENTRIES) return prev;
  const entries = Array.from(prev.entries());
  return new Map(entries.slice(entries.length - MAX_SUBAGENT_TOKEN_ENTRIES));
}

/**
 * Try to extract a Message + Parts from a raw SSE event payload.
 * OpenCode SSE events for message changes carry an `info` object (the message
 * metadata) and an optional `parts` array — the same structure the backend's
 * convertOpenCodeMessages reads from the HTTP API.
 *
 * Returns null when the event doesn't contain usable message data.
 */
function extractMessageFromEvent(
  parsed: Record<string, unknown>,
  sessionId: string,
): { message: Message; parts: Part[] } | null {
  // The event can wrap the payload in `properties` or carry it at the top level.
  const props = (parsed.properties && typeof parsed.properties === 'object')
    ? parsed.properties as Record<string, unknown>
    : parsed;

  const info = props.info as Record<string, unknown> | undefined;
  if (!info || typeof info !== 'object') return null;

  const msgId = info.id as string | undefined;
  if (!msgId) return null;

  const timeObj = info.time as Record<string, unknown> | undefined;
  const timeCreated = typeof timeObj?.created === 'number' ? timeObj.created : Date.now();

  // Default to 'assistant' if role is not yet set (can happen during early streaming)
  const role = (info.role as string) || 'assistant';

  const message: Message = {
    id: msgId,
    sessionId: info.sessionID as string || sessionId,
    timeCreated,
    data: {
      role,
      finish: info.finish as string | undefined,
      modelID: info.modelID as string | undefined,
      providerID: info.providerID as string | undefined,
      agent: info.agent as string | undefined,
      mode: info.mode as string | undefined,
      cost: typeof info.cost === 'number' ? info.cost : undefined,
      tokens: info.tokens as Message['data']['tokens'],
      error: info.error as Message['data']['error'],
    },
  };

  const parts: Part[] = [];
  const rawParts = props.parts;
  if (Array.isArray(rawParts)) {
    for (const p of rawParts) {
      if (!p || typeof p !== 'object') continue;
      const part = p as Record<string, unknown>;
      const partType = part.type as string | undefined;
      // Skip non-essential types (same filter as backend convertOpenCodeMessages)
      if (partType === 'step-start' || partType === 'step-finish' || partType === 'snapshot') continue;

      // Truncate large outputs
      if (typeof part.text === 'string') part.text = truncatePartField(part.text);
      const state = part.state as Record<string, unknown> | undefined;
      if (state) {
        if (typeof state.output === 'string') state.output = truncatePartField(state.output);
        const meta = state.metadata as Record<string, unknown> | undefined;
        if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output);
      }

      parts.push({
        id: (part.id as string) || `sse-part-${msgId}-${parts.length}`,
        messageId: (part.messageID as string) || msgId,
        sessionId: (part.sessionID as string) || sessionId,
        data: part as unknown as string, // stored as the raw object, same as backend
      });
    }
  }

  return { message, parts };
}

/**
 * Try to extract a single Part update from an SSE event.
 * `part.updated` events carry the part data directly in `properties`.
 */
function extractPartFromEvent(
  parsed: Record<string, unknown>,
  sessionId: string,
): Part | null {
  const props = (parsed.properties && typeof parsed.properties === 'object')
    ? parsed.properties as Record<string, unknown>
    : parsed;

  const partId = props.id as string | undefined;
  const messageId = props.messageID as string | undefined;
  if (!partId || !messageId) return null;

  const partType = props.type as string | undefined;
  if (partType === 'step-start' || partType === 'step-finish' || partType === 'snapshot') return null;

  // Truncate large fields
  if (typeof props.text === 'string') props.text = truncatePartField(props.text);
  const state = props.state as Record<string, unknown> | undefined;
  if (state) {
    if (typeof state.output === 'string') state.output = truncatePartField(state.output);
    const meta = state.metadata as Record<string, unknown> | undefined;
    if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output);
  }

  return {
    id: partId,
    messageId,
    sessionId: (props.sessionID as string) || sessionId,
    data: props as unknown as string,
  };
}

interface PendingPermission {
  permissionId: string;
  permission: string;
  patterns: string[];
  // sessionId of the (sub)agent that asked for the permission. When the
  // prompt comes from a subagent of the page's session, the response
  // must be POSTed to that subagent's session, not the parent's.
  // Empty string means "asked by the page's own session" (legacy
  // payloads without a sessionID).
  sessionId: string;
}

interface SseDebugEvent {
  at: number;
  event: string;
  data: string;
}

function formatModelRef(providerId?: string, modelId?: string): string {
  if (!modelId) return '';
  return providerId ? `${providerId}/${modelId}` : modelId;
}

// Checks whether a parsed `session.status` SSE event reports the session as
// idle (the only terminal state among busy/retry/idle). Used to avoid
// clearing pending permission/question prompts during intermediate status
// transitions fired mid-request.
function isSessionStatusIdle(parsed: Record<string, unknown>): boolean {
  const props = parsed.properties as Record<string, unknown> | undefined;
  if (!props) return false;
  const status = props.status;
  if (typeof status === 'string') return status === 'idle';
  if (status && typeof status === 'object' && !Array.isArray(status)) {
    const t = (status as Record<string, unknown>).type;
    return typeof t === 'string' && t === 'idle';
  }
  return false;
}

function extractPendingPermission(node: unknown): PendingPermission | null {
  if (!node || typeof node !== 'object' || Array.isArray(node)) return null;
  const obj = node as Record<string, unknown>;

  if (obj.type !== 'permission.asked') return null;

  const properties = (obj.properties && typeof obj.properties === 'object')
    ? obj.properties as Record<string, unknown>
    : {};

  const id =
    (typeof properties.id === 'string' && properties.id) ||
    (typeof properties.requestID === 'string' && properties.requestID) ||
    '';
  if (!id) return null;

  const permission =
    (typeof properties.permission === 'string' && properties.permission) || 'Permission required';

  const rawPatterns = properties.patterns;
  const patterns = Array.isArray(rawPatterns)
    ? rawPatterns.filter((p): p is string => typeof p === 'string')
    : [];

  // sessionID is the (sub)agent that issued the prompt. Captured so the
  // reply can be POSTed to the right /api/session/{id}/permissions/{pid}
  // endpoint when the prompt came from a subagent of the page session.
  const sessionId =
    (typeof properties.sessionID === 'string' && properties.sessionID) ||
    (typeof properties.sessionId === 'string' && properties.sessionId) ||
    '';

  return { permissionId: id, permission, patterns, sessionId };
}

function extractPendingQuestion(node: unknown): PendingQuestion | null {
  if (!node || typeof node !== 'object' || Array.isArray(node)) return null;
  const obj = node as Record<string, unknown>;

  if (obj.type !== 'question.asked') return null;

  const properties = (obj.properties && typeof obj.properties === 'object')
    ? obj.properties as Record<string, unknown>
    : {};

  const id =
    (typeof properties.id === 'string' && properties.id) ||
    (typeof properties.requestID === 'string' && properties.requestID) ||
    (typeof properties.requestId === 'string' && properties.requestId) ||
    '';
  if (!id) return null;

  const sessionID =
    (typeof properties.sessionID === 'string' && properties.sessionID) ||
    (typeof properties.sessionId === 'string' && properties.sessionId) ||
    '';

  const rawQuestions = properties.questions;
  const questions = normalizeQuestionItems(rawQuestions);
  if (questions.length === 0) return null;

  return { requestId: id, sessionID, questions };
}

const QUESTION_TOOL_NAMES = ['question', 'mcp_question', 'Question', 'mcp_Question'];

function normalizeQuestionItems(raw: unknown): QuestionItem[] {
  let value = raw;

  if (typeof value === 'string') {
    try {
      value = JSON.parse(value) as unknown;
    } catch {
      return [];
    }
  }

  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>;
    if (Array.isArray(obj.questions)) value = obj.questions;
  }

  if (!Array.isArray(value) || value.length === 0) return [];

  return value.filter(
    (q): q is QuestionItem =>
      !!q && typeof q === 'object' && typeof (q as QuestionItem).question === 'string' && Array.isArray((q as QuestionItem).options),
  );
}

function hasQuestionOutput(output: unknown): boolean {
  return output != null && output !== '' && output !== '""' && output !== '[]';
}

function extractPendingQuestionFromPart(part: Part, sessionId: string): PendingQuestion | null {
  let pd: Record<string, unknown>;
  try {
    pd = typeof part.data === 'string' ? JSON.parse(part.data) : (part.data as unknown as Record<string, unknown>);
  } catch {
    return null;
  }

  if (pd.type !== 'tool') return null;
  const toolName = pd.tool as string | undefined;
  if (!toolName || !QUESTION_TOOL_NAMES.includes(toolName)) return null;

  const state = pd.state as Record<string, unknown> | undefined;
  if (!state) return null;

  const status = state.status as string | undefined;
  if (status !== 'running' && hasQuestionOutput(state.output)) return null;

  const input = (state.input && typeof state.input === 'object' && !Array.isArray(state.input))
    ? state.input as Record<string, unknown>
    : {};
  const metadata = (state.metadata && typeof state.metadata === 'object' && !Array.isArray(state.metadata))
    ? state.metadata as Record<string, unknown>
    : {};

  const requestId =
    (typeof input.requestId === 'string' && input.requestId) ||
    (typeof input.requestID === 'string' && input.requestID) ||
    (typeof input.id === 'string' && input.id) ||
    (typeof metadata.requestId === 'string' && metadata.requestId) ||
    (typeof metadata.requestID === 'string' && metadata.requestID) ||
    (typeof metadata.id === 'string' && metadata.id) ||
    '';
  if (!requestId) return null;

  const questions = normalizeQuestionItems(input.questions ?? state.input);
  if (questions.length === 0) return null;

  const pendingSessionId =
    (typeof input.sessionID === 'string' && input.sessionID) ||
    (typeof input.sessionId === 'string' && input.sessionId) ||
    sessionId;

  return { requestId, sessionID: pendingSessionId, questions };
}

function extractPendingQuestionFromParts(parts: Part[], sessionId: string): PendingQuestion | null {
  for (let i = parts.length - 1; i >= 0; i--) {
    const pending = extractPendingQuestionFromPart(parts[i], sessionId);
    if (pending) return pending;
  }
  return null;
}

/** Check if loaded parts contain a pending (unanswered) question tool call. */
function hasPendingQuestionInParts(parts: Part[], sessionId: string): boolean {
  if (extractPendingQuestionFromParts(parts, sessionId)) return true;

  for (let i = parts.length - 1; i >= 0; i--) {
    let pd: Record<string, unknown>;
    try {
      const data = parts[i].data;
      pd = typeof data === 'string' ? JSON.parse(data) : (data as unknown as Record<string, unknown>);
    } catch {
      continue;
    }

    if (pd.type !== 'tool') continue;
    const toolName = pd.tool as string | undefined;
    if (!toolName || !QUESTION_TOOL_NAMES.includes(toolName)) continue;

    const state = pd.state as Record<string, unknown> | undefined;
    if (!state) continue;
    if (state.status === 'running' || !hasQuestionOutput(state.output)) return true;
  }

  return false;
}

const PENDING_QUESTION_KEY = 'ocman:pendingQuestion:';

function storePendingQuestion(sessionId: string, question: PendingQuestion) {
  try {
    sessionStorage.setItem(PENDING_QUESTION_KEY + sessionId, JSON.stringify(question));
  } catch { /* quota exceeded or unavailable */ }
}

function loadPendingQuestion(sessionId: string): PendingQuestion | null {
  try {
    const raw = sessionStorage.getItem(PENDING_QUESTION_KEY + sessionId);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (parsed && parsed.requestId && Array.isArray(parsed.questions) && parsed.questions.length > 0) {
      return parsed as PendingQuestion;
    }
  } catch { /* corrupt or unavailable */ }
  return null;
}

function clearPendingQuestion(sessionId: string) {
  try {
    sessionStorage.removeItem(PENDING_QUESTION_KEY + sessionId);
  } catch { /* unavailable */ }
}

function truncateSseData(raw: string, max = 500): string {
  if (raw.length <= max) return raw;
  return raw.slice(0, max) + '...';
}

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
  const [messages, setMessages] = useState<Message[]>(initialCached?.messages ?? []);
  const [parts, setParts] = useState<Part[]>(initialCached?.parts ?? []);
  const [totalMessages, setTotalMessages] = useState(
    initialCached?.totalMessages ?? initialCached?.session.messageCount ?? 0,
  );
  const [loading, setLoading] = useState(!initialCached);
  // Briefly hides the thread viewport between sessions so the fade-in
  // animation plays against a blank backdrop rather than swapping content
  // in place. Set to true synchronously on id change, cleared on the next
  // animation frame. See spec/session-switch-cache (step 4 follow-up).
  const [switching, setSwitching] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  // Increments whenever an edit/write tool part is added or updated via
  // SSE. Consumed by the session-changes sidebar's useSessionChanges hook
  // to debounced-refetch /api/session/{id}/changes after live edits.
  const [changesDirtyTick, setChangesDirtyTick] = useState(0);

  // Capability flags for the owning platform. Used to *hide* affordances
  // the platform doesn't support (composer, abort, compact, ...). Falls
  // back to all-false before /api/capabilities resolves, which keeps UI
  // dormant — preferable to flashing controls the platform can't honour.
  const caps = usePlatformCapabilities(session?.platform);

  // `portAvailable` represents transient reachability of the running
  // platform process (e.g. OpenCode on a discovered --port). Capability
  // flags describe what the platform supports in principle; an action
  // should generally be enabled iff both are true.
  const [portAvailable, setPortAvailable] = useState(false);
  const [whisperAvailable, setWhisperAvailable] = useState(false);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [modelEntries, setModelEntries] = useState<SessionModelEntry[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedReasoning, setSelectedReasoning] = useState('');
  const [agents, setAgents] = useState<AgentInfo[]>([]);
  // Tracks whether we've finished attempting to load the /agent catalog for
  // the current session's directory. UI that colors by agent should stay
  // muted until this flips true — otherwise we flash a fallback color (e.g.
  // `build`'s mauve default) before the authoritative color arrives from the
  // API, which reads as a jarring pink flash on the composer.
  const [agentsLoaded, setAgentsLoaded] = useState(false);
  const [recentSessions, setRecentSessions] = useState<Session[]>([]);
  const [loadingRecentSessions, setLoadingRecentSessions] = useState(true);

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

  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [showArchivedRecent, setShowArchivedRecent] = useState(false);

  // Track token data from subagent sessions so the TPS indicator includes their output.
  // Maps subagent messageId -> { output: output tokens for that message, created: time.created }
  const [subagentTokens, setSubagentTokens] = useState<Map<string, { output: number; created: number }>>(new Map());
  // Track live output from running tasks. Maps taskId (child session) -> last 10 lines of stdout.
  // Fetched by polling the task's session while it runs.
  const [taskLiveOutput, setTaskLiveOutput] = useState<Record<string, string>>({});
  const { setInfo } = useHeaderInfo();
  usePageTitle(cleanTitle(session?.title) || 'Session');
  const lastHashRef = useRef('');
  const lastSessionHashRef = useRef('');
  const lastSiblingsHashRef = useRef('');
  const archiveTimeoutsRef = useRef<Record<string, number>>({});
  const abortControllerRef = useRef<AbortController | null>(null);
  const showArchivedRecentRef = useRef(showArchivedRecent);
  const droppedMessageCountRef = useRef(0);
  // Tracks the currently-rendered session's directory so the session-change
  // effect can compare it against the incoming one without subscribing to
  // `session` (which would cause the effect to fire on every render).
  const currentDirectoryRef = useRef<string | undefined>(session?.directory);

  // Tmux state
  const tmux = useTmux();
  const openWorktreeForm = useUiStore((s) => s.openWorktreeForm);
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  // Mirrored so SSE's onopen closure can read the latest value without
  // re-subscribing. Used to gate the reconciliation fetch (step 5 of
  // spec/session-switch-cache).
  const loadErrorRef = useRef<string | null>(null);
  loadErrorRef.current = loadError;
  const [answeringPermission, setAnsweringPermission] = useState(false);
  const [pendingPermission, setPendingPermission] = useState<PendingPermission | null>(null);
  const [permissionError, setPermissionError] = useState<string | null>(null);
  const [pendingQuestion, setPendingQuestion] = useState<PendingQuestion | null>(null);
  const [answeringQuestion, setAnsweringQuestion] = useState(false);
  const [questionError, setQuestionError] = useState<string | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<SseDebugEvent[]>([]);
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
  const getSession = useApiStore((state) => state.getSession);
  const archiveSession = useApiStore((state) => state.archiveSession);
  const pinSession = useApiStore((state) => state.pinSession);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const getModels = useApiStore((state) => state.getModels);
  const getSessions = useApiStore((state) => state.getSessions);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const sendMessage = useApiStore((state) => state.sendMessage);
  const listPermissions = useApiStore((state) => state.listPermissions);
  const respondPermission = useApiStore((state) => state.respondPermission);
  const listQuestions = useApiStore((state) => state.listQuestions);
  const respondQuestion = useApiStore((state) => state.respondQuestion);
  const rejectQuestion = useApiStore((state) => state.rejectQuestion);
  const createSession = useApiStore((state) => state.createSession);
  const launchOpencodeInTmux = useApiStore((state) => state.launchOpencodeInTmux);
  const setCachedSession = useApiStore((state) => state.setCachedSession);
  const updateCachedSession = useApiStore((state) => state.updateCachedSession);
  const sidebarWidth = useUiStore((state) => state.sidebarWidth);
  const sidebarView = useUiStore((state) => state.sidebarView);
  const toggleSidebarView = useUiStore((state) => state.toggleSidebarView);
  const collapsedProjects = useUiStore((state) => state.collapsedProjects);
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

  // Load the latest page (newest messages). Merges with older loaded messages.
  const load = useCallback(async (signal?: AbortSignal) => {
    if (!id) return;
    try {
      const result = await getSession(id, PAGE_SIZE, 0, signal);

      // If this request was aborted, don't update state
      if (signal?.aborted) return;

      // Only update session metadata if it actually changed
      const sessionData = {
        ...result.session,
        contextTokenCount: result.session.contextTokenCount ?? result.contextTokenCount,
        defaultAgent: result.defaultAgent,
        defaultModel: result.defaultModel,
      };
      const sessionHash = hashSession(sessionData);
      if (sessionHash !== lastSessionHashRef.current) {
        lastSessionHashRef.current = sessionHash;
        setSession(sessionData);
      }
      const nextTotalMessages = result.totalMessages || result.session.messageCount || 0;
      setTotalMessages(nextTotalMessages);

      // Only update messages if the latest page actually changed
      const newMsgs = result.messages || [];
      const newParts = result.parts || [];
      const hash = hashMessagesAndParts(newMsgs, newParts);
      if (hash !== lastHashRef.current) {
        lastHashRef.current = hash;
        // Merge: keep older loaded messages, replace the newest page.
        // Also remove optimistic (temp-*) and error (error-*) messages once real data arrives.
        setMessages(prev => {
          const newIds = new Set(newMsgs.map(m => m.id));
          const older = prev.filter(m => !newIds.has(m.id) && !m.id.startsWith('temp-') && !m.id.startsWith('error-'));
          return [...older, ...newMsgs];
        });
        setParts(prev => {
          const newIds = new Set(newParts.map(p => p.id));
          const older = prev.filter(p => !newIds.has(p.id) && !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'));
          return [...older, ...newParts];
        });
      }
      // Seed the session detail cache so revisits render instantly. The
      // SSE mirror effect keeps it in sync with live updates after this
      // point. See spec/session-switch-cache.
      setCachedSession(id, {
        session: sessionData,
        messages: newMsgs,
        parts: newParts,
        totalMessages: nextTotalMessages,
        contextTokenCount: result.contextTokenCount,
        defaultAgent: result.defaultAgent,
        defaultModel: result.defaultModel,
      });
      setLoadError(null);
    } catch (e) {
      // Silently ignore aborted requests
      if (e instanceof DOMException && e.name === 'AbortError') return;
      console.error('Failed to load session', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load session');
    }
    setLoading(false);
  }, [getSession, id, setCachedSession]);

  // Load older messages (prepend)
  const loadMore = useCallback(async () => {
    if (!id || loadingMore) return;
    const signal = abortControllerRef.current?.signal;
    setLoadingMore(true);
    try {
      const result = await getSession(id, PAGE_SIZE, messages.length + droppedMessageCountRef.current, signal);
      if (signal?.aborted) return;
      const newMsgs = result.messages || [];
      const newParts = result.parts || [];
      if (newMsgs.length) {
        setMessages(prev => {
          const existingIds = new Set(prev.map(m => m.id));
          const unique = newMsgs.filter(m => !existingIds.has(m.id));
          return [...unique, ...prev];
        });
        setParts(prev => {
          const existingIds = new Set(prev.map(p => p.id));
          const unique = newParts.filter(p => !existingIds.has(p.id));
          return [...unique, ...prev];
        });
      }
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    } finally {
      setLoadingMore(false);
    }
  }, [getSession, id, messages.length, loadingMore]);

  // Close picker on outside click
  useEffect(() => {
    if (!pendingTmuxSession) return;
    const handle = (e: MouseEvent) => {
      if (pickerRef.current && !pickerRef.current.contains(e.target as Node)) {
        setPendingTmuxSession(null);
      }
    };
    document.addEventListener('mousedown', handle);
    return () => document.removeEventListener('mousedown', handle);
  }, [pendingTmuxSession]);

  const handleTmuxSwitch = useCallback((e: React.MouseEvent, tmuxSessionName: string) => {
    // Local user: fire directly, server defaults to /dev/ttys000
    if (tmux.isLocal) {
      tmux.switchSession(tmuxSessionName).catch(err => console.error('tmux switch failed', err));
      return;
    }
    // Remote user with single client
    if (tmux.clients.length === 1) {
      tmux.switchSession(tmuxSessionName, tmux.clients[0].tty).catch(err => console.error('tmux switch failed', err));
      return;
    }
    // Remote user with multiple clients: show floating picker
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    setPickerPos({ top: rect.bottom + 4, left: rect.right });
    setPendingTmuxSession(tmuxSessionName);
  }, [tmux]);

  const handleClientSelect = useCallback((clientTTY: string) => {
    if (!pendingTmuxSession) return;
    tmux.switchSession(pendingTmuxSession, clientTTY).catch(err => console.error('tmux switch failed', err));
    setPendingTmuxSession(null);
  }, [pendingTmuxSession, tmux]);

  const handleArchiveSession = useCallback((e: React.MouseEvent, target: Session) => {
    e.stopPropagation();
    if (archivingSessionIds.has(target.id)) return;
    // Capture the current sibling list and the archived session's position
    // from the displayed state, synchronously at click time. Picks the
    // session at `idx + 1` (directly below), or `idx - 1` (directly above)
    // if there's nothing below.
    const isCurrent = target.id === id;
    const idx = recentSessions.findIndex(s => s.id === target.id);
    const nextSession = isCurrent
      ? (recentSessions[idx + 1] ?? recentSessions[idx - 1])
      : undefined;
    setArchivingSessionIds(prev => new Set(prev).add(target.id));
    archiveTimeoutsRef.current[target.id] = window.setTimeout(() => {
      archiveSession(target.platform, target.id, target.timeUpdated, true)
        .then(() => {
          setRecentSessions(prev => showArchivedRecent
            ? prev.map(session => (session.id === target.id ? { ...session, archived: true } : session))
            : prev.filter(session => session.id !== target.id));
          if (isCurrent) {
            navigate(nextSession ? `/session/${nextSession.id}` : '/');
          }
        })
        .catch(err => {
          console.error('Failed to archive session', err);
        })
        .finally(() => {
          setArchivingSessionIds(prev => {
            const next = new Set(prev);
            next.delete(target.id);
            return next;
          });
          delete archiveTimeoutsRef.current[target.id];
        });
    }, ARCHIVE_ANIMATION_MS);
  }, [archiveSession, archivingSessionIds, id, navigate, recentSessions, showArchivedRecent]);

  const handlePinSession = useCallback((e: React.MouseEvent, target: Session) => {
    e.stopPropagation();
    const nextPinned = !target.pinned;

    // Optimistic update
    setRecentSessions(prev => prev.map(s =>
      s.id === target.id
        ? { ...s, pinned: nextPinned, pinnedAt: nextPinned ? Date.now() : 0 }
        : s
    ));

    pinSession(target.platform, target.id, nextPinned).catch(err => {
      console.error('Failed to pin/unpin session', err);
      // Revert on failure
      setRecentSessions(prev => prev.map(s =>
        s.id === target.id
          ? { ...s, pinned: target.pinned, pinnedAt: target.pinnedAt }
          : s
      ));
    });
  }, [pinSession]);

  useEffect(() => () => {
    Object.values(archiveTimeoutsRef.current).forEach(timeoutId => window.clearTimeout(timeoutId));
  }, []);

  // Re-fetch the session-scoped model list. Used on session entry, when
  // OpenCode becomes reachable, and whenever the user opens the model
  // picker (so newly-configured providers / models show up without a page
  // reload). The optional AbortSignal lets callers tied to a useEffect
  // tear down a stale request on cleanup.
  const refreshModels = useCallback((signal?: AbortSignal) => {
    if (!id) return;
    api.sessionModels(id).then((resp) => {
      if (signal?.aborted) return;
      setModelEntries(resp.models || []);
      setModelOptions(
        Array.from(new Set((resp.models || []).map((m) => formatModelRef(m.provider, m.model)))),
      );
    }).catch(() => {
      if (signal?.aborted) return;
      // Fallback: historical-only list. Only seed empties when we don't
      // already have data so a transient picker-open refresh failure
      // doesn't wipe out the catalog the user is currently looking at.
      getModels()
        .then((models) => {
          if (signal?.aborted) return;
          const ordered = [...models]
            .sort((a, b) => b.count - a.count)
            .map((m) => formatModelRef(m.provider, m.model));
          setModelEntries((prev) => prev.length > 0 ? prev : models.map((m) => ({
            provider: m.provider,
            model: m.model,
          })));
          setModelOptions((prev) => prev.length > 0 ? prev : Array.from(new Set(ordered)));
        })
        .catch(() => { /* keep existing data on failure */ });
    });
  }, [id, getModels]);

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
  }, [getWhisperStatus, id, load, refreshModels]);

  // Keep `portAvailable` in sync with the session's live-connection flag
  // (populated by the platform adapter). SSE onopen still flips it to
  // true on a successful connection; this just seeds the initial value
  // from the session payload so the composer isn't disabled for a frame
  // on entry to a live session.
  useEffect(() => {
    if (session?.liveConnection) setPortAvailable(true);
  }, [session?.liveConnection]);

  // Fetch the platform's composer-agent catalog (OpenCode's /agent
  // endpoint when available) so we can color UI by agent. Platforms
  // without an agent catalog return an empty list via the
  // GET /api/session/{id}/agents endpoint, leaving agentColor to fall
  // back to its deterministic defaults.
  useEffect(() => {
    const dir = session?.directory;
    if (!dir) {
      setAgents([]);
      setAgentsLoaded(false);
      return;
    }
    if (!portAvailable) {
      // No OpenCode instance to query — the fallback colors are all we'll ever
      // have, so mark as "loaded" immediately. The composer/UI can apply them
      // without risk of a subsequent color change.
      setAgents([]);
      setAgentsLoaded(true);
      return;
    }
    if (!id) {
      setAgents([]);
      setAgentsLoaded(true);
      return;
    }
    setAgentsLoaded(false);
    const controller = new AbortController();
    api.agents(id, controller.signal)
      .then((list) => {
        if (controller.signal.aborted) return;
        setAgents(list || []);
        setAgentsLoaded(true);
      })
      .catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setAgents([]);
        setAgentsLoaded(true);
      });
    return () => controller.abort();
  }, [id, session?.directory, portAvailable]);

  // Re-fetch the session-scoped model list once OpenCode becomes reachable so
  // the picker picks up the full /config/providers catalog. The initial fetch
  // in the main load effect may have run before discovery completed.
  useEffect(() => {
    if (!id || !portAvailable) return;
    const controller = new AbortController();
    refreshModels(controller.signal);
    return () => controller.abort();
  }, [id, portAvailable, refreshModels]);

  // Toggle a favorite model. Optimistic: the star flips immediately in
  // the picker, then we re-fetch so the authoritative sort (favorites
  // move into the pinned section) comes back from the server. Failures
  // revert the optimistic update.
  const handleToggleFavorite = useCallback(async (provider: string, model: string, nextFavorite: boolean) => {
    if (!session?.platform || !id) return;
    const platform = session.platform;
    setModelEntries((prev) => prev.map((e) =>
      e.provider === provider && e.model === model ? { ...e, isFavorite: nextFavorite } : e,
    ));
    try {
      if (nextFavorite) {
        await api.addFavorite(platform, provider, model);
      } else {
        await api.removeFavorite(platform, provider, model);
      }
      // Re-fetch for authoritative ordering.
      const resp = await api.sessionModels(id);
      setModelEntries(resp.models || []);
    } catch {
      // Revert on error.
      setModelEntries((prev) => prev.map((e) =>
        e.provider === provider && e.model === model ? { ...e, isFavorite: !nextFavorite } : e,
      ));
    }
  }, [session?.platform, id]);

  // Re-inject ghost user-message bubbles for failed sends that survived a
  // refresh. The optimistic messages are component-local (never written to
  // the DB), so on cold load they're absent from `messages` even though
  // the persisted entry is back in `failedSends`. Skip entries whose text
  // already appears as a real user message in the loaded thread — that
  // means the request actually reached the server and SSE delivered it,
  // so the failed banner would be a confusing duplicate.
  useEffect(() => {
    if (!session || failedSends.length === 0) return;
    const existingIds = new Set(messages.map(m => m.id));
    const realUserTexts = new Set(
      messages
        .filter(m => m.data?.role === 'user')
        .flatMap(m => parts
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
      if (e.text && realUserTexts.has(e.text)) return false;
      return true;
    });
    if (ghostsToInject.length === 0) return;

    const newMsgs: Message[] = [];
    const newParts: Part[] = [];
    for (const entry of ghostsToInject) {
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
  }, [session, failedSends, messages, parts]);

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

  const loadRecentSessions = useCallback(async (signal?: AbortSignal) => {
    try {
      const since = Date.now() - SIDEBAR_RECENT_HOURS * 60 * 60 * 1000;
      // /api/sessions can serialize a Go nil slice as JSON `null`; coerce
      // here so .find() / filterVisibleSessions never see null.
      const result = (await getSessions({ since, limit: RECENT_SESSIONS_LIMIT + 5 }, signal)) ?? [];
      if (signal?.aborted) return;
      const visible = (showArchivedRecentRef.current ? result : filterVisibleSessions(result)).slice(0, RECENT_SESSIONS_LIMIT);
      const current = result.find(s => s.id === id);
      const nextRecentSessions = current && !visible.some(s => s.id === current.id)
        ? [current, ...visible].slice(0, RECENT_SESSIONS_LIMIT)
        : visible;
      const hash = nextRecentSessions
        .map(s => `${s.id}|${s.status}|${s.timeUpdated}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}`)
        .join(',');
      if (hash !== lastSiblingsHashRef.current) {
        lastSiblingsHashRef.current = hash;
        setRecentSessions(nextRecentSessions);
      }
      setLoadingRecentSessions(false);
    } catch (e) {
      if (e instanceof DOMException && e.name === 'AbortError') return;
      throw e;
    }
  }, [getSessions, id]);

  const sessionId = session?.id;
  useEffect(() => {
    if (!sessionId) return;
    void loadRecentSessions(abortControllerRef.current?.signal);
  }, [sessionId, loadRecentSessions]);

  const showArchivedRecentMounted = useRef(true);
  useEffect(() => {
    showArchivedRecentRef.current = showArchivedRecent;
    // Skip the initial mount -- the sessionId effect already handles the first load.
    if (showArchivedRecentMounted.current) {
      showArchivedRecentMounted.current = false;
      return;
    }
    void loadRecentSessions(abortControllerRef.current?.signal);
  }, [showArchivedRecent, loadRecentSessions]);

  useEffect(() => {
    let refreshId: number | null = null;
    const start = () => {
      if (refreshId !== null) return;
      refreshId = window.setInterval(() => {
        loadRecentSessions(abortControllerRef.current?.signal).catch(err => console.error('Failed to refresh recent sessions', err));
      }, SIDEBAR_REFRESH_MS);
    };
    const stop = () => {
      if (refreshId === null) return;
      window.clearInterval(refreshId);
      refreshId = null;
    };
    const onVisibility = () => {
      if (document.hidden) {
        stop();
      } else {
        // Fire once immediately on re-focus so the user sees fresh data
        // without waiting a full interval, then resume polling.
        loadRecentSessions(abortControllerRef.current?.signal).catch(err => console.error('Failed to refresh recent sessions', err));
        start();
      }
    };
    if (!document.hidden) start();
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      document.removeEventListener('visibilitychange', onVisibility);
      stop();
    };
  }, [loadRecentSessions]);

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
        lastSiblingsHashRef.current = next
          .map(s => `${s.id}|${s.status}|${s.timeUpdated}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}`)
          .join(',');
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

  // SSE with reconnection
  const [sseActive, setSseActive] = useState(false);
  useEffect(() => {
    if (!session?.id) return;
    const sid = session.id;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    let hasReceivedContentEvent = false; // tracks whether any content event arrived before reconciliation completes
    let hasConnectedOnce = false; // distinguishes the first onopen from reconnects

    // Immediately fetch the latest content from the API.
    const loadNow = () => {
      if (cancelled) return;
      const signal = abortControllerRef.current?.signal;
      load(signal);
    };

    // Process a parsed SSE event: handle permission/question prompts and
    // clear stale prompts. Only triggers state updates when values change.
    const handleParsedEvent = (parsed: Record<string, unknown>) => {
      const type = (parsed.type as string) || '';

      const perm = extractPendingPermission(parsed);
      if (perm) {
        setPendingPermission(perm);
        setPermissionError(null);
      }
      const question = extractPendingQuestion(parsed);
      if (question) {
        const questionSid = question.sessionID || sid;
        storePendingQuestion(questionSid, question);
        if (!question.sessionID || question.sessionID === sid) {
          setPendingQuestion(question);
        }
      }
      // Only clear permission/question state on specific event types,
      // and only if there's something to clear (avoids no-op renders).
      // Do NOT clear on message.updated — that event fires for queued user
      // messages and would prematurely dismiss the permission/question prompt.
      // For permission.replied, only clear if the replied permission matches
      // the currently displayed one — otherwise a back-to-back permission
      // request that was already set by a preceding permission.asked event
      // would be wiped out.
      if (type === 'permission.replied') {
        // OpenCode's permission.replied event uses `requestID` to reference
        // the permission that was answered. `id`/`permissionID` are included
        // as fallbacks for older/alternate payload shapes.
        const props = parsed.properties as Record<string, unknown> | undefined;
        const repliedId =
          (typeof props?.requestID === 'string' && props.requestID) ||
          (typeof props?.requestId === 'string' && props.requestId) ||
          (typeof props?.id === 'string' && props.id) ||
          (typeof props?.permissionID === 'string' && props.permissionID) ||
          '';
        setPendingPermission(prev => {
          if (prev === null) return prev;
          // If we can't identify which permission was replied to, leave state
          // alone — a new permission.asked may have already replaced it.
          if (!repliedId) return prev;
          return prev.permissionId === repliedId ? null : prev;
        });
      } else if (type === 'session.idle' || (type === 'session.status' && isSessionStatusIdle(parsed))) {
        // Only clear on terminal session states. Intermediate session.status
        // events (busy / retry) fire during normal tool execution — including
        // right after a permission is asked — and must NOT wipe the prompt.
        setPendingPermission(prev => prev === null ? prev : null);
      }
      if (
        type === 'question.replied' ||
        type === 'question.rejected' ||
        type === 'session.idle' ||
        (type === 'session.status' && isSessionStatusIdle(parsed))
      ) {
        setPendingQuestion(prev => {
          if (prev === null) return prev;
          clearPendingQuestion(sid);
          return null;
        });
      }
    };

    const connect = () => {
      if (cancelled) return;
      evtSource = new EventSource(`/api/session/${encodeURIComponent(sid)}/events`);
      evtSource.onopen = () => {
        setSseActive(true);
        // SSE connected means OpenCode is running — mark port as available.
        setPortAvailable(true);
        // Fetch any permissions that were already pending when we connected.
        // SSE only delivers new events; existing pending permissions need to
        // be retrieved explicitly so the dialog shows immediately.
        listPermissions(sid).then((perms) => {
          if (cancelled) return;
          for (const p of perms) {
            const perm = extractPendingPermission({ type: 'permission.asked', properties: p });
            if (!perm) continue;
            // Show prompts that belong to the current session OR any of
            // its known subagents (whose prompts must be answered by
            // the user from the parent session page).
            const props = p as Record<string, unknown>;
            const promptSid = typeof props.sessionID === 'string' ? props.sessionID : '';
            if (!isSessionRelevant(promptSid, sid, subagentSessionIdsRef.current)) continue;
            setPendingPermission(perm);
            setPermissionError(null);
            break; // show the first pending permission for this session
          }
        }).catch(() => { /* ignore errors — SSE events will handle live permissions */ });
        // Fetch any questions that were already pending when we connected.
        // Mirrors the permissions recovery above: the question.asked SSE
        // event only fires once, so a user who wasn't viewing the session
        // when it fired would otherwise never see the prompt.
        listQuestions(sid).then((questions) => {
          if (cancelled) return;
          for (const q of questions) {
            const question = extractPendingQuestion({ type: 'question.asked', properties: q });
            if (!question) continue;
            // Show questions belonging to the current session OR any of
            // its known subagents (the response endpoint is keyed on
            // requestID, not session, so any reachable session works).
            const props = q as Record<string, unknown>;
            const questionSid = typeof props.sessionID === 'string' ? props.sessionID : '';
            if (!isSessionRelevant(questionSid, sid, subagentSessionIdsRef.current)) continue;
            storePendingQuestion(sid, question);
            setPendingQuestion((prev) => prev ?? question);
            break; // show the first pending question for this session
          }
        }).catch(() => { /* ignore errors — SSE events will handle live questions */ });
        // Reconciliation: fetch the latest state only when the initial
        // load() failed AND no SSE content events have arrived. In the happy
        // path the initial load in the session-change effect is authoritative
        // and SSE takes over for live updates, so this timer is a no-op.
        setTimeout(() => {
          if (cancelled || hasReceivedContentEvent) return;
          if (!loadErrorRef.current) return;
          const signal = abortControllerRef.current?.signal;
          load(signal);
        }, 500);
        // Reconnect reconciliation: on every reconnect (not the first
        // connection), refetch authoritative session state. SSE is live-only
        // — events emitted while the stream was disconnected are lost, so
        // without this the UI can stay stuck on a stale `busy`/`waiting`
        // status after a turn finished during the gap.
        if (hasConnectedOnce) {
          const signal = abortControllerRef.current?.signal;
          load(signal);
        }
        hasConnectedOnce = true;
      };
      evtSource.onmessage = (evt) => {
        const raw = evt.data || '';
        if (!raw || !raw.trim()) return;

        // Debug logging — only when debug mode is active
        if (debugModeRef.current) {
          setSseDebugEvents((prev) => {
            const next = [...prev, { at: Date.now(), event: 'message', data: truncateSseData(raw) }];
            return next.slice(-10);
          });
        }

        let parsed: Record<string, unknown>;
        try {
          parsed = JSON.parse(raw) as Record<string, unknown>;
        } catch {
          return; // not JSON, ignore
        }

        const type = (parsed.type as string) || '';

        // Filter out events for other sessions. The session ID can live in
        // several places depending on the event type:
        //   properties.info.sessionID  (message.created / message.updated)
        //   properties.part.sessionID  (message.part.updated / message.part.delta)
        //   properties.sessionID       (session.status, part events, questions)
        const evtProps = (parsed.properties && typeof parsed.properties === 'object')
          ? parsed.properties as Record<string, unknown>
          : null;
        const evtSessionId: string | undefined =
          (evtProps?.sessionID as string) ||
          ((evtProps?.info as Record<string, unknown> | undefined)?.sessionID as string) ||
          ((evtProps?.part as Record<string, unknown> | undefined)?.sessionID as string) ||
          undefined;
        const isSubagentEvent =
          !!evtSessionId && evtSessionId !== sid && subagentSessionIdsRef.current.has(evtSessionId);
        if (evtSessionId && evtSessionId !== sid) {
          // Capture token data from subagent sessions for the TPS indicator.
          // Track per-message tokens so we can accurately sum across multiple
          // assistant messages within a subagent session.
          if (isSubagentEvent &&
              (type === 'message.created' || type === 'message.updated')) {
            const subInfo = (evtProps?.info || (evtProps as Record<string, unknown>)) as Record<string, unknown> | undefined;
            if (subInfo && typeof subInfo.id === 'string' && subInfo.role === 'assistant') {
              const msgId = subInfo.id as string;
              const subTokens = subInfo.tokens as { input?: number; output?: number } | undefined;
              const subTime = subInfo.time as { created?: number } | undefined;
              if (subTokens?.output || subTime?.created) {
                setSubagentTokens(prev => {
                  const existing = prev.get(msgId);
                  const output = subTokens?.output || existing?.output || 0;
                  const created = subTime?.created || existing?.created || Date.now();
                  const updated = new Map(prev);
                  updated.set(msgId, {
                    output: Math.max(existing?.output || 0, output),
                    created: existing ? Math.min(existing.created, created) : created,
                  });
                  return trimSubagentTokens(updated);
                });
              }
            }
          }
          // Subagent prompt events bubble up to the parent session UI so
          // the user can answer them. Without this, an OpenCode subagent
          // that hits a permission/question stalls forever — its session
          // is hidden from the listing, so there's no other place to ack.
          if (isSubagentEvent && (
            type === 'permission.asked' ||
            type === 'permission.replied' ||
            type === 'question.asked' ||
            type === 'question.replied' ||
            type === 'question.rejected'
          )) {
            handleParsedEvent(parsed);
          }
          return;
        }

        // Handle permission/question prompts
        handleParsedEvent(parsed);

        // Apply content updates incrementally.
        // OpenCode SSE event types:
        //   message.created  — properties.info + properties.parts (full message)
        //   message.updated  — properties.info (metadata only, no parts)
        //   message.part.updated — properties.part (single part with incremental content)
        //   message.part.delta   — properties.part (text delta to append)
        //   session.status   — properties.status

        const props = evtProps;

        // message.created carries full message + parts
        if (type === 'message.created') {
          const extracted = extractMessageFromEvent(parsed, sid);
          if (extracted) {
            hasReceivedContentEvent = true;
            setMessages(prev => {
              const filtered = prev.filter(
                m => m.id !== extracted.message.id && !m.id.startsWith('temp-') && !m.id.startsWith('error-'),
              );
              const idx = filtered.findIndex(m => m.timeCreated > extracted.message.timeCreated);
              if (idx === -1) return [...filtered, extracted.message];
              return [...filtered.slice(0, idx), extracted.message, ...filtered.slice(idx)];
            });
            if (extracted.parts.length > 0) {
              setParts(prev => {
                const newIds = new Set(extracted.parts.map(p => p.id));
                const filtered = prev.filter(
                  p => !newIds.has(p.id) && !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'),
                );
                return [...filtered, ...extracted.parts];
              });
            }
            setSession(prev => {
              if (!prev) return prev;
              const msg = extracted.message;
              let status: Session['status'] = 'done';
              if (msg.data.role === 'assistant') {
                if (msg.data.finish === 'error' || msg.data.error) status = 'error';
                else if (msg.data.finish) status = 'waiting';
                else status = 'busy';
              }
              if (prev.status === status) return prev;
              return { ...prev, status };
            });
          }
        }

        // message.updated carries metadata (finish, tokens, cost) and may also
        // include parts during streaming depending on the OpenCode version.
        if (type === 'message.updated' && props) {
          // First, try to extract as a full message with parts (some versions bundle them)
          const extracted = extractMessageFromEvent(parsed, sid);
          if (extracted) {
            hasReceivedContentEvent = true;
            setMessages(prev => {
              const filtered = prev.filter(
                m => m.id !== extracted.message.id && !m.id.startsWith('temp-') && !m.id.startsWith('error-'),
              );
              const idx = filtered.findIndex(m => m.timeCreated > extracted.message.timeCreated);
              if (idx === -1) return [...filtered, extracted.message];
              return [...filtered.slice(0, idx), extracted.message, ...filtered.slice(idx)];
            });
            if (extracted.parts.length > 0) {
              setParts(prev => {
                const newIds = new Set(extracted.parts.map(p => p.id));
                const filtered = prev.filter(
                  p => !newIds.has(p.id) && !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'),
                );
                return [...filtered, ...extracted.parts];
              });
            }
            setSession(prev => {
              if (!prev) return prev;
              const msg = extracted.message;
              let status: Session['status'] = 'done';
              if (msg.data.role === 'assistant') {
                if (msg.data.finish === 'error' || msg.data.error) status = 'error';
                else if (msg.data.finish) status = 'waiting';
                else status = 'busy';
              }
              if (prev.status === status) return prev;
              return { ...prev, status };
            });
          } else {
            // Fallback: metadata-only update (no parts in this event)
            const info = props.info as Record<string, unknown> | undefined;
            if (info && info.id) {
              hasReceivedContentEvent = true;
              const msgId = info.id as string;
              setMessages(prev => {
                const idx = prev.findIndex(m => m.id === msgId);
                if (idx < 0) return prev;
                const updated = { ...prev[idx] };
                updated.data = {
                  ...updated.data,
                  finish: info.finish as string | undefined,
                  tokens: info.tokens as Message['data']['tokens'],
                  time: (info.time ?? updated.data.time) as Message['data']['time'],
                  cost: typeof info.cost === 'number' ? info.cost : updated.data.cost,
                  error: info.error as Message['data']['error'],
                };
                const next = [...prev];
                next[idx] = updated;
                return next;
              });
              setSession(prev => {
                if (!prev) return prev;
                let status: Session['status'] = prev.status;
                const role = info.role as string | undefined;
                if (role === 'assistant') {
                  const finish = info.finish as string | undefined;
                  if (finish === 'error' || info.error) status = 'error';
                  else if (finish) status = 'waiting';
                  else status = 'busy';
                }
                if (prev.status === status) return prev;
                return { ...prev, status };
              });
            }
          }
        }

        // message.part.updated — carries the full part content (replacement).
        if (type === 'message.part.updated' && props) {
          const rawPart = props.part as Record<string, unknown> | undefined;
          if (rawPart && rawPart.id && rawPart.messageID) {
            hasReceivedContentEvent = true;
            const partType = rawPart.type as string | undefined;
            if (partType !== 'step-start' && partType !== 'step-finish' && partType !== 'snapshot') {
              // Truncate large fields
              if (typeof rawPart.text === 'string') rawPart.text = truncatePartField(rawPart.text) as string;
              const state = rawPart.state as Record<string, unknown> | undefined;
              if (state) {
                if (typeof state.output === 'string') state.output = truncatePartField(state.output) as string;
                const meta = state.metadata as Record<string, unknown> | undefined;
                if (meta && typeof meta.output === 'string') meta.output = truncatePartField(meta.output) as string;
              }
              const part: Part = {
                id: rawPart.id as string,
                messageId: rawPart.messageID as string,
                sessionId: (rawPart.sessionID as string) || sid,
                data: rawPart as unknown as string,
              };
              setParts(prev => {
                const idx = prev.findIndex(p => p.id === part.id);
                if (idx >= 0) {
                  const updated = [...prev];
                  updated[idx] = part;
                  return updated;
                }
                return [...prev, part];
              });
              // Mark the changes sidebar dirty when an edit/write tool
              // part lands. The hook coalesces successive ticks via
              // its debounce so a busy session firing many edits in
              // quick succession only triggers one re-fetch.
              if (partType === 'tool') {
                const toolName = (rawPart as Record<string, unknown>).tool as string | undefined;
                if (toolName && (
                  toolName === 'edit' || toolName === 'write' ||
                  toolName === 'mcp_edit' || toolName === 'mcp_write' ||
                  toolName === 'mcp_Edit' || toolName === 'mcp_Write'
                )) {
                  setChangesDirtyTick((t) => t + 1);
                }
              }
            }
          }
        }

        // message.part.delta — per-token incremental text during streaming.
        // Shape: { type: "message.part.delta", properties: { sessionID, messageID, partID, field, delta } }
        // The `delta` field contains the new text chunk to append.
        // The `field` indicates which part field is being updated (usually "text").
        if (type === 'message.part.delta' && props) {
          const partId = props.partID as string | undefined;
          const messageId = props.messageID as string | undefined;
          const deltaText = (props.delta as string) || '';
          const field = (props.field as string) || 'text';
          if (partId && messageId && deltaText) {
            hasReceivedContentEvent = true;
            setParts(prev => {
              const idx = prev.findIndex(p => p.id === partId);
              if (idx >= 0) {
                // Append delta to the target field of the existing part.
                // `field` may be a dotted path like "state.output" — handle
                // one level of nesting so tool output streams incrementally.
                const existing = prev[idx];
                let existingData: Record<string, unknown>;
                try {
                  existingData = typeof existing.data === 'string'
                    ? JSON.parse(existing.data) as Record<string, unknown>
                    : existing.data as unknown as Record<string, unknown>;
                } catch {
                  existingData = {};
                }
                let updatedData: Record<string, unknown>;
                const dotIdx = field.indexOf('.');
                if (dotIdx > 0) {
                  const parent = field.slice(0, dotIdx);
                  const child = field.slice(dotIdx + 1);
                  const parentObj = (existingData[parent] as Record<string, unknown> | undefined) || {};
                  const currentVal = (parentObj[child] as string) || '';
                  updatedData = {
                    ...existingData,
                    [parent]: { ...parentObj, [child]: currentVal + deltaText },
                  };
                } else {
                  const currentVal = (existingData[field] as string) || '';
                  updatedData = { ...existingData, [field]: currentVal + deltaText };
                }
                const updated = [...prev];
                updated[idx] = {
                  ...existing,
                  data: updatedData as unknown as string,
                };
                return updated;
              }
              // Part doesn't exist yet — create it with the delta as initial content.
              // This can happen if the message.part.updated for text-start hasn't
              // arrived yet. Use a minimal text part shape.
              const newPart: Part = {
                id: partId,
                messageId,
                sessionId: (props.sessionID as string) || sid,
                data: { type: 'text', [field]: deltaText } as unknown as string,
              };
              return [...prev, newPart];
            });
          }
        }

        // Catch-all for any event carrying part data — handles legacy event
        // names (part.updated, tool.updated) and any unknown event types that
        // still contain renderable part content. We try multiple extraction
        // strategies to be as permissive as possible.
        if (type !== 'message.created' && type !== 'message.updated' && type !== 'message.part.updated' && type !== 'message.part.delta' && type !== 'session.status') {
          let handled = false;

          // Strategy 1: properties contains a part directly
          if (props) {
            const part = extractPartFromEvent(parsed, sid);
            if (part) {
              hasReceivedContentEvent = true;
              handled = true;
              setParts(prev => {
                const idx = prev.findIndex(p => p.id === part.id);
                if (idx >= 0) {
                  const updated = [...prev];
                  updated[idx] = part;
                  return updated;
                }
                return [...prev, part];
              });
            }
          }

          // Strategy 2: properties.part contains the part (like message.part.updated)
          if (!handled && props) {
            const rawPart = props.part as Record<string, unknown> | undefined;
            if (rawPart && rawPart.id && rawPart.messageID) {
              hasReceivedContentEvent = true;
              handled = true;
              const partType = rawPart.type as string | undefined;
              if (partType !== 'step-start' && partType !== 'step-finish' && partType !== 'snapshot') {
                if (typeof rawPart.text === 'string') rawPart.text = truncatePartField(rawPart.text) as string;
                const part: Part = {
                  id: rawPart.id as string,
                  messageId: rawPart.messageID as string,
                  sessionId: (rawPart.sessionID as string) || sid,
                  data: rawPart as unknown as string,
                };
                setParts(prev => {
                  const idx = prev.findIndex(p => p.id === part.id);
                  if (idx >= 0) {
                    const updated = [...prev];
                    updated[idx] = part;
                    return updated;
                  }
                  return [...prev, part];
                });
              }
            }
          }

          // Strategy 3: try as a full message with parts
          if (!handled) {
            const extracted = extractMessageFromEvent(parsed, sid);
            if (extracted) {
              hasReceivedContentEvent = true;
              setMessages(prev => {
                const filtered = prev.filter(
                  m => m.id !== extracted.message.id && !m.id.startsWith('temp-') && !m.id.startsWith('error-'),
                );
                const idx = filtered.findIndex(m => m.timeCreated > extracted.message.timeCreated);
                if (idx === -1) return [...filtered, extracted.message];
                return [...filtered.slice(0, idx), extracted.message, ...filtered.slice(idx)];
              });
              if (extracted.parts.length > 0) {
                setParts(prev => {
                  const newIds = new Set(extracted.parts.map(p => p.id));
                  const filtered = prev.filter(
                    p => !newIds.has(p.id) && !p.id.startsWith('part-temp-') && !p.id.startsWith('part-error-'),
                  );
                  return [...filtered, ...extracted.parts];
                });
              }
            }
          }
        }

        // session.status — update the session status locally.
        if (type === 'session.status' && props) {
          const statusObj = props.status as Record<string, unknown> | string | undefined;
          const status = typeof statusObj === 'string'
            ? statusObj
            : (typeof statusObj === 'object' && statusObj !== null)
              ? (statusObj.type as string | undefined)
              : (props.status as string | undefined);
          if (status === 'waiting' || status === 'busy' || status === 'done' || status === 'error' || status === 'idle') {
            const mapped = status === 'idle' ? 'done' : status;
            setSession(prev => prev && prev.status !== mapped ? { ...prev, status: mapped as Session['status'] } : prev);
            // When the session finishes (idle), fetch the final state from
            // the API to reconcile any events we may have missed.
            if (status === 'idle') {
              loadNow();
            }
          }
        }

        // session.idle — explicit idle signal, fetch final state.
        if (type === 'session.idle') {
          loadNow();
        }

        // message.updated — message metadata changed (tokens, finish, etc.).
        // Also triggers a load to pick up any content not delivered via
        // part events (e.g. the assistant message itself on finish-step).
        if (type === 'message.updated') {
          loadNow();
        }
      };
      // Some OpenCode SSE updates may use named events, not default "message".
      ['question', 'permission', 'approval', 'tool', 'error'].forEach((eventName) => {
        evtSource?.addEventListener(eventName, (evt) => {
          const raw = (evt as MessageEvent).data || '';
          if (!raw) return;
          if (debugModeRef.current) {
            setSseDebugEvents((prev) => {
              const next = [...prev, { at: Date.now(), event: eventName, data: truncateSseData(raw) }];
              return next.slice(-50);
            });
          }
          try {
            const parsed = JSON.parse(raw) as Record<string, unknown>;
            handleParsedEvent(parsed);
          } catch { /* not JSON */ }
        });
      });
      evtSource.onerror = () => {
        setSseActive(false);
        evtSource?.close();
        evtSource = null;
        // Retry after 5 seconds
        if (!cancelled) {
          reconnectTimer = setTimeout(connect, 5000);
        }
      };
    };

    connect();

    // Fallback polling when SSE is not active
    const fallback = setInterval(() => {
      if (!evtSource || evtSource.readyState !== EventSource.OPEN) {
        const signal = abortControllerRef.current?.signal;
        load(signal);
      }
    }, 10000);

    return () => {
      cancelled = true;
      evtSource?.close();
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearInterval(fallback);
      setSseActive(false);
    };
  }, [session?.directory, session?.id, load, listPermissions, listQuestions]);

  // Compute aggregate token/cost stats from the messages array so the header
  // stays up-to-date from SSE events without needing a server round-trip.
  const liveTokens = (() => {
    let tokensIn = 0, tokensOut = 0, tokensReasoning = 0, cacheRead = 0, cacheWrite = 0, totalCost = 0;
    for (const m of messages) {
      if (m.data?.role === 'assistant' && m.data.tokens) {
        tokensIn += m.data.tokens.input || 0;
        tokensOut += m.data.tokens.output || 0;
        tokensReasoning += m.data.tokens.reasoning || 0;
        cacheRead += m.data.tokens.cache?.read || 0;
        cacheWrite += m.data.tokens.cache?.write || 0;
      }
      if (m.data?.role === 'assistant' && m.data.cost) {
        totalCost += m.data.cost;
      }
    }
    return { tokensIn, tokensOut, tokensReasoning, cacheRead, cacheWrite, totalCost };
  })();

  // Use the larger of server-provided totals and locally-computed totals.
  // The server value covers all messages including paginated-out ones;
  // the local value picks up incremental SSE updates before the next load().
  const displayTokensIn = Math.max(session?.totalInputTokens || 0, liveTokens.tokensIn);
  const displayTokensOut = Math.max(session?.totalOutputTokens || 0, liveTokens.tokensOut);
  const tokenStats = {
    input: displayTokensIn,
    output: displayTokensOut,
    reasoning: liveTokens.tokensReasoning,
    cacheRead: liveTokens.cacheRead,
    cacheWrite: liveTokens.cacheWrite,
    totalCost: liveTokens.totalCost,
    contextWindow: session?.contextTokenCount,
  };

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

  const activeModel = ([...messages]
    .reverse()
    .map((m) => formatModelRef(m.data?.providerID, m.data?.modelID))
    .find(Boolean) || session?.defaultModel || '');
  const activeAgent = [...messages].reverse().find(m => !!m.data?.agent)?.data.agent || session?.defaultAgent || '';

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
      handleTmuxShortcutRef.current();
      return;
    }

    if (command === 'vscode') {
      handleVSCodeShortcutRef.current();
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

  const handlePermissionReply = useCallback(async (reply: 'once' | 'always' | 'reject') => {
    if (!pendingPermission || answeringPermission || !portAvailable || !caps.respondPermission || !session) return;
    setPermissionError(null);
    setAnsweringPermission(true);
    const repliedId = pendingPermission.permissionId;
    // OpenCode's permission API is session-scoped: the URL is
    // /session/{id}/permissions/{pid}. When the prompt comes from a
    // subagent of the page session, route the reply to that subagent's
    // session — the parent session knows nothing about the prompt.
    const targetSessionId = pendingPermission.sessionId || session.id;
    try {
      await respondPermission(targetSessionId, repliedId, reply);
      // Only clear the prompt if the currently pending permission is still
      // the one we just replied to. An SSE `permission.asked` event for a
      // follow-up permission may have already arrived while the POST was in
      // flight — clearing unconditionally would hide that new prompt.
      setPendingPermission(prev => (prev && prev.permissionId === repliedId ? null : prev));
      // Drop any global prompt toast pointing at this session — the
      // user just answered. Cross-tab clients still get pruned on
      // their next poll.
      notifyPromptDismissed(targetSessionId);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      setPermissionError(e instanceof Error ? e.message : 'Failed to respond to permission request');
    } finally {
      setAnsweringPermission(false);
    }
  }, [answeringPermission, caps.respondPermission, pendingPermission, portAvailable, respondPermission, session]);

  const handleQuestionReply = useCallback(async (answers: string[][]) => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !caps.respondQuestion || !session) return;
    setQuestionError(null);
    setAnsweringQuestion(true);
    try {
      await respondQuestion(session.id, pendingQuestion.requestId, answers);
      setPendingQuestion(null);
      setQuestionError(null);
      clearPendingQuestion(session.id);
      notifyPromptDismissed(session.id);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to respond to question', e);
      setQuestionError(e instanceof Error ? e.message : 'Failed to submit answer');
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, caps.respondQuestion, pendingQuestion, portAvailable, respondQuestion, session]);

  const handleQuestionReject = useCallback(async () => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !caps.respondQuestion || !session) return;
    setAnsweringQuestion(true);
    try {
      await rejectQuestion(session.id, pendingQuestion.requestId);
      setPendingQuestion(null);
      clearPendingQuestion(session.id);
      notifyPromptDismissed(session.id);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to dismiss question', e);
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, caps.respondQuestion, pendingQuestion, portAvailable, rejectQuestion, session]);

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

  // Find the tmux session whose resolved path matches the current project directory.
  const matchingTmuxSession = session
    ? tmux.findSession(session.directory)
    : undefined;

  const [launchingOpencode, setLaunchingOpencode] = useState(false);
  const handleLaunchOpencode = useCallback(async () => {
    if (!session?.directory || !tmux.available || launchingOpencode) return;
    setLaunchingOpencode(true);
    try {
      await tmux.launchOpencode(session.directory);
    } catch (e) {
      console.error('Failed to launch opencode in tmux', e);
    } finally {
      setLaunchingOpencode(false);
    }
  }, [launchingOpencode, session?.directory, tmux]);

  const handleTmuxShortcut = useCallback(() => {
    if (!matchingTmuxSession) return;
    if (tmux.isLocal) {
      tmux.switchSession(matchingTmuxSession.name).catch(err => console.error('tmux switch failed', err));
      return;
    }
    if (tmux.clients.length === 1) {
      tmux.switchSession(matchingTmuxSession.name, tmux.clients[0].tty).catch(err => console.error('tmux switch failed', err));
      return;
    }

    setPickerPos({ top: 88, left: Math.min(window.innerWidth - 24, 420) });
    setPendingTmuxSession(matchingTmuxSession.name);
  }, [matchingTmuxSession, tmux]);

  const handleVSCodeShortcut = useCallback(() => {
    if (!session) return;
    openVSCode(session.directory);
  }, [session]);

  const recentSessionsRef = useRef<Session[]>([]);
  useEffect(() => { recentSessionsRef.current = recentSessions; }, [recentSessions]);

  // Alt+J / Alt+K: navigate between recent sessions. Handlers read from refs
  // so they can capture the latest recentSessions without re-registering.
  const jumpToSession = useCallback((direction: 1 | -1) => {
    const sessions = recentSessionsRef.current;
    const currentIndex = sessions.findIndex((s) => s.id === id);
    if (currentIndex === -1) return;
    const target = sessions[currentIndex + direction];
    if (target) navigate(`/session/${target.id}`);
  }, [id, navigate]);

  const navNextShortcut = useMemo(() => ({
    id: 'session.nav-next',
    scope: 'session' as const,
    keys: { code: 'KeyJ', alt: true },
    description: 'Go to next session',
    handler: () => jumpToSession(1),
  }), [jumpToSession]);

  const navPrevShortcut = useMemo(() => ({
    id: 'session.nav-prev',
    scope: 'session' as const,
    keys: { code: 'KeyK', alt: true },
    description: 'Go to previous session',
    handler: () => jumpToSession(-1),
  }), [jumpToSession]);

  useShortcut(navNextShortcut);
  useShortcut(navPrevShortcut);

  const handleTmuxShortcutRef = useRef(handleTmuxShortcut);
  useEffect(() => { handleTmuxShortcutRef.current = handleTmuxShortcut; }, [handleTmuxShortcut]);
  const handleVSCodeShortcutRef = useRef(handleVSCodeShortcut);
  useEffect(() => { handleVSCodeShortcutRef.current = handleVSCodeShortcut; }, [handleVSCodeShortcut]);
  const handleNewSessionRef = useRef(handleNewSession);
  useEffect(() => { handleNewSessionRef.current = handleNewSession; }, [handleNewSession]);
  const matchingTmuxSessionRef = useRef(matchingTmuxSession);
  useEffect(() => { matchingTmuxSessionRef.current = matchingTmuxSession; }, [matchingTmuxSession]);
  const sessionRef = useRef(session);
  useEffect(() => { sessionRef.current = session; }, [session]);
  const portAvailableRef = useRef(portAvailable);
  useEffect(() => { portAvailableRef.current = portAvailable; }, [portAvailable]);
  const selectedModelRef = useRef(selectedModel);
  useEffect(() => { selectedModelRef.current = selectedModel; }, [selectedModel]);
  const activeModelRef = useRef(activeModel);
  useEffect(() => { activeModelRef.current = activeModel; }, [activeModel]);
  const capsRef = useRef(caps);
  useEffect(() => { capsRef.current = caps; }, [caps]);

  const switchTmuxShortcut = useMemo(() => ({
    id: 'session.switch-tmux',
    scope: 'session' as const,
    keys: { code: 'KeyT', alt: true },
    description: 'Switch tmux for current session',
    enabled: () => !!matchingTmuxSessionRef.current,
    handler: () => handleTmuxShortcutRef.current(),
  }), []);

  const openVscodeShortcut = useMemo(() => ({
    id: 'session.open-vscode',
    scope: 'session' as const,
    keys: { code: 'KeyV', alt: true },
    description: 'Open current session in VS Code',
    enabled: () => !!sessionRef.current,
    handler: () => handleVSCodeShortcutRef.current(),
  }), []);

  const newSessionShortcut = useMemo(() => ({
    id: 'session.new-session',
    scope: 'session' as const,
    keys: { code: 'KeyC', alt: true },
    description: 'Create new session in current project',
    enabled: () => !!sessionRef.current && portAvailableRef.current,
    handler: () => handleNewSessionRef.current(),
  }), []);

  useShortcut(switchTmuxShortcut);
  useShortcut(openVscodeShortcut);
  useShortcut(newSessionShortcut);

  const changeModelShortcut = useMemo(() => ({
    id: 'session.change-model',
    scope: 'session' as const,
    keys: { code: 'KeyM', alt: true },
    description: 'Change model via palette',
    handler: () => {
      const el = document.querySelector('.oc-composer-input') as HTMLTextAreaElement | null;
      if (el) {
        el.value = '/model ';
        el.dispatchEvent(new CustomEvent('oc-model-picker-open', { detail: '' }));
        el.focus();
      }
    },
  }), []);

  useShortcut(changeModelShortcut);

  const hasMore = messages.length < totalMessages;
  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  const composerModels = Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model)));
  const showSseNotice = portAvailable && !sseActive;
  const showSseDebug = debugMode && sseDebugEvents.length > 0;
  // Keep a ref so the SSE handler can access the latest subagent IDs without
  // needing to be in the useEffect dependency list (which would reconnect SSE).
  const subagentSessionIdsRef = useRef<Set<string>>(new Set());

  // Derive subagent session IDs from task/mcp_task tool call parts.
  // These are needed to capture SSE token events from subagent sessions.
  const subagentSessionIds = useMemo(() => {
    const ids = new Set<string>();
    for (const p of parts) {
      const d = typeof p.data === 'string' ? (() => { try { return JSON.parse(p.data); } catch { return null; } })() : p.data;
      if (!d || typeof d !== 'object') continue;
      const toolName = (d as Record<string, unknown>).tool as string | undefined;
      if (toolName !== 'task' && toolName !== 'mcp_task' && toolName !== 'Task' && toolName !== 'mcp_Task') continue;
      const st = (d as Record<string, unknown>).state as Record<string, unknown> | undefined;
      const inp = st?.input as Record<string, unknown> | undefined;
      // Extract task_id from various locations (same logic as OcmanRuntimeProvider)
      let taskId = '';
      if (inp && typeof inp.task_id === 'string') taskId = inp.task_id;
      if (!taskId && st) {
        const outputStr = typeof st.output === 'string' ? st.output : JSON.stringify(st.output || '');
        const idMatch = outputStr.match(/task_id:\s*(ses_[^\s)]+)/);
        if (idMatch) taskId = idMatch[1];
        if (!taskId && st.output && typeof st.output === 'object') {
          const out = st.output as Record<string, unknown>;
          if (typeof out.task_id === 'string') taskId = out.task_id;
        }
        if (!taskId && st.metadata) {
          const meta = st.metadata as Record<string, unknown>;
          if (typeof meta.sessionId === 'string') taskId = meta.sessionId;
          else if (typeof meta.taskId === 'string') taskId = meta.taskId;
          else if (typeof meta.task_id === 'string') taskId = meta.task_id;
        }
      }
      if (taskId) ids.add(taskId);
    }
    return ids;
  }, [parts]);
  subagentSessionIdsRef.current = subagentSessionIds;

  // Derive running task IDs and their live output from task tool calls.
  // While a task runs, we poll its session to get stdout for live preview.
  const runningTaskIds = useMemo(() => {
    const running: { taskId: string; status: string }[] = [];
    for (const p of parts) {
      const d = typeof p.data === 'string' ? (() => { try { return JSON.parse(p.data); } catch { return null; } })() : p.data;
      if (!d || typeof d !== 'object') continue;
      const toolName = (d as Record<string, unknown>).tool as string | undefined;
      if (toolName !== 'task' && toolName !== 'mcp_task' && toolName !== 'Task' && toolName !== 'mcp_Task') continue;
      const st = (d as Record<string, unknown>).state as Record<string, unknown> | undefined;
      const inp = st?.input as Record<string, unknown> | undefined;
      const status = (st?.status as string) || 'running';
      if (status !== 'running') continue; // only track running tasks
      let taskId = '';
      if (inp && typeof inp.task_id === 'string') taskId = inp.task_id;
      if (!taskId && st?.output && typeof st.output === 'object') {
        const out = st.output as Record<string, unknown>;
        if (typeof out.task_id === 'string') taskId = out.task_id;
      }
      if (taskId) running.push({ taskId, status });
    }
    return running;
  }, [parts]);

  // Poll running task sessions every 2s for live stdout output.
  // Fetches the task's session messages and extracts stdout from tool outputs.
  useEffect(() => {
    if (runningTaskIds.length === 0) return;
    const controller = new AbortController();
    const taskIdList = runningTaskIds.map(({ taskId }) => taskId);
    const poll = async () => {
      try {
        const resp = await api.sessionTasks(id!, taskIdList, controller.signal);
        if (controller.signal.aborted) return;
        const tasks = resp.tasks || {};
        if (Object.keys(tasks).length > 0) {
          setTaskLiveOutput((prev: Record<string, string>) => ({ ...prev, ...tasks }));
        }
      } catch { /* ignore poll errors */ }
    };
    poll();
    const interval = setInterval(poll, 2000);
    return () => { controller.abort(); clearInterval(interval); };
  }, [runningTaskIds.length]); // only depends on count, not contents

  // The assistant is still working if:
  // - the last message is from the user (assistant hasn't replied yet), or
  // - the last message is from the assistant with no finish reason and no error (still streaming).
  // Once finish is set to any value ("stop", "tool-calls", etc.), that turn is done.
  // A message with an error object is also not running.
  const isRunning = lastMsg
    ? lastMsg.data?.role === 'user' || (lastMsg.data?.role === 'assistant' && !lastMsg.data?.finish && !lastMsg.data?.error)
    : false;

  // Mirror the current session's status from SSE-driven message state into the
  // sidebar list entry so its status dot starts/stops pulsing immediately when
  // a turn begins or ends, without waiting for the 10-second poll to
  // /api/sessions. The derivation mirrors internal/db/types.go exactly so what
  // we set optimistically matches what the next poll will confirm (no flicker).
  // Raw status derived from the last message — may flicker between
  // "busy" and "waiting" during tool-call turn boundaries when the
  // agent finishes one turn and immediately starts another.
  const rawOptimisticStatus: Session['status'] = (() => {
    if (!lastMsg) return 'done';
    const data = lastMsg.data;
    if (data?.role !== 'assistant') return 'done';
    if (data?.finish === 'error' || data?.error) return 'error';
    if (data?.finish) return 'waiting';
    return 'busy';
  })();

  // Debounced status: when transitioning from "busy" to "waiting",
  // hold "busy" for a grace period (3 s) before committing. If the
  // agent starts a new turn within that window the "waiting" flash
  // is suppressed entirely. Transitions to "error", "done", or
  // "busy" are applied immediately.
  const [optimisticStatus, setOptimisticStatus] = useState(rawOptimisticStatus);
  const statusGraceRef = useRef<number | null>(null);
  useEffect(() => {
    // Clear any pending grace timer on unmount.
    return () => { if (statusGraceRef.current !== null) window.clearTimeout(statusGraceRef.current); };
  }, []);
  useEffect(() => {
    if (rawOptimisticStatus === optimisticStatus) {
      // Already in sync — clear any pending grace timer.
      if (statusGraceRef.current !== null) {
        window.clearTimeout(statusGraceRef.current);
        statusGraceRef.current = null;
      }
      return;
    }
    // Busy → waiting: delay the transition so tool-call gaps don't flicker.
    if (optimisticStatus === 'busy' && rawOptimisticStatus === 'waiting') {
      if (statusGraceRef.current !== null) return; // timer already running
      statusGraceRef.current = window.setTimeout(() => {
        statusGraceRef.current = null;
        setOptimisticStatus(rawOptimisticStatus);
      }, 3000);
      return;
    }
    // All other transitions: apply immediately.
    if (statusGraceRef.current !== null) {
      window.clearTimeout(statusGraceRef.current);
      statusGraceRef.current = null;
    }
    setOptimisticStatus(rawOptimisticStatus);
  }, [rawOptimisticStatus, optimisticStatus]);

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
        lastSiblingsHashRef.current = next
          .map(s => `${s.id}|${s.status}|${s.timeUpdated}|${s.pendingPermission ? 'p' : ''}${s.pendingQuestion ? 'q' : ''}`)
          .join(',');
        return next;
      }
      return prev;
    });
  }, [id, optimisticStatus]);

  // Live tokens-per-second: sum output tokens across all assistant messages in the
  // current run window (since the last user message) plus tokens from subagent
  // sessions, divided by the sum of per-message LLM durations.
  //
  // We sum per-message durations (time.completed - time.created for finished
  // messages, Date.now() - time.created for in-flight ones) instead of using
  // wall-clock elapsed from the earliest message. This excludes idle time
  // between messages (tool execution, etc.). When a permission or question
  // prompt is pending, in-flight messages are excluded entirely since the
  // LLM isn't generating — this prevents user think time from deflating TPS.
  const [liveTokensPerSecond, setLiveTokensPerSecond] = useState<number | null>(null);
  useEffect(() => {
    if (!isRunning) {
      setLiveTokensPerSecond(null);
      // Clear subagent token tracking when the run ends so the next run starts fresh.
      setSubagentTokens(prev => prev.size > 0 ? new Map() : prev);
      return;
    }
    const computeTps = () => {
      // Find the start of the current run window: the index after the last user message.
      let windowStart = 0;
      for (let i = messages.length - 1; i >= 0; i--) {
        if (messages[i].data?.role === 'user') { windowStart = i + 1; break; }
      }
      // Sum per-message output tokens and LLM durations across all assistant
      // messages in the window. For completed messages we use the stored
      // time.completed; for still-streaming messages we use Date.now() so the
      // rate updates live while a response is being generated.
      //
      // When a permission or question prompt is pending, the in-flight
      // message's duration would include the user's think time (which can
      // be tens of seconds). We freeze the in-flight message's end time
      // to avoid inflating the denominator with idle wait time.
      const promptPending = pendingPermission !== null || pendingQuestion !== null;
      let totalOutput = 0;
      let totalDurationMs = 0;
      const now = Date.now();
      for (let i = windowStart; i < messages.length; i++) {
        const m = messages[i];
        if (m.data?.role !== 'assistant') continue;
        const created = m.data.time?.created;
        if (!created) continue;
        const output = m.data.tokens?.output || 0;
        const completed = m.data.time?.completed;
        const isInFlight = !completed || completed <= created;
        // Skip in-flight messages entirely when a prompt is pending —
        // the LLM isn't generating, so including Date.now() would
        // artificially deflate the TPS.
        if (isInFlight && promptPending) continue;
        const endTime = isInFlight ? now : completed;
        const durationMs = endTime - created;
        if (durationMs <= 0) continue;
        totalOutput += output;
        totalDurationMs += durationMs;
      }
      // Include output tokens and durations from subagent sessions (captured
      // via SSE). Subagent entries only track `created`, so we always treat
      // them as in-flight and use now - created for the duration.
      for (const entry of subagentTokens.values()) {
        const durationMs = now - entry.created;
        if (durationMs <= 0) continue;
        totalOutput += entry.output;
        totalDurationMs += durationMs;
      }
      if (totalOutput > 0 && totalDurationMs > 100) {
        setLiveTokensPerSecond(totalOutput / (totalDurationMs / 1000));
        return;
      }
      setLiveTokensPerSecond(null);
    };
    computeTps();
    const interval = setInterval(computeTps, 1000);
    return () => clearInterval(interval);
  }, [isRunning, messages, subagentTokens, pendingPermission, pendingQuestion]);

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

    type Aggregate =
      | { kind: 'none' }
      | { kind: 'waiting'; count: number }
      | { kind: 'busy'; count: number }
      | { kind: 'error'; count: number }
      | { kind: 'pending'; count: number };

    const rollup = (sessions: Session[]): Aggregate => {
      let pending = 0;
      let error = 0;
      let busy = 0;
      let waiting = 0;
      for (const s of sessions) {
        const effectiveStatus = s.id === id ? optimisticStatus : s.status;
        if (s.pendingPermission || s.pendingQuestion) {
          pending += 1;
          continue;
        }
        if (effectiveStatus === 'error' && !s.seen) {
          error += 1;
          continue;
        }
        if (effectiveStatus === 'busy') {
          busy += 1;
          continue;
        }
        if (effectiveStatus === 'waiting' && !s.seen) {
          waiting += 1;
        }
      }
      if (pending > 0) return { kind: 'pending', count: pending };
      if (error > 0) return { kind: 'error', count: error };
      if (busy > 0) return { kind: 'busy', count: busy };
      if (waiting > 0) return { kind: 'waiting', count: waiting };
      return { kind: 'none' };
    };

    const groups: { directory: string; sessions: Session[]; lastUpdated: number; aggregate: Aggregate; isPinned?: boolean }[] = Array.from(buckets.entries()).map(([directory, sessions]) => {
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

  // Collapsed state as a Set for O(1) membership checks in render. The current
  // session's project is force-expanded regardless of persisted state so the
  // user can always see where they are.
  const collapsedProjectSet = useMemo(() => {
    const set = new Set(collapsedProjects);
    // Force the current session's group expanded so the user always
    // sees where they are. Groups are keyed by project root (so
    // worktrees of the same repo live under one parent), so look the
    // current session's directory up under the same fold.
    const currentDir = recentSessions.find(s => s.id === id)?.directory;
    if (currentDir) {
      set.delete(currentDir); // legacy keys persisted before fold
      set.delete(projectRootForDirectory(currentDir));
    }
    return set;
  }, [collapsedProjects, recentSessions, id]);

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
