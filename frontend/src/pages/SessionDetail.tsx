import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import { useHotkeys } from 'react-hotkeys-hook';
import { api, type Session, type SessionDetail as SessionDetailData, type Message, type Part } from '../lib/api';
import { formatDuration, formatNumber, shortPath, relativeTime } from '../lib/format';
import { useHeaderInfo, usePageTitle } from '../lib/headerContext';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import { AssistantThread, Composer, type AttachedImage } from '../components/AssistantThread';
import { StatusBadge } from '../components/StatusBadge';
import { useTmux } from '../lib/useTmux';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { useApiStore } from '../lib/apiStore';
import { openVSCode } from '../lib/shortcuts';

const PAGE_SIZE = 50;
const RECENT_SESSIONS_LIMIT = 20;
const ARCHIVE_ANIMATION_MS = 220;

// Maximum length for part text/output before truncation (matches backend maxOutputLen).
const MAX_OUTPUT_LEN = 10000;

/** Truncate large string fields in a part to keep memory usage manageable. */
function truncatePartField(value: unknown): unknown {
  if (typeof value === 'string' && value.length > MAX_OUTPUT_LEN) {
    return value.slice(0, MAX_OUTPUT_LEN) + '\n... (truncated)';
  }
  return value;
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

  return { permissionId: id, permission, patterns };
}

interface QuestionOption {
  label: string;
  description: string;
}

interface QuestionItem {
  question: string;
  header: string;
  options: QuestionOption[];
  multiple?: boolean;
  custom?: boolean;
}

interface PendingQuestion {
  requestId: string;
  sessionID: string;
  questions: QuestionItem[];
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
    '';
  if (!id) return null;

  const sessionID = typeof properties.sessionID === 'string' ? properties.sessionID : '';

  const rawQuestions = properties.questions;
  if (!Array.isArray(rawQuestions) || rawQuestions.length === 0) return null;

  const questions = rawQuestions.filter(
    (q): q is QuestionItem =>
      !!q && typeof q === 'object' && typeof (q as QuestionItem).question === 'string',
  );
  if (questions.length === 0) return null;

  return { requestId: id, sessionID, questions };
}

