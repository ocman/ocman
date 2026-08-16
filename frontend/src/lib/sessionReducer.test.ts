// Pure reducer for the SessionDetail page. Tests live next to the
// implementation and exercise the action table from
// spec/sse-rewrite/architecture.md directly — no React, no SSE.
//
// The five user-visible behaviours from requirements.md each have a
// dedicated `regression:` block so a future developer can grep for
// them when investigating a bug.

import { describe, it, expect } from 'vitest';
import type { Message, Part, SessionDetail } from './api';
import type { QuestionItem } from '../components/session/QuestionPrompt';

type SessionRow = SessionDetail['session'];
import {
  initialSessionView,
  reduceSessionView,
  seedDeltaOwnedFields,
  type SessionView,
  type SseEvent,
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

// Helper to wrap an SSE event body into the `{type, properties}`
// shape that OpenCode emits over the wire.
function sseEvent(type: string, properties: Record<string, unknown>): SseEvent {
  return { type, properties };
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
    const withDelta = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'text', delta: ' more',
      }),
    });
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

describe('reduceSessionView — message.created / message.updated', () => {
  it('upserts the message info sorted by timeCreated', () => {
    let view = makeView({ messages: [makeMessage('m1', 100)] });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'm0', sessionID: SID, role: 'user', time: { created: 50 } },
        parts: [],
      }),
    });
    expect(view.messages.map((m) => m.id)).toEqual(['m0', 'm1']);
  });

  it('appends embedded parts (id-deduped) from the snapshot', () => {
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'm1', sessionID: SID, role: 'assistant', time: { created: 1 } },
        parts: [
          { id: 'p1', messageID: 'm1', sessionID: SID, type: 'text', text: 'hello' },
          { id: 'p2', messageID: 'm1', sessionID: SID, type: 'tool', tool: 'bash', state: { status: 'running' } },
        ],
      }),
    });
    expect(view.parts.map((p) => p.id)).toEqual(['p1', 'p2']);
    expect(decode(view.parts[0]).text).toBe('hello');
  });

  it('replaces a part on collision (same id, new snapshot)', () => {
    let view = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'old')],
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.updated', {
        info: { id: 'm1', sessionID: SID, role: 'assistant', time: { created: 1 } },
        parts: [
          { id: 'p1', messageID: 'm1', sessionID: SID, type: 'text', text: 'new' },
        ],
      }),
    });
    expect(view.parts).toHaveLength(1);
    expect(decode(view.parts[0]).text).toBe('new');
  });

  it('regression: tool block from message.created renders immediately', () => {
    // Requirement: tool blocks must appear in the conversation
    // thread within one animation frame.
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'm1', sessionID: SID, role: 'assistant', time: { created: 1 } },
        parts: [
          { id: 'p-tool', messageID: 'm1', sessionID: SID, type: 'tool', tool: 'bash', state: { status: 'running' } },
        ],
      }),
    });
    const toolPart = view.parts.find((p) => p.id === 'p-tool');
    expect(toolPart).toBeDefined();
    expect(decode(toolPart!).type).toBe('tool');
  });
});

