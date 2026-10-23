// Pure reducer for the SessionDetail page's REST loads and client actions.
// Stream folding is covered by lib/conversation/conversationReducer.test.ts.

import { describe, it, expect } from 'vitest';
import type { Message, Part, SessionDetail } from './api';
import type { QuestionItem } from '../components/session/QuestionPrompt';

type SessionRow = SessionDetail['session'];
import {
  initialSessionView,
  reduceSessionView,
  seedDeltaOwnedFields,
  type SessionView,
} from './sessionReducer';

const SID = 'sess-1';

function makeSession(overrides: Partial<SessionRow> = {}): SessionRow {
  return {
    id: SID,
    platform: 'opencode',
    projectId: 'proj-1',
    title: 'Test session',
    directory: '/tmp/proj',
    timeCreated: 1_000,
    timeUpdated: 2_000,
    summaryAdditions: null,
    summaryDeletions: null,
    summaryFiles: null,
    shareUrl: null,
    messageCount: 0,
    durationMs: 0,
    activeDurationMs: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    liveConnection: true,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
    pinned: false,
    pinnedAt: 0,
    seenTimeUpdated: 0,
    unreadCount: 0,
    ...overrides,
  };
}

function makeMessage(id: string, timeCreated: number, overrides: Partial<Message['data']> = {}): Message {
  return {
    id,
    sessionId: SID,
    timeCreated,
    data: { role: 'assistant', ...overrides },
  };
}

function makeTextPart(id: string, messageId: string, text: string, extra: Record<string, unknown> = {}): Part {
  return {
    id,
    messageId,
    sessionId: SID,
    data: { type: 'text', text, ...extra } as unknown as string,
  };
}

function makeToolPart(id: string, messageId: string, tool: string, state: Record<string, unknown>): Part {
  return {
    id,
    messageId,
    sessionId: SID,
    data: { type: 'tool', tool, state } as unknown as string,
  };
}

function decode(p: Part): Record<string, unknown> {
  const d = p.data as unknown;
  return typeof d === 'string' ? JSON.parse(d) as Record<string, unknown> : d as Record<string, unknown>;
}

function makeView(overrides: Partial<SessionView> = {}): SessionView {
  return {
    ...initialSessionView(SID),
    session: makeSession(),
    ...overrides,
  };
}

// Inline factory so tests don't repeat the QuestionOption shape.
function yesNoQuestion(label = 'Continue?'): QuestionItem {
  return {
    question: label,
    header: label,
    options: [
      { label: 'yes', description: '' },
      { label: 'no', description: '' },
    ],
  };
}

describe('reduceSessionView — load action', () => {
  it('replaces state wholesale on `load`', () => {
    const before = makeView({
      messages: [makeMessage('m-old', 1)],
      parts: [makeTextPart('p-old', 'm-old', 'stale')],
      pendingPermission: { permissionId: 'p1', permission: 'old', patterns: [], sessionId: SID, askedAt: 0 },
    });
    const fresh = makeView({
      messages: [makeMessage('m-new', 10)],
      parts: [makeTextPart('p-new', 'm-new', 'fresh')],
    });
    const after = reduceSessionView(before, { type: 'load', view: fresh });
    expect(after.messages.map((m) => m.id)).toEqual(['m-new']);
    expect(after.parts.map((p) => p.id)).toEqual(['p-new']);
    // load replaces side-channels too — disconnect-reload semantics
    // (requirement: disconnect = reload).
    expect(after.pendingPermission).toBe(null);
  });

  it('discards delta-ownership tracking on load (fresh snapshot is authoritative)', () => {
    // After a reload we trust the server: any field that was
    // delta-owned in the previous session lifetime is irrelevant
    // because we just refetched the whole conversation.
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'streamed locally')],
    });
    const withDelta = { ...before, _deltaOwnedFields: new Map([['p1', new Set(['text'])]]) };
    const reloaded = reduceSessionView(withDelta, {
      type: 'load',
      view: makeView({
        messages: [makeMessage('m1', 1)],
        parts: [makeTextPart('p1', 'm1', 'authoritative')],
      }),
    });
    expect(decode(reloaded.parts[0]).text).toBe('authoritative');
  });

  it('preserves delta-ownership supplied by a cache-seeded load', () => {
    const seeded = new Map([['p1', new Set(['text'])]]);
    const cached = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 4 5 ')],
      _deltaOwnedFields: seeded,
    });

    const after = reduceSessionView(initialSessionView(SID), { type: 'load', view: cached });

    expect(after._deltaOwnedFields.get('p1')?.has('text')).toBe(true);
  });

  it('drops a reconcile load whose sessionId does not match the current state', () => {
    // A stale doFetch from a previous navigation can resolve after the user
    // has already switched to a new session. Applying it wholesale would paint
    // the old session's messages into the current view and, via the cache-mirror
    // effect, corrupt the cache for the current session. The correct behaviour
    // is to return the current state unchanged and discard the stale incoming data.
    const before = makeView({
      session: makeSession({ id: 'sess-new' }),
      sessionId: 'sess-new',
      messages: [{ ...makeMessage('m-new', 2), sessionId: 'sess-new' }],
      parts: [{ ...makeTextPart('p-new', 'm-new', 'current content'), sessionId: 'sess-new' }],
    });
    const stale = makeView({
      session: makeSession({ id: 'sess-old' }),
      sessionId: 'sess-old',
      messages: [{ ...makeMessage('m-old', 1), sessionId: 'sess-old' }],
      parts: [{ ...makeTextPart('p-old', 'm-old', 'stale stream'), sessionId: 'sess-old' }],
    });

    const after = reduceSessionView(before, { type: 'load', view: stale, mode: 'reconcile' });

    // State must be unchanged — the stale load is dropped entirely.
    expect(after).toBe(before);
    expect(after.sessionId).toBe('sess-new');
    expect(after.messages.map((m) => m.id)).toEqual(['m-new']);
  });
});

