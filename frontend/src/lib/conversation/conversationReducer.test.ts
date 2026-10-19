import { describe, expect, it } from 'vitest';
import { seedView } from '../../test/oceFixtures';
import { decode } from '../sessionViewParts';
import type { Session } from '../api.session.types';
import type { SessionView } from '../sessionViewTypes';
import { reduceConversation } from './conversationReducer';
import { coalesceDeltas } from './deltaBatch';
import { reduceLiveSessionView } from './liveSessionView';
import type { LiveOverlay, OceEnvelope, OceEvent, OcePrompt } from './events';

// One fixed cursor, so identity checks see only the event's own effect.
const env = (event: OceEvent, sessionId = 's'): OceEnvelope => ({
  v: 1, epoch: 'e', seq: 1, sessionId, platform: 'opencode', at: 0, durability: 'live', event,
});
const seed = (): SessionView => ({ ...seedView('s'), cursor: 'e:1' });
const fold = (view: SessionView, ...events: OceEvent[]) => reduceConversation(view, events.map((e) => env(e)));
const permission = (id: string, sessionId = 's'): OcePrompt => ({
  id, kind: 'permission', sessionId, askedAt: 9, permission: { permission: 'bash', patterns: ['ls'], always: ['ls *'], metadata: { c: 1 } },
});
const question = (id: string, questions = [{ question: 'Q?', header: 'H', options: [] }]): OcePrompt => ({
  id, kind: 'question', sessionId: 's', askedAt: 9, question: { questions },
});
const status = (s: 'busy' | 'done' | 'limited' | 'error', extra: Partial<Extract<OceEvent, { type: 'session.status' }>> = {}): OceEvent => ({ type: 'session.status', status: s, ...extra });
const part = (id: string, data: Record<string, unknown>, final = false): OceEvent => ({
  type: 'part.upserted', final, part: { id, messageId: 'm', sessionId: 's', data: data as never },
});
const dataOf = (view: SessionView, id: string) => decode(view.parts.find((p) => p.id === id)!);

