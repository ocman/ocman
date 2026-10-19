// Test-only harness: folds OCE goldens from internal/convevents/v1map/testdata
// (design §13.2) through the conversation stream client and reducer.

import { initialSessionView, type SessionView } from '../lib/sessionViewTypes';
import { decode } from '../lib/sessionViewParts';
import type { Session } from '../lib/api.session.types';
import { createConversationStream } from '../lib/conversation/conversationStream';
import { reduceLiveSessionView } from '../lib/conversation/liveSessionView';
import type { OceEnvelope } from '../lib/conversation/events';

export interface Fixture { name: string; sessionId: string; envelopes: OceEnvelope[] }

export function parseFixture(name: string, oce: string): Fixture {
  const envelopes = oce.split('\n').filter((l) => l.trim()).map((l) => JSON.parse(l) as OceEnvelope);
  return { name, sessionId: envelopes[0]?.sessionId ?? '', envelopes };
}

export function seedView(sessionId: string): SessionView {
  const session: Session = {
    id: sessionId, platform: 'opencode', projectId: 'p', title: 'Seed', directory: '/repo', timeCreated: 1, timeUpdated: 1,
    summaryAdditions: null, summaryDeletions: null, summaryFiles: null, shareUrl: null, messageCount: 0, durationMs: 0,
    activeDurationMs: 0, totalInputTokens: 0, totalOutputTokens: 0, totalCost: 0, status: 'done', liveConnection: true,
    pendingPermission: false, pendingQuestion: false, archived: false, seen: true, pinned: false, pinnedAt: 0,
    seenTimeUpdated: 0, unreadCount: 0,
  };
  return { ...initialSessionView(sessionId), session, _statusFresh: true };
}

export interface FoldResult { view: SessionView; dirty: number; reconciles: number }

interface ListenerSource { onmessage: ((e: MessageEvent) => void) | null; listeners: Map<string, (e: MessageEvent) => void> }

function fakeSource(): ListenerSource & EventSource {
  const src: ListenerSource = { onmessage: null, listeners: new Map() };
  return Object.assign(src, {
    addEventListener: (name: string, l: (e: MessageEvent) => void) => src.listeners.set(name, l),
  }) as unknown as ListenerSource & EventSource;
}

const message = (data: string) => ({ data } as MessageEvent);

/** Load every `<platform>/<scenario>.oce.jsonl`, keyed by path. */
export function loadFixtures(files: Record<string, string>): Fixture[] {
  const fixtures: Fixture[] = [];
  for (const path of Object.keys(files).sort()) {
    const match = /([^/]+)\/([^/]+)\.oce\.jsonl$/.exec(path);
    if (match) fixtures.push(parseFixture(`${match[1]}/${match[2]}`, files[path]));
  }
  return fixtures;
}

/** `settle` runs before the stream is disposed, e.g. to advance fake timers. */
export function foldOce(sessionId: string, envelopes: readonly OceEnvelope[], seed = seedView(sessionId), settle?: () => void): FoldResult {
  let view = seed;
  const out = { dirty: 0, reconciles: 0 };
  const stream = createConversationStream({
    sessionId,
    dispatch: (action) => { view = reduceLiveSessionView(view, action); },
    reconcile: async () => { out.reconciles++; return true; },
    recover: async () => { out.reconciles++; return true; },
    dirty: () => { out.dirty++; },
  });
  const source = fakeSource();
  stream.attach(source, false);
  const handle = source.listeners.get('oce')!;
  for (const env of envelopes) handle(message(JSON.stringify(env)));
  stream.flush();
  settle?.();
  stream.dispose();
  return { view, ...out };
}

/** The comparable view: `_` bookkeeping and stream-only fields dropped, part data decoded. */
export function comparable(view: SessionView): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [key, value] of Object.entries(view)) {
    if (key.startsWith('_') || key === 'cursor' || key === 'lastRevision' || key === 'childStatus') continue;
    out[key] = value;
  }
  out.parts = view.parts.map((p) => ({ id: p.id, messageId: p.messageId, sessionId: p.sessionId, data: decode(p) }));
  return JSON.parse(JSON.stringify(out)) as Record<string, unknown>;
}