describe('reduceSessionView — clearPrompt action', () => {
  it('clears permission when type matches', () => {
    const view = makeView({
      pendingPermission: { permissionId: 'p1', permission: 'X', patterns: [], sessionId: SID, askedAt: 0 },
    });
    const after = reduceSessionView(view, { type: 'clearPrompt', kind: 'permission', id: 'p1' });
    expect(after.pendingPermission).toBe(null);
  });

  it('clears question when type matches', () => {
    const view = makeView({
      pendingQuestion: {
        requestId: 'q1', sessionID: SID,
        questions: [yesNoQuestion('Q?')],
      },
    });
    const after = reduceSessionView(view, { type: 'clearPrompt', kind: 'question', id: 'q1' });
    expect(after.pendingQuestion).toBe(null);
  });

  it('does not clear when id mismatches', () => {
    const perm = { permissionId: 'p1', permission: 'X', patterns: [], sessionId: SID, askedAt: 0 };
    const view = makeView({ pendingPermission: perm });
    const after = reduceSessionView(view, { type: 'clearPrompt', kind: 'permission', id: 'p-other' });
    expect(after.pendingPermission).toBe(perm);
  });
});

describe('reduceSessionView — addNotice action', () => {
  it('includes reasoning when addNotice carries it', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'addNotice',
      notice: {
        permissionId: 'perm-3',
        permission: 'write file',
        patterns: ['*.md'],
        reasoning: 'Modifies documentation only.',
        approvedAt: 5000,
      },
    });
    const noticePart = after.parts.find((p) => p.id === 'ocman-notice-perm-3-part');
    expect(noticePart).toBeDefined();
    expect(decode(noticePart!).reasoning).toBe('Modifies documentation only.');
  });

  it('injects a notice once per permission', () => {
    const notice = { permissionId: 'perm-ai', permission: 'bash', patterns: ['make'], reasoning: 'Builds.', approvedBy: 'ai' as const, reply: 'once' as const, approvedAt: 7000 };
    const once = reduceSessionView(makeView(), { type: 'addNotice', notice });
    expect(reduceSessionView(once, { type: 'addNotice', notice })).toBe(once);
    const parts = once.parts.filter((p) => p.id === 'ocman-notice-perm-ai-part');
    expect(parts).toHaveLength(1);
    expect(decode(parts[0])).toMatchObject({ approvedBy: 'ai', reply: 'once', reasoning: 'Builds.', approvedAt: 7000 });
  });
});

// ---------------------------------------------------------------------------
// seedDeltaOwnedFields — helper used by the host hook on cache revisit
// ---------------------------------------------------------------------------
//
// Regression: "missing sections after switching sessions" (see the
// e2e in session-detail.spec.ts). When the user returns to a session
// that was streaming, the reducer is seeded from the cache. The cache
// stores parts but not the `_deltaOwnedFields` map — without
// re-seeding it, the reconcile load wipes streamed chunks the server
// DB hasn't recorded yet.