describe('reduceConversation', () => {
  it('returns the same view for an empty or foreign batch and tracks the cursor', () => {
    const view = seedView('s');
    expect(reduceConversation(view, [])).toBe(view);
    expect(reduceConversation(view, [env(status('busy'), 'other')])).toBe(view);
    const next = reduceConversation(view, [{ ...env(status('busy')), epoch: 'x', seq: 4 }]);
    expect(next.cursor).toBe('x:4');
    expect(reduceConversation(next, [{ ...env({ type: 'turn.started', startedAt: 1 }), epoch: 'x', seq: 4 }])).toBe(next);
    for (const type of ['future.event', 'toString', 'constructor']) {
      expect(reduceConversation(next, [{ ...env({ type } as never), epoch: 'x', seq: 4 }])).toBe(next);
    }
  });

  it('applies status levels, notices and limits', () => {
    const notice = { kind: 'retry', message: 'again', retryAt: 5, attempt: 2 };
    let view = fold(seed(), status('busy', { notice }));
    expect(view.session).toMatchObject({ status: 'busy', notice });
    view = fold(view, status('limited', { resetsAt: 77 }));
    // A newer level supersedes a backoff notice even without an explicit clear.
    expect(view.session).toMatchObject({ status: 'limited', resetsAt: 77, notice: undefined });
    view = fold(view, status('error', { notice: { kind: 'models_exhausted', message: 'm', retryAt: 0, attempt: 0 } }), status('busy'));
    expect(view.session?.notice?.kind).toBe('models_exhausted');
    view = fold(view, status('busy', { notice: null }));
    expect(view.session).toMatchObject({ resetsAt: 0, notice: undefined });
    const headless = fold({ ...seed(), session: null }, status('busy'));
    expect(headless.session).toBeNull();
    expect(headless._statusRevision).toBe(1);
  });

  it('clears background work on a provisional idle edge only', () => {
    const view = fold(seed(), { type: 'background.changed', count: 1, tasks: [{ id: 't', description: 'shell' }] });
    expect(fold(view, status('done')).backgroundTaskCount).toBe(1);
    expect(fold(view, status('done', { provisional: true }))).toMatchObject({ backgroundTaskCount: 0, backgroundTasks: [] });
  });

  it('patches session metadata', () => {
    const view = seed();
    expect(fold(view, { type: 'session.updated', patch: {} })).toBe(view);
    expect(fold({ ...view, session: null }, { type: 'session.updated', patch: { title: 't' } }).session).toBeNull();
    const next = fold(view, { type: 'session.updated', patch: {
      title: 'T', summaryAdditions: 1, summaryDeletions: 2, summaryFiles: 3, shareUrl: 'u', archived: true,
      defaultAgent: 'plan', defaultModel: 'p/m', revert: null,
    } });
    expect(next.session).toMatchObject({ title: 'T', summaryAdditions: 1, summaryDeletions: 2, summaryFiles: 3, shareUrl: 'u', archived: true, defaultAgent: 'plan', defaultModel: 'p/m' });
  });

  it('keeps descendants in the session tree and their status levels', () => {
    const row = seed().session as Session;
    const tree: SessionView = { ...seed(), sessionTree: [row, { ...row, id: 'c', parentId: 's', title: 'old' }] };
    const child = { id: 'c', parentId: 's', title: '', timeCreated: 1, timeUpdated: 0 };
    expect(fold(tree, { type: 'child.upserted', child: { ...child, id: 'unknown' } })._refetchRequested).toBe(true);
    let view = fold(tree, { type: 'child.upserted', child: { ...child, title: 'new', timeUpdated: 9 } });
    expect(view.sessionTree[1]).toMatchObject({ title: 'new', timeUpdated: 9 });
    view = fold(view, { type: 'child.upserted', child });
    expect(view.sessionTree[1]).toMatchObject({ title: 'new', timeUpdated: 9 });
    const notice = { kind: 'retry', message: 'x', retryAt: 0, attempt: 1 };
    view = fold(view, { type: 'child.status', childId: 'c', status: 'busy', notice }, { type: 'child.status', childId: 'x', status: 'done' });
    expect(view.sessionTree[1].status).toBe('busy');
    expect(view.childStatus).toEqual({ c: { status: 'busy', notice }, x: { status: 'done' } });
    view = fold(view, { type: 'child.upserted', child: { ...child, deleted: true } }, { type: 'child.upserted', child: { ...child, id: 'x', deleted: true } });
    expect(view.sessionTree.map((s) => s.id)).toEqual(['s']);
    expect(view.childStatus).toEqual({});
  });

  it('records bookkeeping and reconcile requests', () => {
    let view = fold(seed(), { type: 'history.committed', revision: 3 });
    expect(view.lastRevision).toBe(3);
    expect(fold(view, { type: 'history.committed', revision: 3 }).lastRevision).toBe(3);
    for (const event of [
      { type: 'turn.ended', outcome: 'done', endedAt: 1 },
      { type: 'history.rewritten', reason: 'revert' },
      { type: 'stream.reset', reason: 'epoch' },
    ] as OceEvent[]) expect(fold(view, event)._refetchRequested).toBe(true);
    view = fold(view, { type: 'session.error', error: { message: 'x' } }, { type: 'stream.error', code: 'internal', message: 'x', retryable: true });
    expect(view._refetchRequested).toBe(false);
  });

  it('removes messages and parts', () => {
    let view = fold(seed(), { type: 'message.upserted', message: { id: 'm', sessionId: 's', timeCreated: 1, data: { role: 'assistant' } } }, part('p', { type: 'text', text: 'x' }));
    expect(fold(view, { type: 'message.removed', messageId: 'nope', reason: 'deleted' })).toBe(view);
    expect(fold(view, { type: 'part.removed', messageId: 'm', partId: 'nope' })).toBe(view);
    expect(fold(view, { type: 'part.removed', messageId: 'm', partId: 'p' }).parts).toEqual([]);
    view = fold(view, { type: 'message.removed', messageId: 'm', reason: 'reverted' });
    expect([view.messages, view.parts]).toEqual([[], []]);
  });

  it('inserts messages by creation time', () => {
    const msg = (id: string, timeCreated: number): OceEvent => ({ type: 'message.upserted', message: { id, sessionId: 's', timeCreated, data: { role: 'user' } } });
    const view = fold(seed(), msg('b', 2), msg('a', 1), msg('c', 3), msg('a', 1));
    expect(view.messages.map((m) => m.id)).toEqual(['a', 'b', 'c']);
  });
});

