import { describe, it, expect } from 'vitest';
import type { Message, Session } from './api';
import {
  formatModelRef,
  agentModelRef,
  deriveRawStatus,
  isSessionRunning,
  computeLiveTokens,
  mergeTokenStats,
  deriveActiveModelAndAgent,
  type SessionWithDefaults,
} from './sessionStatus';

function makeMessage(id: string, data: Partial<Message['data']>): Message {
  return {
    id,
    sessionId: 's',
    timeCreated: 0,
    data: { role: 'assistant', ...data },
  };
}

function makeSession(overrides: Partial<SessionWithDefaults> = {}): SessionWithDefaults {
  const base: Session = {
    id: 's',
    platform: 'opencode',
    projectId: 'p',
    title: 't',
    directory: '/tmp',
    timeCreated: 0,
    timeUpdated: 0,
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
    liveConnection: false,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: true,
    pinned: false,
    pinnedAt: 0,
    seenTimeUpdated: 0,
    unreadCount: 0,
  };
  return { ...base, ...overrides };
}

describe('formatModelRef', () => {
  it('returns provider/model when both are present', () => {
    expect(formatModelRef('anthropic', 'claude-opus-4')).toBe('anthropic/claude-opus-4');
  });

  it('returns the model alone when no provider is given', () => {
    expect(formatModelRef(undefined, 'gpt-4')).toBe('gpt-4');
  });

  it('returns empty string when no model is set', () => {
    expect(formatModelRef('anthropic', undefined)).toBe('');
    expect(formatModelRef(undefined, undefined)).toBe('');
  });
});

describe('agentModelRef', () => {
  it('returns empty string when the agent declares no model', () => {
    expect(agentModelRef({ name: 'build' })).toBe('');
    expect(agentModelRef(undefined)).toBe('');
  });

  it('returns a bare provider/model string verbatim (trimmed)', () => {
    expect(agentModelRef({ name: 'plan', model: '  anthropic/opus-4 ' })).toBe('anthropic/opus-4');
  });

  it('formats an object model into a provider/model string', () => {
    expect(
      agentModelRef({ name: 'plan', model: { providerID: 'openai', modelID: 'gpt-5' } }),
    ).toBe('openai/gpt-5');
  });
});

describe('deriveRawStatus', () => {
  it('returns done when there is no last message', () => {
    expect(deriveRawStatus(null)).toBe('done');
  });

  it('returns done for a user message', () => {
    expect(deriveRawStatus(makeMessage('m', { role: 'user' }))).toBe('done');
  });

  it('returns busy for an in-flight assistant message', () => {
    expect(deriveRawStatus(makeMessage('m', { role: 'assistant' }))).toBe('busy');
  });

  it('returns waiting for a finished assistant message', () => {
    expect(deriveRawStatus(makeMessage('m', { role: 'assistant', finish: 'stop' }))).toBe('waiting');
  });

  it('returns error when finish === "error"', () => {
    expect(deriveRawStatus(makeMessage('m', { role: 'assistant', finish: 'error' }))).toBe('error');
  });

  it('returns error when data.error is set even without finish', () => {
    expect(
      deriveRawStatus(makeMessage('m', { role: 'assistant', error: { name: 'boom' } })),
    ).toBe('error');
  });
});

describe('isSessionRunning', () => {
  it('returns false when there is no last message', () => {
    expect(isSessionRunning(null)).toBe(false);
  });

  it('returns false when the last message is from the user', () => {
    expect(isSessionRunning(makeMessage('m', { role: 'user' }))).toBe(false);
  });

  it('returns true when waiting for the first assistant response after a user send', () => {
    expect(isSessionRunning(makeMessage('m', { role: 'user' }), 'done', true)).toBe(true);
  });

  it('returns true when the server reports busy even if the last assistant message finished', () => {
    expect(isSessionRunning(makeMessage('m', { role: 'assistant', finish: 'stop' }), 'busy')).toBe(true);
  });

  it('returns true for a streaming assistant message (no finish, no error)', () => {
    expect(isSessionRunning(makeMessage('m', { role: 'assistant' }))).toBe(true);
  });

  it('returns false for an assistant message with finish set', () => {
    expect(isSessionRunning(makeMessage('m', { role: 'assistant', finish: 'stop' }))).toBe(false);
  });

  it('returns false for an assistant message with an error', () => {
    expect(
      isSessionRunning(makeMessage('m', { role: 'assistant', error: { name: 'x' } })),
    ).toBe(false);
  });
});