describe('seedDeltaOwnedFields', () => {
  it('marks parts with non-empty text as delta-owned on the `text` field', () => {
    const parts = [makeTextPart('p1', 'm1', 'Hello world')];
    const map = seedDeltaOwnedFields(parts);
    expect(map.get('p1')?.has('text')).toBe(true);
  });

  it('does not mark parts whose text is empty', () => {
    const parts = [makeTextPart('p1', 'm1', '')];
    const map = seedDeltaOwnedFields(parts);
    expect(map.has('p1')).toBe(false);
  });

  it('marks tool parts with non-empty state.output as delta-owned on `state.output`', () => {
    const parts = [makeToolPart('p1', 'm1', 'bash', { status: 'completed', output: 'tool log line\n' })];
    const map = seedDeltaOwnedFields(parts);
    expect(map.get('p1')?.has('state.output')).toBe(true);
    expect(map.get('p1')?.has('text')).toBe(false);
  });

  it('marks tool parts with non-empty streamed input as delta-owned on `state.raw`', () => {
    const parts = [makeToolPart('p1', 'm1', 'bash', { status: 'running', raw: '{"command":"ls"}' })];
    expect(seedDeltaOwnedFields(parts).get('p1')).toEqual(new Set(['state.raw']));
  });

  it('returns an empty map when given no parts', () => {
    const map = seedDeltaOwnedFields([]);
    expect(map.size).toBe(0);
  });

  it('handles parts whose data is a JSON-encoded string', () => {
    // The cache normalises some parts to JSON-encoded `data` fields.
    // The seeder must decode them before reading streaming fields.
    const part: Part = {
      id: 'p1',
      messageId: 'm1',
      sessionId: SID,
      data: JSON.stringify({ type: 'text', text: 'streamed' }),
    };
    const map = seedDeltaOwnedFields([part]);
    expect(map.get('p1')?.has('text')).toBe(true);
  });
});

// ---------------------------------------------------------------------------
// upsertSnapshotPart longest-wins rule (via reconcile load)
// ---------------------------------------------------------------------------

describe('reconcile load — live status fence', () => {
  // Idle triggers a refetch; a prompt sent before it returns starts a new
  // turn, and the older snapshot must not flip the live status back.
  it('keeps a status observed live after the refetch started', () => {
    const started = makeView({ session: makeSession({ status: 'done', title: 'old' }) });
    const fence = started._statusRevision ?? 0;
    const live = { ...started, session: makeSession({ status: 'busy', title: 'old' }), _statusRevision: fence + 1 };
    const incoming = makeView({ session: makeSession({ status: 'done', title: 'new' }) });
    const after = reduceSessionView(live, { type: 'load', view: incoming, mode: 'reconcile', statusRevision: fence });
    expect(after.session?.status).toBe('busy');
    expect(after.session?.title).toBe('new');
  });

  it('takes the snapshot status when nothing live arrived meanwhile', () => {
    const before = makeView({ session: makeSession({ status: 'busy' }) });
    const incoming = makeView({ session: makeSession({ status: 'done' }) });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile', statusRevision: before._statusRevision ?? 0 });
    expect(after.session?.status).toBe('done');
  });
});

describe('reconcile load — longest-wins for delta-owned streaming fields', () => {
  it('keeps the local value when the reconcile snapshot has shorter text', () => {
    // Simulates: cache had "1 2 3 4 5 " (delta-owned by seed). Server
    // is DB-lagging and returns "1 2 3 ". Local (longer) must win.
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 4 5 ')],
      _deltaOwnedFields: new Map([['p1', new Set(['text'])]]),
    });
    const incoming = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 ')],
    });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile' });
    expect(decode(after.parts[0]).text).toBe('1 2 3 4 5 ');
  });

  it('takes the snapshot value when it is longer than the local value', () => {
    // Inverse case: the user was away long enough that the DB caught
    // up *past* the cached text. The longer snapshot must win so the
    // user doesn't see permanently-truncated content.
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 ')],
      _deltaOwnedFields: new Map([['p1', new Set(['text'])]]),
    });
    const incoming = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 4 5 6 7 ')],
    });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile' });
    expect(decode(after.parts[0]).text).toBe('1 2 3 4 5 6 7 ');
  });

  it('keeps the local value when the snapshot has equal-length text', () => {
    // Ties go to the local value (avoid churning state with a no-op
    // replacement when the strings happen to match).
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'same')],
      _deltaOwnedFields: new Map([['p1', new Set(['text'])]]),
    });
    const incoming = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'same')],
    });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile' });
    expect(decode(after.parts[0]).text).toBe('same');
  });

  it('keeps the local value when the snapshot omits the streaming field entirely', () => {
    // Defensive: a snapshot that doesn't return `text` at all (e.g.
    // because the part shape changed) must not blank the local value.
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', '1 2 3 4 5 ')],
      _deltaOwnedFields: new Map([['p1', new Set(['text'])]]),
    });
    const incoming = makeView({
      messages: [makeMessage('m1', 1)],
      // Part with no `text` field at all.
      parts: [{ id: 'p1', messageId: 'm1', sessionId: SID, data: { type: 'text' } as unknown as string }],
    });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile' });
    expect(decode(after.parts[0]).text).toBe('1 2 3 4 5 ');
  });

  it('still replaces non-streaming fields wholesale when the snapshot lands', () => {
    // Tool part: state.status (non-streaming) must come from the
    // snapshot; state.output (streaming, delta-owned) must keep local
    // when it is the longer of the two.
    const before = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeToolPart('p1', 'm1', 'bash', { status: 'running', output: 'line1\nline2\n' })],
      _deltaOwnedFields: new Map([['p1', new Set(['state.output'])]]),
    });
    const incoming = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeToolPart('p1', 'm1', 'bash', { status: 'completed', output: 'line1\n' })],
    });
    const after = reduceSessionView(before, { type: 'load', view: incoming, mode: 'reconcile' });
    const decoded = decode(after.parts[0]);
    expect((decoded.state as Record<string, unknown>).status).toBe('completed');
    expect((decoded.state as Record<string, unknown>).output).toBe('line1\nline2\n');
  });
});