describe('anchored deltas', () => {
  it('create typed stubs for the first delta of a part', () => {
    const view = fold(seed(),
      { type: 'part.delta', messageId: 'm', partId: 'out', field: 'tool.output', offset: 0, text: 'o' },
      { type: 'part.delta', messageId: 'm', partId: 'in', field: 'tool.input', offset: 0, text: '{', partType: 'tool' },
      { type: 'part.delta', messageId: 'm', partId: 'r', field: 'text', offset: 0, text: 'think', partType: 'reasoning' },
      { type: 'part.delta', messageId: 'm', partId: 'e', field: 'text', offset: 0, text: '' });
    expect(dataOf(view, 'out')).toEqual({ type: 'tool', state: { output: 'o' } });
    expect(dataOf(view, 'in')).toEqual({ type: 'tool', state: { raw: '{' } });
    expect(dataOf(view, 'r')).toEqual({ type: 'reasoning', text: 'think' });
    expect(view.parts.find((p) => p.id === 'e')).toBeUndefined();
    expect(view.messages.map((m) => m.id)).toEqual(['m']);
    expect(view._deltaOwnedFields.get('in')).toEqual(new Set(['state.raw']));
  });

  it('refuse gaps, skip fully seen text and heal on a caught-up upsert', () => {
    let view = fold(seed(), part('p', { type: 'text', text: 'abc' }));
    const gap: OceEvent = { type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset: 9, text: 'z' };
    view = fold(view, gap);
    expect(view._stale?.has('p')).toBe(true);
    expect(fold(view, gap)).toBe(view);
    expect(fold(view, { type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset: 1, text: 'b' })).toBe(view);
    expect(fold(view, part('p', { type: 'text', text: 'ab' }))._stale?.has('p')).toBe(true);
    expect(fold(view, part('p', { type: 'text', text: 'abc' }))._stale?.has('p')).toBe(true);
    expect(fold(view, part('p', { type: 'text', text: 'abcdefghiz' }, true))._stale?.has('p')).toBe(false);
    expect(fold(view, { type: 'part.removed', messageId: 'm', partId: 'p' })._stale?.has('p')).toBe(false);
    const orphan = fold(seed(), { ...gap, partId: 'new' });
    expect(orphan._stale?.has('new')).toBe(true);
    expect(fold(orphan, part('new', { type: 'text', text: '' }))._stale?.has('new')).toBe(true);
  });

  it('keep the longer streamed value on non-final upserts for every streaming field', () => {
    let view = fold(seed(), part('t', { type: 'tool', state: { status: 'running', output: 'long output', raw: '{"a":1}' } }));
    view = fold(view, part('t', { type: 'tool', state: { status: 'running', title: 'x' } }));
    expect(dataOf(view, 't').state).toEqual({ status: 'running', title: 'x', output: 'long output', raw: '{"a":1}' });
    view = fold(view, part('t', { type: 'tool', state: { status: 'completed', output: 'short' } }, true));
    expect(dataOf(view, 't').state).toEqual({ status: 'completed', output: 'short' });
  });

  it('coalesce only contiguous deltas for the same part, field and session', () => {
    const d = (offset: number, text: string, extra: Partial<Extract<OceEvent, { type: 'part.delta' }>> = {}, sessionId = 's') =>
      env({ type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset, text, ...extra }, sessionId);
    const merged = coalesceDeltas([d(0, 'ab'), d(2, 'c', { partType: 'text' }), d(5, 'x'), d(6, 'y', { field: 'tool.output' }), d(7, 'z', {}, 'o'), env(status('busy'))]);
    expect(merged.map((e) => e.event.type === 'part.delta' ? [e.event.offset, e.event.text, e.event.partType] : e.event.type))
      .toEqual([[0, 'abc', 'text'], [5, 'x', undefined], [6, 'y', undefined], [7, 'z', undefined], 'session.status']);
  });
});

