import { reduceSessionView, type SessionAction, type SessionView, type SseEvent } from '../../lib/sessionReducer';
import { truncateSseData } from '../../lib/sseHelpers';
import type { SseDebugEvent } from './useSession';

type BatchedSessionAction = SessionAction | { type: 'sseBatch'; events: SseEvent[] };

export function reduceBatchedSessionView(view: SessionView, action: BatchedSessionAction): SessionView {
  return action.type === 'sseBatch'
    ? action.events.reduce((state, event) => reduceSessionView(state, { type: 'sse', event }), view)
    : reduceSessionView(view, action);
}

function normalizeEnvelope(event: SseEvent): SseEvent {
  if (event.properties) return event;
  const payload = (event as unknown as Record<string, unknown>).payload;
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return event;
  const nested = payload as Record<string, unknown>;
  if (typeof nested.type === 'string' && nested.properties && typeof nested.properties === 'object') {
    return nested as unknown as SseEvent;
  }
  return { ...event, properties: nested };
}

function parseEvent(raw: string, channel: string): SseEvent {
  const event = normalizeEnvelope(JSON.parse(raw) as SseEvent);
  if (channel === 'message') return event;
  const payload = event as unknown as Record<string, unknown>;
  const properties = payload.properties && typeof payload.properties === 'object'
    ? payload.properties as Record<string, unknown> : null;
  const isPart = typeof payload.id === 'string' && (
    typeof payload.messageID === 'string' || typeof payload.messageId === 'string'
  );
  if (!event.type || isPart || (channel === 'message.part.updated' && event.type !== channel)) {
    return {
      type: channel,
      properties: properties ?? (channel === 'message.part.updated' ? { part: payload } : payload),
    };
  }
  return event;
}

// Do not register `message`: browsers already deliver it to onmessage.
const NAMED_CHANNELS = [
  'message.created', 'message.updated', 'message.part.updated', 'message.part.delta',
  'session.status', 'session.idle', 'session.diff',
  'permission', 'permission.asked', 'permission.replied',
  'question', 'question.asked', 'question.replied', 'question.rejected',
  'approval', 'tool', 'error', 'ocman.permission.pending', 'ocman.permission.checking',
  'ocman.permission.flagged', 'ocman.permission.auto-approved', 'ocman.permission.approved',
  'ocman.session.changed',
];

function sessionActivity(event: SseEvent, sessionId: string) {
  const properties = event.properties;
  const target = properties?.sessionID ??
    (properties?.info as { sessionID?: string } | undefined)?.sessionID ??
    (properties?.part as { sessionID?: string } | undefined)?.sessionID;
  if (target && target !== sessionId) return 'foreign';
  const status = properties?.status;
  const statusType = typeof status === 'string' ? status : (status as { type?: string } | undefined)?.type;
  if (event.type === 'session.idle' || (event.type === 'session.status' && statusType === 'idle')) return 'idle';
  if ((event.type === 'session.status' && (statusType === 'busy' || statusType === 'retry')) ||
    ['message.created', 'message.part.delta'].includes(event.type)) return 'active';
  return null;
}

/** One ordered queue per mounted session, shared by named and default channels. */
export function createSessionSse(options: {
  sessionId: string;
  dispatch: (action: BatchedSessionAction) => void;
  reconcile: () => Promise<boolean>;
  dirty: () => void;
  debug?: (events: SseDebugEvent[]) => void;
}) {
  let pending: SseEvent[] = [];
  let debugEvents: SseDebugEvent[] = [];
  let frame: number | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let diffTimer: ReturnType<typeof setTimeout> | undefined;
  let disposed = false;
  let idle = false;
  let revision = 0;

  const take = () => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    clearTimeout(timer);
    frame = undefined;
    timer = undefined;
    const events = pending;
    pending = [];
    return events;
  };
  const flush = () => {
    const events = take();
    if (events.length) {
      options.dispatch({ type: 'sseBatch', events });
      if (events.some((event) => {
        if (event.type === 'session.diff' || event.type === 'ocman.session.changed') return true;
        if (event.type !== 'message.part.updated') return false;
        const part = event.properties?.part as Record<string, unknown> | undefined;
        return part?.type === 'tool' && ['edit', 'write', 'mcp_edit', 'mcp_write', 'mcp_Edit', 'mcp_Write'].includes(part.tool as string);
      })) options.dirty();
    }
    if (debugEvents.length) options.debug?.(debugEvents);
    debugEvents = [];
  };
  const schedule = () => {
    if (timer !== undefined) return;
    frame = requestAnimationFrame(flush);
    // Background tabs may not receive animation frames.
    timer = setTimeout(flush, 50);
  };
  const handle = (channel: string) => (message: MessageEvent) => {
    if (disposed) return;
    const raw = message.data || '';
    if (!raw.trim()) return;
    if (options.debug) {
      debugEvents = [...debugEvents, { at: Date.now(), event: channel, data: truncateSseData(raw) }].slice(-50);
      schedule();
    }
    let event: SseEvent;
    try { event = parseEvent(raw, channel); } catch { return; }
    if (typeof event.type !== 'string') return;
    const activity = sessionActivity(event, options.sessionId);
    if (activity === 'active') {
      idle = false;
      revision++;
    }
    if (activity === 'idle' && idle) {
      // A twin must not replace an authoritative terminal status with `done`.
      flush();
      return;
    }
    pending.push(event);
    // Prompts and lifecycle changes are urgent; flush earlier content first.
    if (event.type.startsWith('message.') || event.type === 'tool' || event.type === 'session.diff' || event.type === 'ocman.session.changed') {
      schedule();
    } else {
      flush();
    }
    if (activity === 'idle') {
      clearTimeout(diffTimer);
      diffTimer = undefined;
      idle = true;
      const requestedRevision = revision;
      void options.reconcile().then((success) => {
        if (!success && revision === requestedRevision) idle = false;
      });
    } else if (activity !== 'foreign' && event.type === 'session.diff') {
      clearTimeout(diffTimer);
      diffTimer = setTimeout(() => {
        diffTimer = undefined;
        flush();
        void options.reconcile();
      }, 500);
    }
  };

  return {
    attach(source: EventSource) {
      // Activity may have been missed while disconnected. Old fetch completions
      // must not change the new connection's idle deduplication state.
      idle = false;
      revision++;
      source.onmessage = handle('message');
      for (const channel of NAMED_CHANNELS) source.addEventListener(channel, handle(channel));
    },
    flush,
    dispose() {
      disposed = true;
      clearTimeout(diffTimer);
      return take();
    },
  };
}