describe('computeLiveTokens', () => {
  it('returns zeroes for an empty array', () => {
    expect(computeLiveTokens([])).toEqual({
      tokensIn: 0,
      tokensOut: 0,
      tokensReasoning: 0,
      cacheRead: 0,
      cacheWrite: 0,
      totalCost: 0,
    });
  });

  it('skips user messages', () => {
    const out = computeLiveTokens([
      makeMessage('m1', { role: 'user' }),
      makeMessage('m2', {
        role: 'assistant',
        tokens: { input: 10, output: 20 },
      }),
    ]);
    expect(out.tokensIn).toBe(10);
    expect(out.tokensOut).toBe(20);
  });

  it('sums cache and reasoning tokens across assistant messages', () => {
    const out = computeLiveTokens([
      makeMessage('m1', {
        role: 'assistant',
        tokens: { input: 5, output: 7, reasoning: 3, cache: { read: 100, write: 50 } },
      }),
      makeMessage('m2', {
        role: 'assistant',
        tokens: { input: 1, output: 2, reasoning: 0, cache: { read: 10, write: 0 } },
      }),
    ]);
    expect(out).toEqual({
      tokensIn: 6,
      tokensOut: 9,
      tokensReasoning: 3,
      cacheRead: 110,
      cacheWrite: 50,
      totalCost: 0,
    });
  });

  it('accumulates cost only from messages that report it', () => {
    const out = computeLiveTokens([
      makeMessage('m1', { role: 'assistant', cost: 0.1 }),
      makeMessage('m2', { role: 'assistant' }),
      makeMessage('m3', { role: 'assistant', cost: 0.05 }),
    ]);
    expect(out.totalCost).toBeCloseTo(0.15, 6);
  });

  it('handles assistant messages without a tokens block', () => {
    const out = computeLiveTokens([makeMessage('m', { role: 'assistant' })]);
    expect(out.tokensIn).toBe(0);
    expect(out.tokensOut).toBe(0);
  });
});

describe('mergeTokenStats', () => {
  const live = {
    tokensIn: 5,
    tokensOut: 10,
    tokensReasoning: 1,
    cacheRead: 100,
    cacheWrite: 50,
    totalCost: 0.25,
  };

  it('uses the larger of server total and live tokens', () => {
    const session = makeSession({ totalInputTokens: 50, totalOutputTokens: 1 });
    const stats = mergeTokenStats(session, live);
    expect(stats.input).toBe(50); // server wins
    expect(stats.output).toBe(10); // live wins
  });

  it('passes through reasoning, cache and cost from live stats', () => {
    const stats = mergeTokenStats(makeSession(), live);
    expect(stats.reasoning).toBe(1);
    expect(stats.cacheRead).toBe(100);
    expect(stats.cacheWrite).toBe(50);
    expect(stats.totalCost).toBe(0.25);
  });

  it('reflects contextTokenCount on the result', () => {
    const session = makeSession();
    session.contextTokenCount = 12345;
    expect(mergeTokenStats(session, live).contextWindow).toBe(12345);
  });

  it('defaults server totals to zero when session is null', () => {
    const stats = mergeTokenStats(null, live);
    expect(stats.input).toBe(5);
    expect(stats.output).toBe(10);
    expect(stats.contextWindow).toBeUndefined();
  });
});

describe('deriveActiveModelAndAgent', () => {
  it('falls back to session defaults when no messages have model/agent', () => {
    const session = makeSession();
    session.defaultAgent = 'build';
    session.defaultModel = 'sonnet';
    const out = deriveActiveModelAndAgent([makeMessage('m', { role: 'user' })], session);
    expect(out).toEqual({ activeModel: 'sonnet', activeAgent: 'build' });
  });

  it('returns empty strings when nothing is set', () => {
    expect(deriveActiveModelAndAgent([], null)).toEqual({ activeModel: '', activeAgent: '' });
  });

  it('picks the most recent message that carries a model reference', () => {
    const messages = [
      makeMessage('a', { role: 'assistant', providerID: 'anthropic', modelID: 'sonnet-3' }),
      makeMessage('b', { role: 'user' }),
      makeMessage('c', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4' }),
      makeMessage('d', { role: 'user' }),
    ];
    expect(deriveActiveModelAndAgent(messages, null).activeModel).toBe('anthropic/opus-4');
  });

  it('picks the most recent agent independently of the model match', () => {
    const messages = [
      makeMessage('a', { role: 'assistant', agent: 'plan', providerID: 'p', modelID: 'm' }),
      makeMessage('b', { role: 'assistant', agent: 'build' }),
      makeMessage('c', { role: 'user' }),
    ];
    const out = deriveActiveModelAndAgent(messages, null);
    expect(out.activeAgent).toBe('build');
    // model from the same scan
    expect(out.activeModel).toBe('p/m');
  });

  it('ignores model from skill/tool messages after the last user turn', () => {
    // A skill ran with 'skill-model' after the user message, but the
    // primary response used 'primary-model'. Only 'primary-model' should
    // be reflected as activeModel.
    const messages = [
      makeMessage('a', { role: 'assistant', providerID: 'anthropic', modelID: 'primary-model' }),
      makeMessage('b', { role: 'user' }),
      makeMessage('c', { role: 'assistant', providerID: 'anthropic', modelID: 'skill-model' }),
      makeMessage('d', { role: 'assistant', providerID: 'anthropic', modelID: 'skill-model' }),
    ];
    expect(deriveActiveModelAndAgent(messages, null).activeModel).toBe('anthropic/primary-model');
  });

  it('falls back to session default when there is no user message in history', () => {
    const session = makeSession();
    session.defaultModel = 'default-model';
    const messages = [
      makeMessage('a', { role: 'assistant', providerID: 'anthropic', modelID: 'some-model' }),
    ];
    expect(deriveActiveModelAndAgent(messages, session).activeModel).toBe('default-model');
  });
});