describe('prompts', () => {
  it('shows, judges and resolves a permission', () => {
    let view = fold(seed(), { type: 'prompt.asked', prompt: permission('p1') });
    expect(view.pendingPermission).toEqual({ permissionId: 'p1', permission: 'bash', patterns: ['ls'], always: ['ls *'], metadata: { c: 1 }, sessionId: 's', askedAt: 9 });
    expect(fold(view, { type: 'prompt.asked', prompt: permission('p1') })).toBe(view);
    view = fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'scheduled', startsAt: 5 });
    expect(view.judgeStartsAt).toBe(5);
    expect(fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'scheduled', startsAt: 5 })).toBe(view);
    expect(fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'scheduled' })).toBe(view);
    expect(fold(view, { type: 'prompt.judge', promptId: 'other', phase: 'flagged', reasoning: 'r' })).toBe(view);
    view = fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'checking' });
    expect(view).toMatchObject({ judgeStartsAt: null, checkingPermissionId: 'p1' });
    expect(fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'checking' })).toBe(view);
    expect(fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'flagged' })).toBe(view);
    expect(fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'unknown' as never })).toBe(view);
    view = fold(view, { type: 'prompt.judge', promptId: 'p1', phase: 'flagged', reasoning: 'risky' });
    expect(view).toMatchObject({ checkingPermissionId: null, judgeReasoning: 'risky' });
    view = fold(view, { type: 'prompt.resolved', promptId: 'p1', kind: 'permission', outcome: 'once', by: 'user' });
    expect(view).toMatchObject({ pendingPermission: null, judgeReasoning: null });
    expect(fold(view, { type: 'prompt.resolved', promptId: 'p1', kind: 'permission', outcome: 'once', by: 'ai' })).toBe(view);
  });

  it('keeps another prompt shown when a different permission resolves', () => {
    let view = fold(seed(), { type: 'prompt.asked', prompt: permission('p2') }, { type: 'prompt.judge', promptId: 'p1', phase: 'checking' });
    expect(view.checkingPermissionId).toBe('p1');
    view = fold(view, { type: 'prompt.resolved', promptId: 'p1', kind: 'permission', outcome: 'once', by: 'ai' });
    expect(view).toMatchObject({ checkingPermissionId: null, pendingPermission: { permissionId: 'p2' } });
    expect(fold(view, { type: 'prompt.resolved', promptId: 'p3', kind: 'permission', outcome: 'once', by: 'ai' })).toBe(view);
  });

  it('defaults sparse permission requests', () => {
    const view = fold(seed(), { type: 'prompt.asked', prompt: { id: 'p', kind: 'permission', sessionId: 'c', askedAt: 1 } });
    expect(view.pendingPermission).toEqual({ permissionId: 'p', permission: 'Permission required', patterns: [], sessionId: 'c', askedAt: 1 });
  });

  it('shows and resolves questions with content', () => {
    expect(fold(seed(), { type: 'prompt.asked', prompt: question('q', []) }).pendingQuestion).toBeNull();
    let view = fold(seed(), { type: 'prompt.asked', prompt: question('q') });
    expect(view.pendingQuestion).toMatchObject({ requestId: 'q', sessionID: 's' });
    expect(fold(view, { type: 'prompt.asked', prompt: question('q') })).toBe(view);
    expect(fold(view, { type: 'prompt.resolved', promptId: 'x', kind: 'question', outcome: 'answered', by: 'external' })).toBe(view);
    view = fold(view, { type: 'prompt.resolved', promptId: 'q', kind: 'question', outcome: 'answered', by: 'external' });
    expect(view.pendingQuestion).toBeNull();
  });
});