describe('reduceSessionView — message.part.updated (snapshot)', () => {
  it('upserts a single part by id', () => {
    let view = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'hi')],
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: { id: 'p2', messageID: 'm1', sessionID: SID, type: 'text', text: 'second' },
      }),
    });
    expect(view.parts.map((p) => p.id)).toEqual(['p1', 'p2']);
  });

  it('snapshot wins on fields that have not received deltas', () => {
    let view = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'old text')],
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: { id: 'p1', messageID: 'm1', sessionID: SID, type: 'text', text: 'new text' },
      }),
    });
    expect(decode(view.parts[0]).text).toBe('new text');
  });

  it('delta-owned text field is preserved when a later snapshot lands', () => {
    let view = makeView({ messages: [makeMessage('m1', 1)] });
    // First delta arrives — establishes ownership of `text`.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'text', delta: 'Hello ',
      }),
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'text', delta: 'world',
      }),
    });
    // Stale snapshot arrives later — must not clobber delta value.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: { id: 'p1', messageID: 'm1', sessionID: SID, type: 'text', text: 'stale' },
      }),
    });
    expect(decode(view.parts[0]).text).toBe('Hello world');
  });

  it('delta-owned state.output is preserved while non-streamed fields update', () => {
    let view = makeView({ messages: [makeMessage('m1', 1)] });
    // Establish delta ownership on state.output.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'state.output', delta: 'streamed',
      }),
    });
    // Snapshot updates status (non-streaming field, must win) and
    // output (streaming field, must be preserved).
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p1', messageID: 'm1', sessionID: SID, type: 'tool', tool: 'bash',
          state: { status: 'completed', output: 'stale' },
        },
      }),
    });
    const decoded = decode(view.parts[0]);
    expect(decoded.tool).toBe('bash');
    expect((decoded.state as Record<string, unknown>).status).toBe('completed');
    expect((decoded.state as Record<string, unknown>).output).toBe('streamed');
  });

  it('synthesises a stub assistant message when a tool snapshot arrives first', () => {
    // OpenCode can stream `message.part.updated` for a running tool
    // before the owning `message.created`/`message.updated` lands.
    // Without a stub Message, the tool part exists in reducer state
    // but convertMessages() has no assistant message to attach it to,
    // so the user sees the tool only after completion or refresh.
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p-tool-first',
          messageID: 'm-tool-first',
          sessionID: SID,
          type: 'tool',
          tool: 'bash',
          state: { status: 'running', input: { command: 'sleep 1' } },
        },
      }),
    });

    const stub = view.messages.find((m) => m.id === 'm-tool-first');
    expect(stub).toBeDefined();
    expect(stub!.data.role).toBe('assistant');
    expect(view.parts.find((p) => p.id === 'p-tool-first')).toBeDefined();
  });
});

describe('reduceSessionView — message.part.delta', () => {
  it('appends to text field by partId', () => {
    let view = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeTextPart('p1', 'm1', 'Hello')],
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'text', delta: ' world',
      }),
    });
    expect(decode(view.parts[0]).text).toBe('Hello world');
  });

  it('synthesises a stub part if the partId is unknown', () => {
    let view = makeView({ messages: [makeMessage('m1', 1)] });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p-new', messageID: 'm1', sessionID: SID, field: 'text', delta: 'orphan',
      }),
    });
    expect(view.parts).toHaveLength(1);
    expect(view.parts[0].id).toBe('p-new');
    expect(decode(view.parts[0]).text).toBe('orphan');
  });

  it('synthesises a stub assistant message when delta arrives before message.created', () => {
    // OpenCode quirk: some streams deliver part deltas before the
    // owning message.created. The reducer must keep a stub so the
    // converter has a Message to attach the parts to.
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm-future', sessionID: SID, field: 'text', delta: 'hello',
      }),
    });
    const stub = view.messages.find((m) => m.id === 'm-future');
    expect(stub).toBeDefined();
    expect(stub!.data.role).toBe('assistant');
  });

  it('regression: streaming text never rewinds (deltas always append)', () => {
    // Requirement: text in the bubble only grows — never shortens,
    // never blanks, never replaces with a different prefix.
    let view = makeView({ messages: [makeMessage('m1', 1)] });
    const tokens = ['Hello', ' ', 'world', ',', ' how', ' are', ' you'];
    for (const t of tokens) {
      view = reduceSessionView(view, {
        type: 'sse',
        event: sseEvent('message.part.delta', {
          partID: 'p1', messageID: 'm1', sessionID: SID, field: 'text', delta: t,
        }),
      });
    }
    expect(decode(view.parts[0]).text).toBe('Hello world, how are you');
  });

  it('uses dotted path resolution for state.output', () => {
    let view = makeView({
      messages: [makeMessage('m1', 1)],
      parts: [makeToolPart('p1', 'm1', 'bash', { status: 'running', output: 'line 1\n' })],
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.delta', {
        partID: 'p1', messageID: 'm1', sessionID: SID, field: 'state.output', delta: 'line 2\n',
      }),
    });
    const state = decode(view.parts[0]).state as Record<string, unknown>;
    expect(state.output).toBe('line 1\nline 2\n');
  });
});

