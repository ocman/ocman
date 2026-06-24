import { describe, expect, it } from 'vitest';
import type { Loop } from './api.types';
import {
  formatDurationMs,
  formatGoDuration,
  loopBudgetLabel,
  loopTriggerLabel,
  nextRunLabel,
  parseGoDuration,
} from './loopFormat';

function loop(overrides: Partial<Loop> = {}): Loop {
  return {
    id: 'l1',
    platform: 'opencode',
    rootSessionID: 's1',
    directory: '',
    projectName: '',
    title: '',
    currentTask: '',
    pattern: '',
    triggerType: 'schedule',
    actionType: 'prompt_root',
    actionTemplate: '',
    sessionMode: 'fresh',
    state: 'active',
    iteration: 0,
    errorStreak: 0,
    tokensUsed: 0,
    costUSD: 0,
    lastFiredAt: 0,
    createdAt: 0,
    updatedAt: 0,
    completedAt: 0,
    lastSummary: '',
    triggerConfig: { interval_seconds: 1800 },
    stopConditions: { max_iterations: 10, max_cost_usd: 1 },
    ...overrides,
  };
}

describe('formatDurationMs', () => {
  it('formats seconds, minutes, hours', () => {
    expect(formatDurationMs(5000)).toBe('5s');
    expect(formatDurationMs(90000)).toBe('1m 30s');
    expect(formatDurationMs(120000)).toBe('2m');
    expect(formatDurationMs(3 * 3600_000 + 5 * 60_000)).toBe('3h 5m');
  });
});

describe('parseGoDuration', () => {
  it('parses single units', () => {
    expect(parseGoDuration('30s')).toBe(30);
    expect(parseGoDuration('5m')).toBe(300);
    expect(parseGoDuration('1h')).toBe(3600);
    expect(parseGoDuration('2d')).toBe(172800);
  });

  it('parses combined units and fractions', () => {
    expect(parseGoDuration('1h30m')).toBe(5400);
    expect(parseGoDuration('1h 30m 15s')).toBe(5415);
    expect(parseGoDuration('1.5h')).toBe(5400);
  });

  it('treats a bare number as seconds', () => {
    expect(parseGoDuration('90')).toBe(90);
  });

  it('returns null on empty or invalid input', () => {
    expect(parseGoDuration('')).toBeNull();
    expect(parseGoDuration('soon')).toBeNull();
    expect(parseGoDuration('1h x 2m')).toBeNull();
    expect(parseGoDuration('5x')).toBeNull();
  });
});

describe('formatGoDuration', () => {
  it('is the inverse of parse for whole values', () => {
    expect(formatGoDuration(30)).toBe('30s');
    expect(formatGoDuration(300)).toBe('5m');
    expect(formatGoDuration(3600)).toBe('1h');
    expect(formatGoDuration(5400)).toBe('1h30m');
    expect(formatGoDuration(5415)).toBe('1h30m15s');
    expect(formatGoDuration(0)).toBe('0s');
  });
});

describe('nextRunLabel', () => {
  it('reads "due now" for a never-fired active schedule loop', () => {
    expect(nextRunLabel(loop({ lastFiredAt: 0 }))).toBe('due now');
  });

  it('computes time until the next interval fire', () => {
    // Fired 5 min ago, interval 30 min -> ~25 min remaining.
    const fired = Date.now() - 5 * 60_000;
    const label = nextRunLabel(loop({ lastFiredAt: fired, triggerConfig: { interval_seconds: 1800 } }));
    expect(label).toMatch(/^in 2[45]m/);
  });

  it('reads "due now" when overdue', () => {
    const fired = Date.now() - 2 * 3600_000; // 2h ago, 30m interval
    expect(nextRunLabel(loop({ lastFiredAt: fired }))).toBe('due now');
  });

  it('honors the 60s interval floor', () => {
    // interval 1s is floored to 60s; fired just now -> ~1m remaining.
    const label = nextRunLabel(loop({ lastFiredAt: Date.now(), triggerConfig: { interval_seconds: 1 } }));
    expect(label).toMatch(/^in (59s|1m)/);
  });

  it('returns empty for non-schedule loops', () => {
    expect(nextRunLabel(loop({ triggerType: 'pr_event' }))).toBe('');
  });

  it('returns empty for paused/terminal loops', () => {
    expect(nextRunLabel(loop({ state: 'paused' }))).toBe('');
    expect(nextRunLabel(loop({ state: 'completed' }))).toBe('');
  });
});

describe('loopTriggerLabel', () => {
  it('formats a schedule interval as a Go duration, not raw seconds', () => {
    expect(loopTriggerLabel(loop({ triggerType: 'schedule', triggerConfig: { interval_seconds: 10000 } })))
      .toBe('every 2h46m40s');
    expect(loopTriggerLabel(loop({ triggerType: 'schedule', triggerConfig: { interval_seconds: 1800 } })))
      .toBe('every 30m');
  });

  it('labels a pr_event trigger by PR number', () => {
    expect(loopTriggerLabel(loop({ triggerType: 'pr_event', triggerConfig: { pr_number: 42 } }))).toBe('PR #42');
  });

  it('labels event triggers', () => {
    expect(loopTriggerLabel(loop({ triggerType: 'child_complete' }))).toBe('on child complete');
    expect(loopTriggerLabel(loop({ triggerType: 'turn_complete' }))).toBe('on turn complete');
  });
});

describe('loopBudgetLabel', () => {
  it('summarizes iterations and cost', () => {
    expect(loopBudgetLabel(loop({ iteration: 2, costUSD: 0.5, stopConditions: { max_iterations: 48, max_cost_usd: 5 } })))
      .toBe('2/48 · $0.50/$5.00');
  });

  it('appends an error streak when present', () => {
    expect(loopBudgetLabel(loop({ iteration: 1, errorStreak: 2, stopConditions: { max_iterations: 10, max_cost_usd: 1 } })))
      .toContain('2 err');
  });
});
