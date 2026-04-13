import { useState, useEffect, useCallback, useRef } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { api } from '../lib/api';
import type { Session, SessionDetail as SessionDetailData, Message, Part } from '../lib/api';
import { formatDuration, formatNumber, shortPath, relativeTime } from '../lib/format';
import { useHeaderInfo } from '../lib/headerContext';
import { OcmanRuntimeProvider } from '../components/OcmanRuntimeProvider';
import { AssistantThread, Composer } from '../components/AssistantThread';
import { StatusBadge } from '../components/StatusBadge';

const PAGE_SIZE = 50;

export function SessionDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [session, setSession] = useState<SessionDetailData['session'] | null>(null);
  const [messages, setMessages] = useState<Message[]>([]);
  const [parts, setParts] = useState<Part[]>([]);
  const [totalMessages, setTotalMessages] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [portAvailable, setPortAvailable] = useState(false);
  const [siblings, setSiblings] = useState<Session[]>([]);
  const [loadingSiblings, setLoadingSiblings] = useState(true);
  const { setInfo } = useHeaderInfo();
  const lastHashRef = useRef('');
  const lastSiblingsHashRef = useRef('');
  const directoryRef = useRef('');

  // Load the latest page (newest messages). Merges with older loaded messages.
  const load = useCallback(async () => {
    if (!id) return;
    try {
      const result = await api.session(id, PAGE_SIZE, 0);

      // Always update session metadata
      setSession(result.session);
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
        // Also remove optimistic (temp-*) messages once real data arrives.
        setMessages(prev => {
          const newIds = new Set(newMsgs.map(m => m.id));
          const older = prev.filter(m => !newIds.has(m.id) && !m.id.startsWith('temp-'));
          return [...older, ...newMsgs];
        });
        setParts(prev => {
          const newIds = new Set(newParts.map(p => p.id));
          const older = prev.filter(p => !newIds.has(p.id) && !p.id.startsWith('part-temp-'));
          return [...older, ...newParts];
        });
      }
    } catch (e) {
      console.error('Failed to load session', e);
    }
    setLoading(false);
  }, [id]);

  // Load older messages (prepend)
  const loadMore = useCallback(async () => {
    if (!id || loadingMore) return;
    setLoadingMore(true);
    try {
      const result = await api.session(id, PAGE_SIZE, messages.length);
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
    } finally {
      setLoadingMore(false);
    }
  }, [id, messages.length, loadingMore]);

  // Reset on session change
  useEffect(() => {
    lastHashRef.current = '';
    setSession(null);
    setMessages([]);
    setParts([]);
    setTotalMessages(0);
    setLoading(true);
    setPortAvailable(false);
    load();
    if (id) {
      api.sessionPort(id).then(p => setPortAvailable(p.available)).catch(() => setPortAvailable(false));
    }
  }, [id, load]);

  const loadSiblings = useCallback(async (dir: string) => {
    const result = await api.sessions({ dir });
    const hash = result.map(s => s.id + s.status + s.timeUpdated).join(',');
    if (hash !== lastSiblingsHashRef.current) {
      lastSiblingsHashRef.current = hash;
      setSiblings(result);
    }
    setLoadingSiblings(false);
  }, []);

  useEffect(() => {
    if (!session) return;
    directoryRef.current = session.directory;
    loadSiblings(session.directory);
  }, [session?.directory, loadSiblings]);

  // SSE
  useEffect(() => {
    if (!session?.directory) return;
    const dir = session.directory;
    let debounceTimer: ReturnType<typeof setTimeout> | null = null;
    let sseConnected = false;

    const evtSource = new EventSource(`/api/events/?dir=${encodeURIComponent(dir)}`);
    evtSource.onopen = () => { sseConnected = true; };
    evtSource.onmessage = () => {
      if (debounceTimer) clearTimeout(debounceTimer);
      debounceTimer = setTimeout(() => {
        load();
        loadSiblings(dir);
      }, 200);
    };
    evtSource.onerror = () => { sseConnected = false; };

    const fallback = setInterval(() => {
      if (!sseConnected) {
        load();
        loadSiblings(dir);
      }
    }, 10000);

    return () => {
      evtSource.close();
      if (debounceTimer) clearTimeout(debounceTimer);
      clearInterval(fallback);
    };
  }, [session?.directory, load, loadSiblings]);

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

  const handleSend = useCallback(async (text: string) => {
    if (!session || !portAvailable) return;

    // Optimistically add user message immediately
    const tempId = 'temp-' + Date.now();
    const optimisticMsg: Message = {
      id: tempId,
      sessionId: session.id,
      timeCreated: Date.now(),
      data: { role: 'user' },
    };
    const optimisticPart: Part = {
      id: 'part-' + tempId,
      messageId: tempId,
      sessionId: session.id,
      data: { type: 'text', text } as unknown as string,
    };
    setMessages(prev => [...prev, optimisticMsg]);
    setParts(prev => [...prev, optimisticPart]);

    try {
      await api.sendMessage(session.id, session.directory, text);
      // Reload immediately to get the real message + assistant response
      load();
    } catch (e) {
      console.error('Failed to send message', e);
      // Remove optimistic message on failure
      setMessages(prev => prev.filter(m => m.id !== tempId));
    }
  }, [session?.id, session?.directory, portAvailable, load]);

  const hasMore = messages.length < totalMessages;
  const lastMsg = messages.length > 0 ? messages[messages.length - 1] : null;
  // The assistant is still working if:
  // - the last message is from the user (assistant hasn't replied yet), or
  // - the last message is from the assistant with no finish reason (still streaming).
  // Once finish is set to any value ("stop", "tool-calls", etc.), that turn is done.
  const isRunning = lastMsg
    ? lastMsg.data?.role === 'user' || (lastMsg.data?.role === 'assistant' && !lastMsg.data?.finish)
    : false;

  return (
    <div className="session-layout">
      <div className="session-sidebar">
        <div className="session-sidebar-header">
          <span>{session ? shortPath(session.directory) : '...'}</span>
          {session && (
            <button
              className="session-sidebar-new"
              onClick={async () => {
                try {
                  const res = await api.createSession(session.directory);
                  if (res.id) navigate(`/session/${res.id}`);
                } catch (e) {
                  console.error('Failed to create session', e);
                }
              }}
              title="New session"
            >+</button>
          )}
        </div>
        <div className="session-sidebar-list">
          {loadingSiblings ? (
            <div className="oc-list-loading">
              <div className="oc-spinner" />
              Loading sessions...
            </div>
          ) : siblings.map(sib => (
            <div
              key={sib.id}
              className={`session-sidebar-item ${sib.id === id ? 'active' : ''}`}
              onClick={() => navigate(`/session/${sib.id}`)}
            >
              <StatusBadge status={sib.status} />
              <span className="session-sidebar-title">{sib.title || 'Untitled'}</span>
              <span className="session-sidebar-time">{relativeTime(sib.timeUpdated)}</span>
            </div>
          ))}
        </div>
      </div>
      <div className="session-main">
        {loading ? (
          <div className="oc-loading">
            <div className="oc-spinner" />
            Loading conversation...
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
              composer={<Composer onSend={handleSend} isRunning={isRunning} disabled={!portAvailable} />}
            />
          </OcmanRuntimeProvider>
        )}
      </div>
    </div>
  );
}