describe('reduceSessionView — session.status / session.idle', () => {
  it('updates session.status from properties.status', () => {
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: 'busy' }),
    });
    expect(view.session?.status).toBe('busy');
  });

  it('accepts nested object status shape', () => {
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: { type: 'busy' } }),
    });
    expect(view.session?.status).toBe('busy');
  });

  // `retry` is a provider backoff *within* a turn, so the turn is still
  // running. Mirrors turnRunning() on the backend.
  it('maps `retry` to `busy`', () => {
    let view = makeView({ session: makeSession({ status: 'waiting' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: { type: 'retry', attempt: 2 } }),
    });
    expect(view.session?.status).toBe('busy');
  });

  it('accepts interrupted', () => {
    let view = makeView({ session: makeSession({ status: 'busy' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: 'interrupted' }),
    });
    expect(view.session?.status).toBe('interrupted');
  });

  it('maps `idle` to `done` and flags a refetch via the reducer result', () => {
    let view = makeView({ session: makeSession({ status: 'busy' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.idle', {}),
    });
    expect(view.session?.status).toBe('done');
    expect(view._refetchRequested).toBe(true);
  });

  // The backend settles lifecycle status from the agent's own turn
  // (db.SettleSessionStatus), so a *message snapshot* must not re-derive
  // and overwrite it. Note this is narrower than "message shape is never
  // a lifecycle signal": a running/pending tool part and a streaming
  // delta still flip the status to busy (reducePartSnapshot /
  // reducePartDelta), and that is what keeps the badge alive mid-turn.
  it('does not overwrite a busy status with a message snapshot', () => {
    let view = makeView({ session: makeSession({ status: 'done' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: { type: 'busy' } }),
    });
    expect(view.session?.status).toBe('busy');

    // A completed tool-only assistant envelope used to infer `done`.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.updated', {
        info: { id: 'm-shell', sessionID: SID, role: 'assistant' },
        parts: [{
          id: 'p-shell', messageID: 'm-shell', sessionID: SID,
          type: 'tool', tool: 'bash', state: { status: 'completed' },
        }],
      }),
    });
    expect(view.session?.status).toBe('busy');
    // The message itself is still applied.
    expect(view.messages.map((m) => m.id)).toContain('m-shell');
  });

  it('does not overwrite a settled status with an in-flight message snapshot', () => {
    let view = makeView({ session: makeSession({ status: 'done' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.updated', {
        info: { id: 'm-agent', sessionID: SID, role: 'assistant' },
        parts: [
          { id: 'p-start', messageID: 'm-agent', sessionID: SID, type: 'step-start' },
        ],
      }),
    });
    expect(view.session?.status).toBe('done');
  });

  // Turn-start ordering: the first event of a turn is a message.updated
  // carrying a step-start, and `session.status: busy` only arrives after
  // it. The snapshot must leave the settled status alone, and the
  // session.status that follows must still flip it.
  it('leaves a step-start snapshot alone and flips on the session.status that follows', () => {
    let view = makeView({ session: makeSession({ status: 'done' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.updated', {
        info: { id: 'm-agent', sessionID: SID, role: 'assistant' },
        parts: [
          { id: 'p-start', messageID: 'm-agent', sessionID: SID, type: 'step-start' },
          {
            id: 'p-tool', messageID: 'm-agent', sessionID: SID,
            type: 'tool', tool: 'bash', state: { status: 'completed' },
          },
        ],
      }),
    });
    expect(view.session?.status).toBe('done');

    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('session.status', { status: { type: 'busy' } }),
    });
    expect(view.session?.status).toBe('busy');
  });

  // A running tool part is a live signal, not a message snapshot: it is
  // what keeps the badge busy between session.status events. Deleting
  // this writer in a future "message shape is never a lifecycle signal"
  // cleanup would leave the badge stale mid-turn.
  it('flips to busy on a running tool part', () => {
    let view = makeView({ session: makeSession({ status: 'done' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p-tool', messageID: 'm-agent', sessionID: SID,
          type: 'tool', tool: 'bash', state: { status: 'running' },
        },
      }),
    });
    expect(view.session?.status).toBe('busy');
  });

  it('does not overwrite an error status reported by session.status', () => {
    let view = makeView({ session: makeSession({ status: 'error' }) });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.updated', {
        info: { id: 'm-ok', sessionID: SID, role: 'assistant', finish: 'stop' },
      }),
    });
    expect(view.session?.status).toBe('error');
  });
});

