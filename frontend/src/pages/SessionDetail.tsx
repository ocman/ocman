import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate, useSearchParams } from 'react-router-dom';
import type { Session, SessionDetail as SessionDetailData, Message, Part } from '../lib/api';
import { formatDuration, formatNumber, shortPath, relativeTime } from '../lib/format';
import { useHeaderInfo, usePageTitle } from '../lib/headerContext';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import { AssistantThread, Composer, type AttachedImage } from '../components/AssistantThread';
import { StatusBadge } from '../components/StatusBadge';
import { useTmux } from '../lib/useTmux';
import { filterVisibleSessions } from '../lib/sessionVisibility';
import { useApiStore, useApiRequest } from '../lib/apiStore';

const PAGE_SIZE = 50;
const RECENT_SESSIONS_LIMIT = 20;
const ARCHIVE_ANIMATION_MS = 220;

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

function truncateSseData(raw: string, max = 240): string {
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
}: {
  question: PendingQuestion;
  onReply: (answers: string[][]) => void;
  onReject: () => void;
  disabled?: boolean;
}) {
  const [selectedIndices, setSelectedIndices] = useState<Record<number, number | null>>({});
  const [customTexts, setCustomTexts] = useState<Record<number, string>>({});

  const handleOptionClick = (qi: number, oi: number) => {
    if (disabled) return;
    setSelectedIndices(prev => ({ ...prev, [qi]: oi }));
    setCustomTexts(prev => ({ ...prev, [qi]: '' }));
  };

  const handleCustomFocus = (qi: number) => {
    setSelectedIndices(prev => ({ ...prev, [qi]: null }));
  };

  const handleSubmit = () => {
    if (disabled) return;
    const answers: string[][] = question.questions.map((q, qi) => {
      const sel = selectedIndices[qi];
      const custom = customTexts[qi]?.trim();
      if (sel != null && sel >= 0 && sel < q.options.length) {
        return [q.options[sel].label];
      }
      if (custom) return [custom];
      return [];
    });
    if (answers.every(a => a.length > 0)) {
      onReply(answers);
    }
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onReject();
    } else if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSubmit();
    }
  };

  const allAnswered = question.questions.every((_, qi) => {
    const sel = selectedIndices[qi];
    const custom = customTexts[qi]?.trim();
    return (sel != null && sel >= 0) || !!custom;
  });

  return (
    <div className="oc-question-wrap" onKeyDown={handleKeyDown}>
      {question.questions.map((q, qi) => (
        <div key={qi} className="oc-question-box">
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
            <button
              type="button"
              className="oc-question-submit-btn"
              onClick={handleSubmit}
              disabled={disabled || !allAnswered}
            >Submit</button>
            <button
              type="button"
              className="oc-question-dismiss-btn"
              onClick={onReject}
              disabled={disabled}
            >Dismiss</button>
            <span className="oc-question-keys">
              <kbd>enter</kbd> submit &middot; <kbd>esc</kbd> dismiss
            </span>
          </div>
        </div>
      ))}
    </div>
  );
}