describe('stream.hello', () => {
  const overlay = (extra: Partial<LiveOverlay> = {}): LiveOverlay => ({
    status: { type: 'session.status', status: 'busy' }, prompts: [], judge: [],
    background: { type: 'background.changed', count: 1, tasks: [{ id: 't', description: 'd' }] },
    inflight: { messages: [{ id: 'm', sessionId: 's', timeCreated: 1, data: { role: 'assistant' } }], parts: [{ id: 'p', messageId: 'm', sessionId: 's', data: { type: 'text', text: 'live' } }] },
    liveConnection: false, ...extra,
  });
  const hello = (resumed: boolean, o?: LiveOverlay): OceEvent => ({ type: 'stream.hello', protocol: 1, resumed, revision: 0, ...(o ? { overlay: o } : {}) });

  it('restates live state from the overlay unless resumed', () => {
    const view = seed();
    expect(fold(view, hello(true, overlay())).session).toBe(view.session);
    expect(fold(view, hello(false)).session).toBe(view.session);
    const next = fold(view, hello(false, overlay()));
    expect(next.session).toMatchObject({ status: 'busy', liveConnection: false });
    expect(next).toMatchObject({ backgroundTaskCount: 1, messages: [{ id: 'm' }] });
    expect(dataOf(next, 'p').text).toBe('live');
    expect(fold(next, hello(false, overlay())).session?.liveConnection).toBe(false);
    const sparse = fold(view, hello(false, { ...overlay(), prompts: undefined, judge: undefined, background: undefined, inflight: undefined } as never));
    expect(sparse.backgroundTaskCount).toBe(0);
  });

  it('keeps a still-open prompt, replaces closed ones and replays judge phases', () => {
    let view = fold(seed(), { type: 'prompt.asked', prompt: permission('keep') }, { type: 'prompt.judge', promptId: 'keep', phase: 'flagged', reasoning: 'r' }, { type: 'prompt.asked', prompt: question('gone') });
    const kept = fold(view, hello(false, overlay({ prompts: [permission('new'), permission('keep')] })));
    expect(kept.pendingPermission).toBe(view.pendingPermission);
    expect(kept.judgeReasoning).toBe('r');
    expect(kept.pendingQuestion).toBeNull();
    view = fold(view, hello(false, overlay({ prompts: [permission('new'), question('q2')], judge: [{ type: 'prompt.judge', promptId: 'new', phase: 'scheduled', startsAt: 3 }] })));
    expect(view).toMatchObject({ pendingPermission: { permissionId: 'new' }, pendingQuestion: { requestId: 'q2' }, judgeReasoning: null, judgeStartsAt: 3 });
    const sameQuestion = fold(view, hello(false, overlay({ prompts: [question('q2')] })));
    expect(sameQuestion.pendingQuestion).toBe(view.pendingQuestion);
    expect(sameQuestion.pendingPermission).toBeNull();
  });
});

describe('reduceLiveSessionView', () => {
  it('folds stream batches and keeps stream bookkeeping across loads of the same session', () => {
    const view = reduceLiveSessionView(seed(), { type: 'oceBatch', envelopes: [
      { ...env({ type: 'history.committed', revision: 2 }), epoch: 'e', seq: 3 },
      env({ type: 'child.status', childId: 'c', status: 'busy' }),
      env({ type: 'part.delta', messageId: 'm', partId: 'p', field: 'text', offset: 4, text: 'gap' }),
    ] });
    expect(view._stale?.size).toBe(1);
    const reloaded = reduceLiveSessionView(view, { type: 'load', view: seed(), mode: 'reconcile' });
    expect(reloaded).toMatchObject({ lastRevision: 2, childStatus: { c: { status: 'busy' } }, _stale: undefined });
    expect(reloaded.cursor).toBe(view.cursor);
    const other = reduceLiveSessionView(view, { type: 'load', view: seedView('t') });
    expect(other).toMatchObject({ sessionId: 't', cursor: undefined, lastRevision: undefined, childStatus: undefined });
    const cleared = reduceLiveSessionView(view, { type: 'clearPrompt', kind: 'question', id: 'x' });
    expect(cleared).toBe(view);
  });
});