describe('reduceSessionView — permission prompts', () => {
  it('sets pendingPermission on permission.asked', () => {
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('permission.asked', {
        id: 'perm-1', permission: 'Allow shell?', patterns: ['ls'], sessionID: SID,
      }),
    });
    expect(view.pendingPermission?.permissionId).toBe('perm-1');
  });

  it('clears pendingPermission when permission.replied matches id', () => {
    let view = makeView({
      pendingPermission: { permissionId: 'perm-1', permission: 'X', patterns: [], sessionId: SID, askedAt: 0 },
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('permission.replied', { requestID: 'perm-1' }),
    });
    expect(view.pendingPermission).toBe(null);
  });

  it('does not clear pendingPermission when id mismatches', () => {
    const initial = { permissionId: 'perm-1', permission: 'X', patterns: [], sessionId: SID, askedAt: 0 };
    let view = makeView({ pendingPermission: initial });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('permission.replied', { requestID: 'perm-other' }),
    });
    expect(view.pendingPermission).toBe(initial);
  });
});

describe('reduceSessionView — question prompts', () => {
  it('sets pendingQuestion on question.asked', () => {
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('question.asked', {
        id: 'q-1', sessionID: SID,
        questions: [yesNoQuestion()],
      }),
    });
    expect(view.pendingQuestion?.requestId).toBe('q-1');
    expect(view.pendingQuestion?.questions).toHaveLength(1);
  });

  it('clears pendingQuestion on question.replied with matching id', () => {
    let view = makeView({
      pendingQuestion: {
        requestId: 'q-1', sessionID: SID,
        questions: [yesNoQuestion()],
      },
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('question.replied', { requestID: 'q-1' }),
    });
    expect(view.pendingQuestion).toBe(null);
  });

  it('clears pendingQuestion on question.rejected with matching id', () => {
    let view = makeView({
      pendingQuestion: {
        requestId: 'q-1', sessionID: SID,
        questions: [yesNoQuestion()],
      },
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('question.rejected', { requestID: 'q-1' }),
    });
    expect(view.pendingQuestion).toBe(null);
  });

  it('regression: question → reply → assistant follow-up renders without refresh', () => {
    // Requirement: when the agent posts a question prompt and the
    // user answers, the answered question and the assistant follow-up
    // both render via SSE alone.
    let view = makeView();
    // 1) Question arrives.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('question.asked', {
        id: 'q-1', sessionID: SID,
        questions: [yesNoQuestion()],
      }),
    });
    expect(view.pendingQuestion?.requestId).toBe('q-1');
    // 2) User replies — prompt clears.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('question.replied', { requestID: 'q-1' }),
    });
    expect(view.pendingQuestion).toBe(null);
    // 3) Assistant follow-up message arrives.
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'm-reply', sessionID: SID, role: 'assistant', time: { created: 100 } },
        parts: [
          { id: 'p-reply', messageID: 'm-reply', sessionID: SID, type: 'text', text: 'Continuing.' },
        ],
      }),
    });
    expect(view.messages.find((m) => m.id === 'm-reply')).toBeDefined();
    expect(decode(view.parts.find((p) => p.id === 'p-reply')!).text).toBe('Continuing.');
  });

  it('regression: clears pendingQuestion when its tool part resolves out-of-band (CLI answer)', () => {
    // The user answers in the OpenCode CLI. OpenCode streams a
    // `message.part.updated` with the question tool now completed and
    // an output set, but no `question.replied` event. The prompt must
    // still be dismissed.
    let view = makeView({
      pendingQuestion: {
        requestId: 'q-cli', sessionID: SID,
        questions: [yesNoQuestion()],
      },
    });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p-q', messageID: 'm-q', sessionID: SID, type: 'tool', tool: 'question',
          state: {
            status: 'completed',
            input: { requestId: 'q-cli', questions: [yesNoQuestion()] },
            output: 'yes',
          },
        },
      }),
    });
    expect(view.pendingQuestion).toBe(null);
  });

  it('keeps pendingQuestion while its tool part is still running', () => {
    const pending = {
      requestId: 'q-cli', sessionID: SID,
      questions: [yesNoQuestion()],
    };
    let view = makeView({ pendingQuestion: pending });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p-q', messageID: 'm-q', sessionID: SID, type: 'tool', tool: 'question',
          state: {
            status: 'running',
            input: { requestId: 'q-cli', questions: [yesNoQuestion()] },
            output: '',
          },
        },
      }),
    });
    expect(view.pendingQuestion).toBe(pending);
  });

  it('does not clear pendingQuestion when a different question resolves', () => {
    const pending = {
      requestId: 'q-current', sessionID: SID,
      questions: [yesNoQuestion()],
    };
    let view = makeView({ pendingQuestion: pending });
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.part.updated', {
        part: {
          id: 'p-q', messageID: 'm-q', sessionID: SID, type: 'tool', tool: 'question',
          state: {
            status: 'completed',
            input: { requestId: 'q-other', questions: [yesNoQuestion()] },
            output: 'yes',
          },
        },
      }),
    });
    expect(view.pendingQuestion).toBe(pending);
  });
});

