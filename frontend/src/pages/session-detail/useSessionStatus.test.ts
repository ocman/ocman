// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderHook } from '@testing-library/react';
import type { Message, SessionStatus } from '../../lib/api';
import { useSessionStatus } from './useSessionStatus';
import type { SubagentTokenMap } from './useSubagentTracking';

// Minimal user / assistant message builders. Only the fields the
// hook reads (`role`, `finish`, `error`, `time`) are populated.
function userMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: { role: 'user', time: { created } },
  };
}

function streamingAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      tokens: { input: 0, output: 0 },
      time: { created },
    },
  };
}

function finishedAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      finish: 'stop',
      time: { created, completed: created + 100 },
    },
  };
}

function erroredAssistantMessage(id: string, created: number): Message {
  return {
    id,
    sessionId: 's1',
    timeCreated: created,
    data: {
      role: 'assistant',
      error: { message: 'boom' } as unknown as never,
      time: { created, completed: created + 100 },
    },
  };
}

interface HookProps {
  lastMsg: Message | null;
  messages: Message[];
  subagentTokens: SubagentTokenMap;
  sessionStatus?: SessionStatus;
  awaitingAssistantResponse?: boolean;
  isRunning: boolean;
  pendingPermission: null;
  pendingQuestion: null;
}

function buildHook(initial: HookProps) {
  const setSubagentTokens = vi.fn();
  return renderHook(
    (props: HookProps) =>
      useSessionStatus({
        lastMsg: props.lastMsg,
        messages: props.messages,
        subagentTokens: props.subagentTokens,
        setSubagentTokens: (next) => setSubagentTokens(next),
        sessionStatus: props.sessionStatus,
        awaitingAssistantResponse: props.awaitingAssistantResponse,
        isRunning: props.isRunning,
        pendingPermission: props.pendingPermission,
        pendingQuestion: props.pendingQuestion,
      }),
    { initialProps: initial },
  );
}

describe('useSessionStatus', () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2024-01-01T00:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  // The backend settles lifecycle status (db.SettleSessionStatus); the
  // hook reports it verbatim. It must NOT re-derive a status from the
  // message list — that is what used to overwrite the authoritative value.
  it.each<[SessionStatus]>([
    ['busy'],
    ['waiting'],
    ['done'],
    ['error'],
    ['interrupted'],
  ])('reports the backend status %s verbatim', (sessionStatus) => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe(sessionStatus);
  });

  it('does not derive waiting from a finished assistant message', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'busy',
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('busy');
  });

  it('does not derive error from an errored assistant message', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      erroredAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'busy',
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('busy');
  });

  // OpenCode's session.status vocabulary is busy|retry|idle only
  // (live_status.go), so a failed turn reaches the page as an errored
  // message plus `session.idle` — which reduces to `done`. Message shape
  // is what decides *which* terminal state (db.InferSessionStatus), so
  // the errored tail is the only in-band error signal until the REST
  // reconcile lands.
  it.each<[string, SessionStatus | undefined]>([
    ['a done status', 'done'],
    ['no reported status', undefined],
  ])('reports error for an errored tail with %s', (_label, sessionStatus) => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      erroredAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('error');
  });

  it('reports error for a finish=error tail', () => {
    const t0 = Date.now();
    const errored: Message = {
      id: 'a1',
      sessionId: 's1',
      timeCreated: t0 - 1_000,
      data: { role: 'assistant', finish: 'error', time: { created: t0 - 1_000 } },
    };
    const messages: Message[] = [userMessage('u1', t0 - 5_000), errored];

    const { result } = buildHook({
      lastMsg: errored,
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'done',
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('error');
  });

  it('does not derive busy from a streaming assistant message', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      streamingAssistantMessage('a1', t0 - 200),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'interrupted',
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    // #488: an interrupted turn's last message looks like live streaming
    // to every local heuristic. The backend knows the process is gone.
    expect(result.current.displayStatus).toBe('interrupted');
  });

  it('falls back to done when no status has been reported yet', () => {
    const { result } = buildHook({
      lastMsg: null,
      messages: [],
      subagentTokens: new Map(),
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('done');
  });

  it('shows busy while awaiting the first assistant response after a user send', () => {
    const t0 = Date.now();
    const messages: Message[] = [userMessage('u1', t0 - 100)];

    const { result } = buildHook({
      lastMsg: messages[0],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'done',
      awaitingAssistantResponse: true,
      isRunning: true,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('busy');
  });

  it('lets a fresh prompt outrank a stale interrupted status', () => {
    const t0 = Date.now();
    const messages: Message[] = [userMessage('u1', t0 - 100)];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'interrupted',
      awaitingAssistantResponse: true,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('busy');
  });

  // The send affordance only applies while the user's own message is
  // still the tail. Once an assistant message exists for the turn, the
  // backend status takes over unconditionally.
  it('drops the send affordance once an assistant message lands', () => {
    const t0 = Date.now();
    const messages: Message[] = [
      userMessage('u1', t0 - 5_000),
      finishedAssistantMessage('a1', t0 - 1_000),
    ];

    const { result } = buildHook({
      lastMsg: messages[messages.length - 1],
      messages,
      subagentTokens: new Map(),
      sessionStatus: 'waiting',
      awaitingAssistantResponse: true,
      isRunning: false,
      pendingPermission: null,
      pendingQuestion: null,
    });

    expect(result.current.displayStatus).toBe('waiting');
  });
});