// regression: navigating A -> B while a reverse-sync listPermissions
// call is in flight used to inject A's prompt into B, disabling B's
// composer behind a dialog that could never be answered. The reducer
// now drops a permission that belongs to another session, while still
// accepting prompts bubbled up from the page session's subagents.
describe('setPendingPermission session guard', () => {
  const perm = (sessionId: string) => ({
    permissionId: 'perm-1',
    permission: 'Bash command',
    patterns: [],
    sessionId,
    askedAt: 1_000,
  });

  it('accepts a permission for the current session', () => {
    const next = reduceSessionView(initialSessionView(SID), {
      type: 'setPendingPermission',
      permission: perm(SID),
    });
    expect(next.pendingPermission?.permissionId).toBe('perm-1');
  });

  it('accepts a legacy permission with no sessionId', () => {
    const next = reduceSessionView(initialSessionView(SID), {
      type: 'setPendingPermission',
      permission: perm(''),
    });
    expect(next.pendingPermission?.permissionId).toBe('perm-1');
  });

  it('drops a permission belonging to another session', () => {
    const state = initialSessionView(SID);
    const next = reduceSessionView(state, {
      type: 'setPendingPermission',
      permission: perm('sess-other'),
    });
    expect(next).toBe(state);
    expect(next.pendingPermission).toBeNull();
  });

  it('accepts a subagent permission listed in ownerIds', () => {
    const next = reduceSessionView(initialSessionView(SID), {
      type: 'setPendingPermission',
      permission: perm('sess-subagent'),
      ownerIds: [SID, 'sess-subagent'],
    });
    expect(next.pendingPermission?.permissionId).toBe('perm-1');
  });
});

describe('setPendingQuestion', () => {
  const question = (sessionID: string, requestId = 'q-1') => ({ requestId, sessionID, questions: [yesNoQuestion()] });

  it('shows a question for the current session or with no owner', () => {
    for (const owner of [SID, '']) {
      const next = reduceSessionView(initialSessionView(SID), { type: 'setPendingQuestion', question: question(owner) });
      expect(next.pendingQuestion).toEqual(question(owner));
    }
  });

  it('ignores another session, the same request and empty questions', () => {
    const state = initialSessionView(SID);
    expect(reduceSessionView(state, { type: 'setPendingQuestion', question: question('sess-other') })).toBe(state);
    expect(reduceSessionView(state, { type: 'setPendingQuestion', question: { ...question(SID), requestId: '' } })).toBe(state);
    expect(reduceSessionView(state, { type: 'setPendingQuestion', question: { ...question(SID), questions: [] } })).toBe(state);
    const shown = reduceSessionView(state, { type: 'setPendingQuestion', question: question(SID) });
    expect(reduceSessionView(shown, { type: 'setPendingQuestion', question: question(SID) })).toBe(shown);
    const next = reduceSessionView(shown, { type: 'setPendingQuestion', question: question(SID, 'q-2') });
    expect(next.pendingQuestion?.requestId).toBe('q-2');
  });
});

describe('patchSession', () => {
  it('patches loaded metadata only', () => {
    const empty = initialSessionView(SID);
    expect(reduceSessionView(empty, { type: 'patchSession', patch: { title: 'x' } })).toBe(empty);
    expect(reduceSessionView(makeView(), { type: 'patchSession', patch: { title: 'x' } }).session?.title).toBe('x');
  });
});