describe('reduceSessionView — cross-session events', () => {
  it('ignores events whose sessionID belongs to a different session', () => {
    const before = makeView({ messages: [makeMessage('m1', 1)] });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'other-msg', sessionID: 'sess-other', role: 'assistant', time: { created: 5 } },
        parts: [],
      }),
    });
    expect(after).toBe(before);
  });

  it('treats missing sessionID as "belongs to current session"', () => {
    // Defensive: some legacy payloads omit sessionID and the only
    // reasonable interpretation is "this is for the active stream".
    let view = makeView();
    view = reduceSessionView(view, {
      type: 'sse',
      event: sseEvent('message.created', {
        info: { id: 'm1', role: 'assistant', time: { created: 1 } },
        parts: [],
      }),
    });
    expect(view.messages.find((m) => m.id === 'm1')).toBeDefined();
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

describe('regression: ocman.permission.auto-approved routing', () => {
  // Bug: the backend used to emit `sessionId` (camelCase) but the
  // reducer's eventSessionId() reads `sessionID` (caps, OpenCode style).
  // The mismatch made cross-session filtering a no-op, so an event
  // generated for session B would be applied to whichever session's
  // reducer was currently running — the user saw the "auto-approved by
  // AI" notice attached to whatever session they happened to be viewing.
  it('drops auto-approved events whose sessionID belongs to another session', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.auto-approved', {
        permissionId: 'perm-1',
        sessionID: 'other-session', // wire-format key, different session
        permission: 'read /etc/passwd',
        patterns: [],
        approvedAt: 1234,
      }),
    });
    // No mutation: the notice must not be injected into this session.
    expect(after).toBe(before);
  });

  it('applies auto-approved events that target this session', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.auto-approved', {
        permissionId: 'perm-1',
        sessionID: SID, // matches this reducer
        permission: 'read /etc/passwd',
        patterns: [],
        approvedAt: 1234,
      }),
    });
    // Notice injected, keyed by permissionId (judge session no longer exists).
    expect(after.messages.some((m) => m.id === 'ocman-notice-perm-1')).toBe(true);
  });

  it('drops checking events whose sessionID belongs to another session', () => {
    const before = makeView({ pendingPermission: { permissionId: 'perm-1', permission: 'x', patterns: [], sessionId: SID, askedAt: 0 } });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.checking', {
        permissionId: 'perm-1',
        sessionID: 'other-session',
      }),
    });
    expect(after.checkingPermissionId).toBeNull();
    expect(after).toBe(before);
  });

  it('drops pending events whose sessionID belongs to another session', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.pending', {
        permissionId: 'perm-1',
        sessionID: 'other-session',
        judgeStartsAt: Date.now() + 5000,
      }),
    });
    expect(after.judgeStartsAt).toBeNull();
    expect(after).toBe(before);
  });
});