export function SessionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const debugMode = searchParams.has('debug');
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
  const [filterRefreshPending, setFilterRefreshPending] = useState(false);
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [showArchivedRecent, setShowArchivedRecent] = useState(false);
  const { setInfo } = useHeaderInfo();
  usePageTitle(session?.title || 'Session');
  const lastHashRef = useRef('');
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
  const sessionsRequest = useApiRequest('sessions:get');
  const refreshingRecentSessionsFilter = filterRefreshPending || sessionsRequest.loading;

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

      // Always update session metadata
      setSession({
        ...result.session,
        contextTokenCount: result.session.contextTokenCount ?? result.contextTokenCount,
        defaultAgent: result.defaultAgent,
        defaultModel: result.defaultModel,
      });
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

  // SSE with reconnection
  const [sseActive, setSseActive] = useState(false);
  useEffect(() => {
    if (!session?.directory) return;
    const dir = session.directory;
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    let evtSource: EventSource | null = null;
    let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
    let cancelled = false;

    const handleEventData = (raw: string, event = 'message') => {
      if (!raw) return;

      setSseDebugEvents((prev) => {
        const next = [...prev, { at: Date.now(), event, data: truncateSseData(raw) }];
        return next.slice(-8);
      });

      try {
        const parsed = JSON.parse(raw) as unknown;
        const perm = extractPendingPermission(parsed);
        if (perm) {
          setPendingPermission(perm);
          setPermissionError(null);
        }
        const question = extractPendingQuestion(parsed);
        if (question) {
          setPendingQuestion(question);
        }
        if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
          const obj = parsed as Record<string, unknown>;
          // Clear pending permission on session status changes or new messages
          if (obj.type === 'session.status' || obj.type === 'message.updated' || obj.type === 'permission.responded') {
            setPendingPermission(null);
          }
          // Clear pending question when answered, rejected, or session moves on
          if (obj.type === 'question.replied' || obj.type === 'question.rejected' || obj.type === 'session.status' || obj.type === 'message.updated') {
            setPendingQuestion(null);
          }
        }
      } catch {
        // not JSON, ignore
      }
    };

    const connect = () => {
      if (cancelled) return;
      evtSource = new EventSource(`/api/events/?dir=${encodeURIComponent(dir)}`);
      evtSource.onopen = () => { setSseActive(true); };
      evtSource.onmessage = (evt) => {
        handleEventData(evt.data || '', 'message');
        if (debounceTimer) clearTimeout(debounceTimer);
        debounceTimer = setTimeout(() => {
          const signal = abortControllerRef.current?.signal;
          load(signal);
        }, 200);
      };
      // Some OpenCode SSE updates may use named events, not default "message".
      ['question', 'permission', 'approval', 'tool', 'error'].forEach((eventName) => {
        evtSource?.addEventListener(eventName, (evt) => {
          handleEventData((evt as MessageEvent).data || '', eventName);
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
      if (debounceTimer) clearTimeout(debounceTimer);
      if (reconnectTimer) clearTimeout(reconnectTimer);
      clearInterval(fallback);
      setSseActive(false);
    };
  }, [session?.directory, load]);

  // Header info
  useEffect(() => {
    if (!session) return;
    const s = session;
    const stats: { label: string; value: string }[] = [
      { label: 'Duration', value: formatDuration(s.durationMs) },
      { label: 'Messages', value: String(totalMessages || s.messageCount) },
      { label: 'Tokens', value: `${formatNumber(s.totalInputTokens)}/${formatNumber(s.totalOutputTokens)}` },
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
  }, [session, totalMessages, setInfo]);

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
      // Reload immediately to get the real message + assistant response
      load(abortControllerRef.current?.signal);
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
  }, [activeAgent, activeModel, load, portAvailable, selectedAgent, selectedModel, sendMessage, session]);

  const handlePermissionReply = useCallback(async (reply: 'once' | 'always' | 'reject') => {
    if (!pendingPermission || answeringPermission || !portAvailable || !session) return;
    setPermissionError(null);
    setAnsweringPermission(true);
    try {
      await respondPermission(session.id, session.directory, pendingPermission.permissionId, reply);
      setPendingPermission(null);
      // Reload immediately and again after a short delay to catch the updated session state
      load(abortControllerRef.current?.signal);
      setTimeout(() => load(abortControllerRef.current?.signal), 1000);
    } catch (e) {
      setPermissionError(e instanceof Error ? e.message : 'Failed to respond to permission request');
    } finally {
      setAnsweringPermission(false);
    }
  }, [answeringPermission, load, pendingPermission, portAvailable, respondPermission, session]);

  const handleQuestionReply = useCallback(async (answers: string[][]) => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !session) return;
    setAnsweringQuestion(true);
    try {
      await respondQuestion(session.id, session.directory, pendingQuestion.requestId, answers);
      setPendingQuestion(null);
      load(abortControllerRef.current?.signal);
      setTimeout(() => load(abortControllerRef.current?.signal), 1000);
    } catch (e) {
      console.error('Failed to respond to question', e);
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, load, pendingQuestion, portAvailable, respondQuestion, session]);

  const handleQuestionReject = useCallback(async () => {
    if (!pendingQuestion || answeringQuestion || !portAvailable || !session) return;
    setAnsweringQuestion(true);
    try {
      await rejectQuestion(session.id, session.directory, pendingQuestion.requestId);
      setPendingQuestion(null);
      load(abortControllerRef.current?.signal);
      setTimeout(() => load(abortControllerRef.current?.signal), 1000);
    } catch (e) {
      console.error('Failed to dismiss question', e);
    } finally {
      setAnsweringQuestion(false);
    }
  }, [answeringQuestion, load, pendingQuestion, portAvailable, rejectQuestion, session]);

  // Find the tmux session whose resolved path matches the current project directory.
  const matchingTmuxSession = session
    ? tmux.findSession(session.directory)
    : undefined;

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
            {refreshingRecentSessionsFilter && (
              <span className="session-sidebar-inline-loading" aria-hidden="true">
                <span className="oc-spinner session-sidebar-inline-spinner" />
              </span>
            )}
          </span>
          <button
            type="button"
            className={`session-sidebar-new${showArchivedRecent ? ' active' : ''}`}
            onClick={() => {
              setFilterRefreshPending(true);
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
              <StatusBadge status={sib.status} compact seen={sib.status === 'waiting' && sib.seen} />
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
                title={`Switch tmux to ${shortPath(matchingTmuxSession.name)}`}
                style={{ fontSize: 11, fontFamily: "'SF Mono', Consolas, monospace" }}
              >tmux</button>
            )}
            <a
              href={`vscode://file${session.directory}`}
              className="session-sidebar-new"
              title="Open in VS Code"
              style={{ textDecoration: 'none', fontSize: 11 }}
            >&lt;/&gt;</a>
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
                />
              ) : (
                <Composer
                  onSend={handleSend}
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
