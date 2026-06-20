import { describe, it, expect } from 'vitest';
import type { Message } from './api';
import { computeTurnStats, humanizeModelRef, latestTurnModel, messageModelRef } from './turnStats';

function makeMessage(
  id: string,
  data: Partial<Message['data']> & { role: 'user' | 'assistant' },
  timeCreated = 0,
): Message {
  return { id, sessionId: 's', timeCreated, data: { ...data } };
}

describe('messageModelRef', () => {
  it('formats top-level provider/model', () => {
    expect(messageModelRef(makeMessage('a', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4' }))).toBe(
      'anthropic/opus-4',
    );
  });

  it('falls back to the nested model object', () => {
    expect(
      messageModelRef(
        makeMessage('a', {
          role: 'assistant',
          model: { providerID: 'google', modelID: 'gemini-pro' },
        } as Partial<Message['data']> & { role: 'assistant' }),
      ),
    ).toBe('google/gemini-pro');
  });

  it('returns empty string when no model is present', () => {
    expect(messageModelRef(makeMessage('a', { role: 'assistant' }))).toBe('');
  });
});

describe('humanizeModelRef', () => {
  it('humanizes Anthropic Claude raw ids', () => {
    expect(humanizeModelRef('anthropic/claude-opus-4-8')).toBe('Claude Opus 4.8');
  });

  it('humanizes multi-part numeric versions', () => {
    expect(humanizeModelRef('anthropic/claude-3-5-sonnet')).toBe('Claude 3.5 Sonnet');
  });

  it('keeps common model acronyms uppercase', () => {
    expect(humanizeModelRef('openai/gpt-5')).toBe('GPT 5');
  });
});

describe('computeTurnStats — model', () => {
  it('records the model for the turn keyed by the last assistant message', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4', finish: 'stop' }, 2),
    ];
    const map = computeTurnStats(messages, []);
    expect(map.get('a')?.model).toBe('anthropic/opus-4');
  });

  it('uses the most recent model when it changes within a turn', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4', finish: 'tool-calls' }, 2),
      makeMessage('a2', { role: 'assistant', providerID: 'openai', modelID: 'gpt-5', finish: 'stop' }, 3),
    ];
    const map = computeTurnStats(messages, []);
    expect(map.get('a2')?.model).toBe('openai/gpt-5');
  });

  it('leaves model empty when no assistant message reports one', () => {
    const messages = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant', finish: 'stop' }, 2),
    ];
    const map = computeTurnStats(messages, []);
    expect(map.get('a')?.model).toBe('');
  });
});

describe('latestTurnModel', () => {
  it('uses the completed latest turn, not the previous turn', () => {
    const messages = [
      makeMessage('u1', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4', finish: 'stop' }, 2),
      makeMessage('u2', { role: 'user' }, 3),
      makeMessage('a2', { role: 'assistant', providerID: 'openai', modelID: 'gpt-5', finish: 'stop' }, 4),
    ];
    const map = computeTurnStats(messages, []);

    expect(latestTurnModel(messages, map)).toBe('openai/gpt-5');
  });

  it('falls back to the previous completed turn while a new user turn is pending', () => {
    const messages = [
      makeMessage('u1', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant', providerID: 'anthropic', modelID: 'opus-4', finish: 'stop' }, 2),
      makeMessage('u2', { role: 'user' }, 3),
    ];
    const map = computeTurnStats(messages, []);

    expect(latestTurnModel(messages, map)).toBe('anthropic/opus-4');
  });
});
