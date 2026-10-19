// Test helpers for the conversation stream (OCE envelopes on `event: oce`).
import type { OceEnvelope, OceEvent, OcePart } from '../lib/conversation/events';

let nextSeq = 0;

export function oceEnvelope(sessionId: string, event: OceEvent, opts: { epoch?: string; seq?: number } = {}): OceEnvelope {
  return {
    v: 1, epoch: opts.epoch ?? 'test', seq: opts.seq ?? ++nextSeq, sessionId, platform: 'opencode',
    at: Date.now(), durability: 'live', event,
  };
}

/** The page session of a `/api/session/{id}/stream` URL. */
export function streamSessionId(url: string): string {
  const match = /\/api\/session\/([^/?]+)\/stream/.exec(url);
  return match ? decodeURIComponent(match[1]) : '';
}

export function hello(resumed = false, overlay?: Extract<OceEvent, { type: 'stream.hello' }>['overlay']): OceEvent {
  return { type: 'stream.hello', protocol: 1, resumed, revision: 0, ...(overlay ? { overlay } : {}) };
}

export function textDelta(partId: string, offset: number, text: string, messageId = 'm'): OceEvent {
  return { type: 'part.delta', messageId, partId, field: 'text', offset, text, partType: 'text' };
}

export function partUpserted(part: Omit<OcePart, 'sessionId'> & { sessionId?: string }, sessionId: string, final = false): OceEvent {
  return { type: 'part.upserted', part: { sessionId, ...part }, final };
}
