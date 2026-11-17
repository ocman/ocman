import { expect, it } from 'vitest';
import type { Project, Session } from './api';
import { formatCost, projectActivityDetails, sessionActivityDetails, sessionCost } from './listActivity';

it('formats costs for compact lists', () => {
  expect(formatCost(0)).toBe('$0');
  expect(formatCost(Number.NaN)).toBe('$0');
  expect(formatCost(0.004)).toBe('<$0.01');
  expect(formatCost(12.468)).toBe('$12.47');
  expect(formatCost(1500)).toBe('$1.5K');
});

it('prefers effective cost and spells out definitions', () => {
  const s = { messageCount: 1, durationMs: 1_245_000, activeDurationMs: 60_000, totalCost: 1, totalEffectiveCost: 2,
    totalInputTokens: 140, totalOutputTokens: 11_000 } as Session;
  expect(sessionCost(s)).toBe(2);
  expect(sessionCost({ totalCost: 1 } as Session)).toBe(1);
  expect(sessionActivityDetails(s)).toBe('1 prompt\n20m 45s elapsed, 1m 0s agent working\n$2.00 cost\n140 input / 11.0K output tokens (excl. cache reads)');
  expect(sessionActivityDetails({ ...s, activeDurationMs: 0 })).toContain('20m 45s elapsed\n');
  expect(projectActivityDetails({ messageCount: 331, totalTokensIn: 650, totalTokensOut: 234_600 } as Project))
    .toBe('331 messages (user, agent and helper sessions)\n$0 cost\n650 input / 234.6K output tokens (excl. cache reads)');
});