/** Check if loaded parts contain a pending (unanswered) question tool call. */
function hasPendingQuestionInParts(parts: Part[]): boolean {
  const questionToolNames = ['question', 'mcp_question', 'Question', 'mcp_Question'];

  for (let i = parts.length - 1; i >= 0; i--) {
    const p = parts[i];
    let pd: Record<string, unknown>;
    try {
      pd = typeof p.data === 'string' ? JSON.parse(p.data) : (p.data as unknown as Record<string, unknown>);
    } catch {
      continue;
    }
    if (pd.type !== 'tool') continue;
    const toolName = pd.tool as string | undefined;
    if (!toolName || !questionToolNames.includes(toolName)) continue;

    const state = pd.state as Record<string, unknown> | undefined;
    if (!state) continue;

    const status = state.status as string | undefined;
    const output = state.output;
    const hasOutput = output != null && output !== '' && output !== '""' && output !== '[]';
    if (status === 'running' || !hasOutput) return true;
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

function QuestionPrompt({
  question,
  onReply,
  onReject,
  disabled,
  error,
}: {
  question: PendingQuestion;
  onReply: (answers: string[][]) => void;
  onReject: () => void;
  disabled?: boolean;
  error?: string | null;
}) {
  const [selectedIndices, setSelectedIndices] = useState<Record<number, number | null>>({});
  const [customTexts, setCustomTexts] = useState<Record<number, string>>({});
  const [currentStep, setCurrentStep] = useState(0);

  const totalSteps = question.questions.length;
  const isStepped = totalSteps > 1;

  const isStepAnswered = (qi: number) => {
    const sel = selectedIndices[qi];
    const custom = customTexts[qi]?.trim();
    const q = question.questions[qi];
    return (sel != null && sel >= 0 && (!q || sel < q.options.length)) || !!custom;
  };

  const handleOptionClick = (qi: number, oi: number) => {
    if (disabled) return;
    setSelectedIndices(prev => ({ ...prev, [qi]: oi }));
    setCustomTexts(prev => ({ ...prev, [qi]: '' }));
  };

  const handleCustomFocus = (qi: number) => {
    setSelectedIndices(prev => ({ ...prev, [qi]: null }));
  };

  const getAnswers = (): string[][] =>
    question.questions.map((q, qi) => {
      const sel = selectedIndices[qi];
      const custom = customTexts[qi]?.trim();
      if (sel != null && sel >= 0 && sel < q.options.length) {
        return [q.options[sel].label];
      }
      if (custom) return [custom];
      return [];
    });

  const handleSubmit = () => {
    if (disabled) return;
    const answers = getAnswers();
    if (answers.every(a => a.length > 0)) {
      onReply(answers);
    }
  };

  const handleNext = () => {
    if (currentStep < totalSteps - 1) {
      setCurrentStep(currentStep + 1);
    }
  };

  const handlePrev = () => {
    if (currentStep > 0) {
      setCurrentStep(currentStep - 1);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onReject();
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      if (isStepped && currentStep < totalSteps - 1) {
        if (isStepAnswered(currentStep)) handleNext();
      } else {
        handleSubmit();
      }
    }
  };

  const allAnswered = getAnswers().every(a => a.length > 0);
  const isLastStep = currentStep === totalSteps - 1;

  const renderStep = (qi: number) => {
    const q = question.questions[qi];
    return (
      <div key={qi} className="oc-question-box">
        {isStepped && (
          <div className="oc-question-step-indicator">
            {question.questions.map((_, si) => (
              <button
                key={si}
                type="button"
                className={`oc-question-step-dot${si === currentStep ? ' oc-question-step-active' : ''}${isStepAnswered(si) ? ' oc-question-step-done' : ''}`}
                onClick={() => setCurrentStep(si)}
                disabled={disabled}
                title={`Question ${si + 1}`}
              />
            ))}
            <span className="oc-question-step-label">{currentStep + 1} / {totalSteps}</span>
          </div>
        )}
        <div className="oc-question-box-text">{q.question}</div>
        <div className="oc-question-box-options">
          {q.options.map((opt, oi) => (
            <button
              key={oi}
              type="button"
              className={`oc-question-opt-btn${selectedIndices[qi] === oi ? ' oc-question-opt-selected' : ''}`}
              onClick={() => handleOptionClick(qi, oi)}
              disabled={disabled}
            >
              <span className="oc-question-opt-num">{oi + 1}.</span>
              <span className="oc-question-opt-content">
                <span className="oc-question-opt-label">{opt.label}</span>
                {opt.description && (
                  <span className="oc-question-opt-desc">{opt.description}</span>
                )}
              </span>
            </button>
          ))}
          <div className={`oc-question-opt-custom${selectedIndices[qi] === null && customTexts[qi]?.trim() ? ' oc-question-opt-custom-active' : ''}`}>
            <span className="oc-question-opt-num">{q.options.length + 1}.</span>
            <input
              type="text"
              className="oc-question-inline-input"
              placeholder="Type your own answer"
              value={customTexts[qi] || ''}
              onChange={(e) => setCustomTexts(prev => ({ ...prev, [qi]: e.target.value }))}
              onFocus={() => handleCustomFocus(qi)}
              disabled={disabled}
            />
          </div>
        </div>
        <div className="oc-question-box-actions">
          {isStepped && currentStep > 0 && (
            <button
              type="button"
              className="oc-question-dismiss-btn"
              onClick={handlePrev}
              disabled={disabled}
            >Back</button>
          )}
          {isStepped && !isLastStep ? (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={handleNext}
              disabled={disabled || !isStepAnswered(currentStep)}
            >Next</button>
          ) : (
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={handleSubmit}
              disabled={disabled || !allAnswered}
            >Submit</button>
          )}
          <button
            type="button"
            className="oc-question-dismiss-btn"
            onClick={onReject}
            disabled={disabled}
          >Dismiss</button>
          <span className="oc-question-keys">
            <kbd>enter</kbd> {isStepped && !isLastStep ? 'next' : 'submit'} &middot; <kbd>esc</kbd> dismiss
          </span>
        </div>
        {error && (
          <div className="oc-question-error">{error}</div>
        )}
      </div>
    );
  };

  return (
    <div className="oc-question-wrap" onKeyDown={handleKeyDown}>
      {renderStep(currentStep)}
    </div>
  );
}

export function SessionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
  const debugModeRef = useRef(debugMode);
  debugModeRef.current = debugMode;
  const [session, setSession] = useState<(SessionDetailData['session'] & { defaultAgent?: string; defaultModel?: string }) | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [parts, setParts] = useState<Part[]>([]);
  const [totalMessages, setTotalMessages] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [portAvailable, setPortAvailable] = useState(false);
  const [whisperAvailable, setWhisperAvailable] = useState(false);
  const [modelOptions, setModelOptions] = useState<string[]>([]);
  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [recentSessions, setRecentSessions] = useState<Session[]>([]);

  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [showArchivedRecent, setShowArchivedRecent] = useState(false);
  const { setInfo } = useHeaderInfo();
  usePageTitle(session?.title || 'Session');
  const lastHashRef = useRef('');
  const lastSessionHashRef = useRef('');
  const lastSiblingsHashRef = useRef('');
  const archiveTimeoutsRef = useRef<Record<string, number>>({});
  const abortControllerRef = useRef<AbortController | null>(null);
  const showArchivedRecentRef = useRef(showArchivedRecent);

  // Tmux state
  const tmux = useTmux();
  const [pendingTmuxSession, setPendingTmuxSession] = useState<string | null>(null);
  const [pickerPos, setPickerPos] = useState<{ top: number; left: number } | null>(null);
  const pickerRef = useRef<HTMLDivElement>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [answeringPermission, setAnsweringPermission] = useState(false);
  const [pendingPermission, setPendingPermission] = useState<PendingPermission | null>(null);
  const [permissionError, setPermissionError] = useState<string | null>(null);
  const [pendingQuestion, setPendingQuestion] = useState<PendingQuestion | null>(null);
  const [answeringQuestion, setAnsweringQuestion] = useState(false);
  const [questionError, setQuestionError] = useState<string | null>(null);
  const [sseDebugEvents, setSseDebugEvents] = useState<SseDebugEvent[]>([]);
  const getSession = useApiStore((state) => state.getSession);
  const archiveSession = useApiStore((state) => state.archiveSession);
  const getSessionPort = useApiStore((state) => state.getSessionPort);
  const getWhisperStatus = useApiStore((state) => state.getWhisperStatus);
  const getModels = useApiStore((state) => state.getModels);
  const getSessions = useApiStore((state) => state.getSessions);
  const markSessionSeen = useApiStore((state) => state.markSessionSeen);
  const sendMessage = useApiStore((state) => state.sendMessage);
  const respondPermission = useApiStore((state) => state.respondPermission);
  const respondQuestion = useApiStore((state) => state.respondQuestion);
  const rejectQuestion = useApiStore((state) => state.rejectQuestion);
  const createSession = useApiStore((state) => state.createSession);


  useEffect(() => {
    showArchivedRecentRef.current = showArchivedRecent;
  }, [showArchivedRecent]);

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
      const sessionHash = JSON.stringify({ id: sessionData.id, status: sessionData.status, title: sessionData.title, ctx: sessionData.contextTokenCount, agent: sessionData.defaultAgent, model: sessionData.defaultModel });
      if (sessionHash !== lastSessionHashRef.current) {
        lastSessionHashRef.current = sessionHash;
        setSession(sessionData);
      }
      setTotalMessages(result.totalMessages || result.session.messageCount || 0);

      // Only update messages if the latest page actually changed
      const newMsgs = result.messages || [];
      const newParts = result.parts || [];
      // Include message IDs + part IDs and data to detect content changes
      // (e.g., tool call status updates, new text in parts)
      const hash = newMsgs.map(m => m.id + ':' + m.timeCreated).join(',')
        + '|' + newParts.map(p => p.id + ':' + JSON.stringify(p.data)).join(',');
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
      setLoadError(null);
    } catch (e) {
      // Silently ignore aborted requests
      if (e instanceof DOMException && e.name === 'AbortError') return;
      console.error('Failed to load session', e);
      setLoadError(e instanceof Error ? e.message : 'Failed to load session');
    }
    setLoading(false);
  }, [getSession, id]);

  // Load older messages (prepend)
  const loadMore = useCallback(async () => {
    if (!id || loadingMore) return;
    const signal = abortControllerRef.current?.signal;
    setLoadingMore(true);
    try {
      const result = await getSession(id, PAGE_SIZE, messages.length, signal);
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
    setArchivingSessionIds(prev => new Set(prev).add(target.id));
    archiveTimeoutsRef.current[target.id] = window.setTimeout(() => {
      archiveSession(target.id, target.timeUpdated, true)
        .then(() => {
          setRecentSessions(prev => showArchivedRecent
            ? prev.map(session => (session.id === target.id ? { ...session, archived: true } : session))
            : prev.filter(session => session.id !== target.id));
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
  }, [archiveSession, archivingSessionIds, showArchivedRecent]);

  useEffect(() => () => {
    Object.values(archiveTimeoutsRef.current).forEach(timeoutId => window.clearTimeout(timeoutId));
  }, []);

  // Reset on session change — abort any in-flight requests from the previous session
  useEffect(() => {
    // Abort previous session's pending requests
    abortControllerRef.current?.abort();
    const controller = new AbortController();
    abortControllerRef.current = controller;
    const signal = controller.signal;

    lastHashRef.current = '';
    lastSessionHashRef.current = '';
    setSession(null);
    setMessages([]);
    setParts([]);
    setTotalMessages(0);
    setLoading(true);
    setPortAvailable(false);
    setSelectedModel('');
    setSelectedAgent('');
    setPendingPermission(null);
    setPermissionError(null);
    setPendingQuestion(null);
    setSseDebugEvents([]);
    load(signal);
    if (id) {
      getSessionPort(id, signal).then(p => {
        if (!signal.aborted) setPortAvailable(p.available);
      }).catch((e) => {
        if (e instanceof DOMException && e.name === 'AbortError') return;
        setPortAvailable(false);
      });
    }
    getWhisperStatus().then(s => setWhisperAvailable(s.available)).catch(() => setWhisperAvailable(false));
    getModels()
      .then((models) => {
        const ordered = [...models]
          .sort((a, b) => b.count - a.count)
          .map((m) => formatModelRef(m.provider, m.model));
        setModelOptions(Array.from(new Set(ordered)));
      })
      .catch(() => setModelOptions([]));

    return () => controller.abort();
  }, [getModels, getSessionPort, getWhisperStatus, id, load]);

  const loadRecentSessions = useCallback(async (signal?: AbortSignal) => {
    try {
      const result = await getSessions(undefined, signal);
      if (signal?.aborted) return;
      const visible = (showArchivedRecentRef.current ? result : filterVisibleSessions(result)).slice(0, RECENT_SESSIONS_LIMIT);
      const current = result.find(s => s.id === id);
      const nextRecentSessions = current && !visible.some(s => s.id === current.id)
        ? [current, ...visible].slice(0, RECENT_SESSIONS_LIMIT)
        : visible;
      const hash = nextRecentSessions.map(s => s.id + s.status + s.timeUpdated).join(',');
      if (hash !== lastSiblingsHashRef.current) {
        lastSiblingsHashRef.current = hash;
        setRecentSessions(nextRecentSessions);
      }
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
    const refreshId = window.setInterval(() => {
      loadRecentSessions(abortControllerRef.current?.signal).catch(err => console.error('Failed to refresh recent sessions', err));
    }, 10000);
    return () => window.clearInterval(refreshId);
  }, [loadRecentSessions]);

  const sessionSeenId = session?.id;
  const sessionSeenUpdated = session?.timeUpdated || 0;

  useEffect(() => {
    if (!sessionSeenId) return;
    void markSessionSeen(sessionSeenId, sessionSeenUpdated)
      .then(() => {
        setSession(prev => prev && prev.id === sessionSeenId ? { ...prev, seen: true } : prev);
        setRecentSessions(prev => prev.map(s => (s.id === sessionSeenId ? { ...s, seen: true } : s)));
      })
      .catch(err => console.error('Failed to mark session seen', err));
  }, [markSessionSeen, sessionSeenId, sessionSeenUpdated]);

  // Restore pending question when navigating to a page.
  // Check sessionStorage for a previously received question (stored when the
  // SSE question.asked event fired), but only if the parts still show the
  // question tool call as pending (not yet answered).
  useEffect(() => {
    if (pendingQuestion || !session?.id || !portAvailable) return;
    if (!hasPendingQuestionInParts(parts)) return;

    const stored = loadPendingQuestion(session.id);
    if (stored) {
      setPendingQuestion(stored);
    }
  }, [parts, session?.id, portAvailable, pendingQuestion]);

  // SSE with reconnection
  const [sseActive, setSseActive] = useState(false);
  useEffect(() => {
    if (!session?.directory || !session?.id) return;
    const dir = session.directory;
    const sid = session.id;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;
    let hasReceivedContentEvent = false; // tracks whether any content event arrived before reconciliation completes

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
      if (type === 'session.status' || type === 'message.updated' || type === 'permission.responded') {
        setPendingPermission(prev => prev === null ? prev : null);
      }
      if (type === 'question.replied' || type === 'question.rejected' || type === 'session.status' || type === 'message.updated') {
        setPendingQuestion(prev => {
          if (prev === null) return prev;
          clearPendingQuestion(sid);
          return null;
        });
      }
    };

    const connect = () => {
      if (cancelled) return;
      evtSource = new EventSource(`/api/events/?dir=${encodeURIComponent(dir)}`);
      evtSource.onopen = () => {
        setSseActive(true);
        // Reconciliation: fetch the latest state to close the gap between
        // the initial load() and the SSE connection opening. Delay slightly
        // so that if SSE events arrive immediately (active conversation),
        // we can skip the redundant fetch.
        setTimeout(() => {
          if (!hasReceivedContentEvent && !cancelled) {
            const signal = abortControllerRef.current?.signal;
            load(signal);
          }
        }, 500);
      };
      evtSource.onmessage = (evt) => {
        const raw = evt.data || '';
        if (!raw || !raw.trim()) return;

        // Debug logging — only when debug mode is active
        if (debugModeRef.current) {
          setSseDebugEvents((prev) => {
            const next = [...prev, { at: Date.now(), event: 'message', data: truncateSseData(raw) }];
            return next.slice(-50);
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
        if (evtSessionId && evtSessionId !== sid) return;

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
                // Append delta to the target field of the existing part
                const existing = prev[idx];
                let existingData: Record<string, unknown>;
                try {
                  existingData = typeof existing.data === 'string'
                    ? JSON.parse(existing.data) as Record<string, unknown>
                    : existing.data as unknown as Record<string, unknown>;
                } catch {
                  existingData = {};
                }
                const currentVal = (existingData[field] as string) || '';
                const updatedData = { ...existingData, [field]: currentVal + deltaText };
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
  }, [session?.directory, session?.id, load]);

  // Compute aggregate token/cost stats from the messages array so the header
  // stays up-to-date from SSE events without needing a server round-trip.
  const liveTokens = (() => {
    let tokensIn = 0, tokensOut = 0;
    for (const m of messages) {
      if (m.data?.role === 'assistant' && m.data.tokens) {
        tokensIn += m.data.tokens.input || 0;
        tokensOut += m.data.tokens.output || 0;
      }
    }
    return { tokensIn, tokensOut };
  })();

  // Use the larger of server-provided totals and locally-computed totals.
  // The server value covers all messages including paginated-out ones;
  // the local value picks up incremental SSE updates before the next load().
  const displayTokensIn = Math.max(session?.totalInputTokens || 0, liveTokens.tokensIn);
  const displayTokensOut = Math.max(session?.totalOutputTokens || 0, liveTokens.tokensOut);

  // Header info
  useEffect(() => {
    if (!session) return;
    const s = session;
    const stats: { label: string; value: string }[] = [
      { label: 'Duration', value: formatDuration(s.durationMs) },
      { label: 'Messages', value: String(totalMessages || s.messageCount) },
      { label: 'Tokens', value: `${formatNumber(displayTokensIn)}/${formatNumber(displayTokensOut)}` },
      { label: 'Project', value: shortPath(s.directory) },
    ];
    if (s.summaryFiles) {
      const changes = [
        s.summaryFiles + ' files',
        s.summaryAdditions ? '+' + s.summaryAdditions : '',
        s.summaryDeletions ? '-' + s.summaryDeletions : '',
      ].filter(Boolean).join(' ');
      stats.push({ label: 'Changes', value: changes });
    }
    setInfo({ sessionTitle: s.title || 'Untitled', stats });
    return () => setInfo({});
  }, [session, totalMessages, setInfo, displayTokensIn, displayTokensOut]);

  const activeModel = ([...messages]
    .reverse()
    .map((m) => formatModelRef(m.data?.providerID, m.data?.modelID))
    .find(Boolean) || session?.defaultModel || '');
  const activeAgent = [...messages].reverse().find(m => !!m.data?.agent)?.data.agent || session?.defaultAgent || '';

  const handleSend = useCallback(async (text: string, images?: AttachedImage[]) => {
    if (!session || !portAvailable) return;

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

    try {
      await sendMessage(
        session.id,
        session.directory,
        text,
        images,
        selectedModel || activeModel || undefined,
        selectedAgent || activeAgent || undefined,
      );
      // SSE events will deliver the real message + assistant response incrementally.
      // The optimistic message is already visible to the user.
    } catch (e) {
      console.error('Failed to send message', e);
      // Show the error as a system message in the conversation
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
          text: `**Failed to send message:** ${e instanceof Error ? e.message : 'Unknown error'}`,
        } as unknown as string,
      };
      setMessages(prev => [...prev, errMsg]);
      setParts(prev => [...prev, errPart]);
    }
  }, [activeAgent, activeModel, portAvailable, selectedAgent, selectedModel, sendMessage, session]);

  const handleCommand = useCallback(async (command: string, args: string) => {
    if (!session || !portAvailable) return;

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
        session.directory,
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
  }, [activeAgent, activeModel, portAvailable, selectedAgent, selectedModel, session]);

  const handlePermissionReply = useCallback(async (reply: 'once' | 'always' | 'reject') => {
    if (!pendingPermission || answeringPermission || !portAvailable || !session) return;
    setPermissionError(null);
    setAnsweringPermission(true);
    try {
      await respondPermission(session.id, session.directory, pendingPermission.permissionId, reply);
      setPendingPermission(null);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      setPermissionError(e instanceof Error ? e.message : 'Failed to respond to permission request');
    } finally {
      setAnsweringPermission(false);
    }
  }, [answeringPermission, pendingPermission, portAvailable, respondPermission, session]);

  const handleQuestionReply = useCallback(async (answers: string[][]) => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !session) return;
    setQuestionError(null);
    setAnsweringQuestion(true);
    try {
      await respondQuestion(session.id, session.directory, pendingQuestion.requestId, answers);
      setPendingQuestion(null);
      setQuestionError(null);
      clearPendingQuestion(session.id);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to respond to question', e);
      setQuestionError(e instanceof Error ? e.message : 'Failed to submit answer');
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, pendingQuestion, portAvailable, respondQuestion, session]);

  const handleQuestionReject = useCallback(async () => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !session) return;
    setAnsweringQuestion(true);
    try {
      await rejectQuestion(session.id, session.directory, pendingQuestion.requestId);
      setPendingQuestion(null);
      clearPendingQuestion(session.id);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to dismiss question', e);
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, pendingQuestion, portAvailable, rejectQuestion, session]);

  const abortSession = useApiStore((state) => state.abortSession);

  const handleAbort = useCallback(async () => {
    if (!session || !portAvailable) return;
    try {
      await abortSession(session.id, session.directory);
      // SSE events will deliver the updated session state incrementally.
    } catch (e) {
      console.error('Failed to abort session', e);
    }
  }, [abortSession, portAvailable, session]);

  // Find the tmux session whose resolved path matches the current project directory.
  const matchingTmuxSession = session
    ? tmux.findSession(session.directory)
    : undefined;

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

  useHotkeys('t', (e) => {
    e.preventDefault();
    handleTmuxShortcut();
  }, { enabled: !!matchingTmuxSession, preventDefault: true }, [handleTmuxShortcut, matchingTmuxSession]);

  useHotkeys('v', (e) => {
    e.preventDefault();
    handleVSCodeShortcut();
  }, { enabled: !!session, preventDefault: true }, [handleVSCodeShortcut, session]);

  const handleNewSession = useCallback(async () => {
    if (!session) return;
    try {
      const res = await createSession(session.directory);
      if (res.id) navigate(`/session/${res.id}`);
    } catch (e) {
      console.error('Failed to create session', e);
    }
  }, [session, createSession, navigate]);

  useHotkeys('n', (e) => {
    e.preventDefault();
    handleNewSession();
  }, { enabled: !!session && portAvailable, preventDefault: true }, [handleNewSession, session, portAvailable]);

  const hasMore = messages.length < totalMessages;
  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  const composerModels = Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model)));
  const showSseNotice = portAvailable && !sseActive;
  const showSseDebug = debugMode && sseDebugEvents.length > 0;
  // The assistant is still working if:
  // - the last message is from the user (assistant hasn't replied yet), or
  // - the last message is from the assistant with no finish reason and no error (still streaming).
  // Once finish is set to any value ("stop", "tool-calls", etc.), that turn is done.
  // A message with an error object is also not running.
  const isRunning = lastMsg
    ? lastMsg.data?.role === 'user' || (lastMsg.data?.role === 'assistant' && !lastMsg.data?.finish && !lastMsg.data?.error)
    : false;

  return (
    <div className="session-layout">
      <div className="session-sidebar">
        <div className="session-sidebar-header">
          <span className="session-sidebar-heading">
            <span>Recent sessions</span>
          </span>
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
        <div className="session-sidebar-list">
          {recentSessions.map(sib => (
            <div
              key={sib.id}
              role="button"
              tabIndex={0}
              aria-selected={sib.id === id}
              className={`session-sidebar-item ${sib.id === id ? 'active' : ''}${archivingSessionIds.has(sib.id) ? ' archiving' : ''}`}
              onClick={() => navigate(`/session/${sib.id}`)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); navigate(`/session/${sib.id}`); } }}
            >
              <StatusBadge status={sib.status} compact seen={(sib.status === 'waiting' || sib.status === 'error') && sib.seen} />
              <span className="session-sidebar-item-body">
                <span className="session-sidebar-title">{sib.title || 'Untitled'}</span>
                <span className="session-sidebar-project">{shortPath(sib.directory)}</span>
              </span>
              <span className="session-sidebar-meta">
                <span className="session-sidebar-time">{relativeTime(sib.timeUpdated)}</span>
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
            </div>
          ))}
        </div>
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
            <button
              type="button"
              className="session-sidebar-new"
              onClick={handleVSCodeShortcut}
              title="Open in VS Code (V)"
              style={{ textDecoration: 'none', fontSize: 11 }}
            >&lt;/&gt;</button>
            <button
              className="session-sidebar-new"
              onClick={async () => {
                try {
                  const res = await createSession(session.directory);
                  if (res.id) navigate(`/session/${res.id}`);
                } catch (e) {
                  console.error('Failed to create session', e);
                }
              }}
              title="New session"
            >+</button>
          </div>
        )}
        {loading ? (
          <div className="oc-loading">
            <div className="oc-spinner" />
            Loading conversation...
          </div>
        ) : loadError ? (
          <div className="oc-error-banner" style={{ margin: 24 }}>
            {loadError}
            <button onClick={() => { setLoadError(null); load(abortControllerRef.current?.signal); }}>Retry</button>
          </div>
        ) : session && (
          <OcmanRuntimeProvider
            key={session.id}
            messages={messages}
            parts={parts}
            sessionId={session.id}
            directory={session.directory}
            portAvailable={portAvailable}
          >
            <AssistantThread
              hasMore={hasMore}
              loadingMore={loadingMore}
              onLoadMore={loadMore}
              composer={pendingPermission && portAvailable ? (
                <div className="oc-permission-wrap">
                  <div className="oc-permission-box">
                    <div className="oc-permission-header">
                      <span className="oc-permission-icon">&#9651;</span>
                      <span>Permission required</span>
                    </div>
                    <div className="oc-permission-desc">
                      &larr; {pendingPermission.permission}
                    </div>
                    {pendingPermission.patterns.length > 0 && (
                      <div className="oc-permission-patterns">
                        <div className="oc-permission-patterns-label">Patterns</div>
                        {pendingPermission.patterns.map((p) => (
                          <div key={p} className="oc-permission-pattern">- {p}</div>
                        ))}
                      </div>
                    )}
                    {permissionError && (
                      <div className="oc-permission-error">{permissionError}</div>
                    )}
                    <div className="oc-permission-actions">
                      <button
                        type="button"
                        className={`oc-permission-btn oc-permission-btn-active`}
                        onClick={() => handlePermissionReply('once')}
                        disabled={answeringPermission}
                      >Allow once</button>
                      <button
                        type="button"
                        className="oc-permission-btn"
                        onClick={() => handlePermissionReply('always')}
                        disabled={answeringPermission}
                      >Allow always</button>
                      <button
                        type="button"
                        className="oc-permission-btn"
                        onClick={() => handlePermissionReply('reject')}
                        disabled={answeringPermission}
                      >Reject</button>
                    </div>
                  </div>
                </div>
              ) : pendingQuestion && portAvailable ? (
                <QuestionPrompt
                  question={pendingQuestion}
                  onReply={handleQuestionReply}
                  onReject={handleQuestionReject}
                  disabled={answeringQuestion}
                  error={questionError}
                />
              ) : (
                <Composer
                  onSend={handleSend}
                  onCommand={handleCommand}
                  onAbort={handleAbort}
                  isRunning={isRunning}
                  disabled={!portAvailable}
                  whisperAvailable={whisperAvailable}
                  models={composerModels}
                  activeModel={activeModel}
                  selectedModel={selectedModel}
                  onModelChange={setSelectedModel}
                  activeAgent={activeAgent}
                  selectedAgent={selectedAgent}
                  onAgentChange={setSelectedAgent}
                  contextTokens={session?.contextTokenCount || undefined}
                  sessionId={session?.id}
                  directory={session?.directory}
                />
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
          </OcmanRuntimeProvider>
        )}
      </div>
    </div>
  );
}
