// Client for GET /api/session/{id}/stream: SSE `event: oce` envelopes with
// `id: <epoch>:<seq>`. Batches content for one reducer dispatch per frame,
// tracks the resume cursor and routes side-channel events (design §7, §9).

import type { QueuedMessage } from '../api.types';
import { markSseFlush } from '../perfMonitor';
import { truncateSseData } from '../sseHelpers';
import { formatCursor, OCE_VERSION, parseCursor, type OceEnvelope } from './events';

export interface StreamDebugEvent { at: number; event: string; data: string }

export const STALE_RECONCILE_MS = 2_000;
/** Fallback when an idle edge is not followed by a settled turn end. */
export const SETTLE_RECONCILE_MS = 3_000;

const BATCHED = new Set([
  'message.upserted', 'message.removed', 'part.upserted', 'part.removed', 'part.delta',
  'history.committed', 'changes.dirty', 'child.upserted',
]);

export function conversationStreamUrl(sessionId: string, platform?: string, after?: string): string {
  const query = new URLSearchParams();
  if (platform) query.set('platform', platform);
  if (after) query.set('after', after);
  const qs = query.toString();
  return `/api/session/${encodeURIComponent(sessionId)}/stream${qs ? `?${qs}` : ''}`;
}

function parseEnvelope(raw: string): OceEnvelope | null {
  let value: unknown;
  try { value = JSON.parse(raw); } catch { return null; }
  if (!value || typeof value !== 'object') return null;
  const env = value as OceEnvelope;
  if (env.v !== OCE_VERSION || typeof env.epoch !== 'string' || !env.event || typeof env.event.type !== 'string') return null;
  return env;
}

export function createConversationStream(options: {
  sessionId: string;
  dispatch: (action: { type: 'oceBatch'; envelopes: OceEnvelope[] }) => void;
  reconcile: () => Promise<boolean>;
  recover: () => Promise<boolean>;
  upstream?: (error: string | null) => void;
  dirty: () => void;
  queue?: (messages: QueuedMessage[]) => void;
  /** Whether the rendered view still holds parts the stream could not anchor. */
  hasStale?: () => boolean;
  debug?: (events: StreamDebugEvent[]) => void;
}) {
  let pending: OceEnvelope[] = [];
  let debugEvents: StreamDebugEvent[] = [];
  let frame: number | undefined;
  let timer: ReturnType<typeof setTimeout> | undefined;
  let staleTimer: ReturnType<typeof setTimeout> | undefined;
  let settleTimer: ReturnType<typeof setTimeout> | undefined;
  let cursor: string | undefined;
  let disposed = false;

  const reconcile = () => {
    clearTimeout(settleTimer);
    settleTimer = undefined;
    void options.reconcile();
  };
  const take = () => {
    if (frame !== undefined) cancelAnimationFrame(frame);
    clearTimeout(timer);
    frame = undefined;
    timer = undefined;
    const envelopes = pending;
    pending = [];
    return envelopes;
  };
  const watchStale = () => {
    if (staleTimer !== undefined || !options.hasStale) return;
    staleTimer = setTimeout(() => {
      staleTimer = undefined;
      if (!disposed && options.hasStale?.()) void options.recover();
    }, STALE_RECONCILE_MS);
  };
  const flush = () => {
    const envelopes = take();
    if (envelopes.length) {
      markSseFlush();
      options.dispatch({ type: 'oceBatch', envelopes });
      if (envelopes.some((env) => env.event.type === 'changes.dirty')) options.dirty();
      if (envelopes.some((env) => env.event.type === 'part.delta')) watchStale();
      if (envelopes.some((env) => env.event.type === 'child.upserted' && !env.event.child.deleted)) reconcile();
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
  // Side effects run after the envelope is in the view.
  const sideEffects = (env: OceEnvelope, priorEpoch: string | undefined) => {
    const e = env.event;
    switch (e.type) {
      case 'stream.hello': {
        if (!e.resumed) options.upstream?.(e.overlay?.liveConnection === false ? 'Platform connection unavailable' : null);
        if (!e.resumed || (priorEpoch !== undefined && priorEpoch !== env.epoch)) {
          void options.recover(); options.dirty();
          if (e.overlay?.queue) options.queue?.(e.overlay.queue.messages);
        }
        break;
      }
      case 'stream.reset':
        // A slow consumer is closed and resumes from its cursor on reconnect.
        if (e.reason !== 'slow-consumer') void options.recover();
        break;
      case 'history.rewritten':
        void options.recover();
        break;
      case 'stream.error':
        options.upstream?.(e.message);
        break;
      case 'turn.ended':
        reconcile();
        break;
      case 'session.status':
        if (e.provisional && e.status === 'done') {
          clearTimeout(settleTimer);
          settleTimer = setTimeout(reconcile, SETTLE_RECONCILE_MS);
        }
        break;
      case 'queue.changed':
        options.queue?.(e.messages);
        break;
    }
  };
  const handle = (message: MessageEvent) => {
    if (disposed) return;
    const raw = typeof message.data === 'string' ? message.data : '';
    if (!raw.trim()) return;
    if (options.debug) {
      debugEvents = [...debugEvents, { at: Date.now(), event: 'oce', data: truncateSseData(raw) }].slice(-50);
      schedule();
    }
    const env = parseEnvelope(raw);
    if (!env || env.sessionId !== options.sessionId) return;
    const priorEpoch = cursor ? parseCursor(cursor)?.epoch : undefined;
    cursor = typeof env.seq === 'number' ? formatCursor(env)
      // An empty log's hello has no seq; keep the epoch so a resume is possible.
      : env.event.type === 'stream.hello' && priorEpoch !== env.epoch ? `${env.epoch}:0` : cursor;
    pending.push(env);
    if (BATCHED.has(env.event.type)) {
      schedule();
    } else {
      // Prompts and lifecycle changes are urgent; earlier content flushes first.
      flush();
      sideEffects(env, priorEpoch);
    }
  };

  return {
    /** Every non-resumed hello fences a fresh authoritative history read. */
    attach(source: EventSource) {
      source.addEventListener('oce', handle as EventListener);
    },
    cursor: () => cursor,
    flush,
    dispose() {
      disposed = true;
      clearTimeout(staleTimer);
      clearTimeout(settleTimer);
      return take();
    },
  };
}
