// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import type { Message, Part } from '../../lib/api';
import { useSessionStatus } from './useSessionStatus';
import { computeTurnStats } from '../../lib/turnStats';

afterEach(() => vi.useRealTimers());

it('excludes tool time from the conversation turn summary', () => {
  const messages: Message[] = [
    { id: 'u', sessionId: 's', timeCreated: 500, data: { role: 'user' } },
    { id: 'm', sessionId: 's', timeCreated: 1000, data: {
      role: 'assistant', time: { created: 1000, completed: 11000 }, tokens: { input: 0, output: 200 },
    } },
  ];
  const parts: Part[] = [{ id: 'p', sessionId: 's', messageId: 'm', data: {
    type: 'tool', state: { status: 'completed', time: { start: 3000, end: 11000 } },
  } }];
  expect(computeTurnStats(messages, parts).get('m')?.tps).toBe(100);
});

it('excludes overlapping tool waits and stays stable while another message is running', () => {
  vi.useFakeTimers();
  vi.setSystemTime(11000);
  const messages: Message[] = [
    { id: 'u', sessionId: 's', timeCreated: 500, data: { role: 'user' } },
    { id: 'm', sessionId: 's', timeCreated: 1000, data: {
      role: 'assistant', time: { created: 1000, completed: 11000 }, tokens: { input: 0, output: 200 },
    } },
    { id: 'live', sessionId: 's', timeCreated: 11000, data: { role: 'assistant', time: { created: 11000 } } },
  ];
  const parts = [3000, 5000].map((start, i) => ({
    id: `p${i}`, sessionId: 's', messageId: 'm', data: {
      type: 'tool', state: { status: 'completed', time: { start, end: 11000 } },
    },
  })) as Part[];
  const options = {
    messages, parts, lastMsg: messages[2], subagentTokens: new Map(),
    setSubagentTokens: vi.fn(), isRunning: true, pendingPermission: null, pendingQuestion: null,
  };
  const { result } = renderHook(() => useSessionStatus(options));
  expect(result.current.liveTokensPerSecond).toBe(100);
  act(() => vi.advanceTimersByTime(60000));
  expect(result.current.liveTokensPerSecond).toBe(100);
});