describe('reduceSessionView — unknown events', () => {
  it('returns the same state reference for unknown event types', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('unknown.event.type', { foo: 'bar' }),
    });
    expect(after).toBe(before);
  });
});

describe('reduceSessionView — judge reasoning surfacing', () => {
  // The transient judge session is deleted by the backend as soon as
  // the verdict is parsed, so the SSE payloads no longer carry a
  // judgeSessionId and the reducer no longer exposes one. Only the
  // one-line reasoning survives — these tests pin that contract.
  it('captures reasoning from a flagged SSE event', () => {
    const perm = { permissionId: 'p1', permission: 'rm -rf', patterns: [], sessionId: SID, askedAt: 0 };
    const before = makeView({ pendingPermission: perm });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.flagged', {
        permissionId: 'p1',
        sessionID: SID,
        reasoning: 'Deletes the entire repository.',
      }),
    });
    expect(after.judgeReasoning).toBe('Deletes the entire repository.');
  });

  it('ignores flagged events that omit the reasoning', () => {
    // Without reasoning the event carries no actionable signal (the
    // judge session it used to point at is already gone), so the
    // reducer should leave state untouched rather than flicker a UI
    // marker with no content.
    const perm = { permissionId: 'p1', permission: 'rm -rf', patterns: [], sessionId: SID, askedAt: 0 };
    const before = makeView({ pendingPermission: perm });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.flagged', {
        permissionId: 'p1',
        sessionID: SID,
      }),
    });
    expect(after.judgeReasoning).toBeNull();
    expect(after).toBe(before);
  });

  it('clears reasoning when a new permission is asked', () => {
    const before = makeView({
      pendingPermission: { permissionId: 'p1', permission: 'x', patterns: [], sessionId: SID, askedAt: 0 },
      judgeReasoning: 'Previous reasoning.',
    });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('permission.asked', {
        id: 'p2',
        permission: 'edit',
        patterns: ['/tmp/foo'],
        sessionID: SID,
      }),
    });
    expect(after.judgeReasoning).toBeNull();
  });

  it('clears reasoning when the user replies to the permission', () => {
    const before = makeView({
      pendingPermission: { permissionId: 'p1', permission: 'x', patterns: [], sessionId: SID, askedAt: 0 },
      judgeReasoning: 'Some reasoning.',
    });
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('permission.replied', {
        requestID: 'p1',
        sessionID: SID,
        response: 'once',
      }),
    });
    expect(after.judgeReasoning).toBeNull();
  });

  it('embeds reasoning in the injected auto-approved notice part', () => {
    const before = makeView();
    const after = reduceSessionView(before, {
      type: 'sse',
      event: sseEvent('ocman.permission.auto-approved', {
        permissionId: 'perm-1',
        sessionID: SID,
        permission: 'read /etc/hosts',
        patterns: [],
        reasoning: 'Read-only system file.',
        approvedAt: 1234,
      }),
    });
    // Notice key uses the permissionId now that judge session IDs are
    // ephemeral (deleted post-verdict).
    const noticePart = after.parts.find((p) => p.id === 'ocman-notice-perm-1-part');
    expect(noticePart).toBeDefined();
    expect(decode(noticePart!).reasoning).toBe('Read-only system file.');
  });

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
