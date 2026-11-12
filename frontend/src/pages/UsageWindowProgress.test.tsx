// @vitest-environment jsdom
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { UsageWindowProgress } from './UsageWindowProgress';

describe('UsageWindowProgress', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-12T12:00:00Z'));
  });
  afterEach(() => vi.useRealTimers());

  it.each([
    ['5 hours', '2026-11-12T14:30:00Z', 50],
    ['7 days · Sonnet', '2026-11-16T00:00:00Z', 50],
    ['Codex Spark · 5 hours', '2026-11-12T14:30:00Z', 50],
    ['60 minutes', '2026-11-12T12:45:00Z', 25],
    ['1 hour', '2026-11-12T13:00:00Z', 0],
    ['1 day', '2026-11-14T12:00:00Z', 0],
  ])('positions the elapsed-time dot for %s', (name, resetsAt, percent) => {
    render(<UsageWindowProgress label="Provider usage" window={{ name, resetsAt, usedPercent: 40 }} />);
    expect(screen.getByRole('progressbar', { name: 'Provider usage' })).toHaveValue(40);
    expect(screen.getByRole('img')).toHaveStyle({ left: `${percent}%` });
    expect(screen.getByRole('img')).toHaveAccessibleName(new RegExp(`${percent}% of period elapsed`));
  });

  it.each([
    ['5 hours', undefined], ['Rolling', '2026-11-12T14:30:00Z'],
    ['Monthly', '2026-12-01T00:00:00Z'], ['5 hours', 'invalid'],
    ['5 hours', '2026-11-12T12:00:00Z'], ['5 hours', '2026-11-11T12:00:00Z'],
    ['0 hours', '2026-11-12T14:30:00Z'],
  ])('omits the dot for an unknown or expired period: %s, %s', (name, resetsAt) => {
    render(<UsageWindowProgress label="Usage" window={{ name, resetsAt, usedPercent: 10 }} />);
    expect(screen.getByRole('progressbar')).toHaveValue(10);
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
  });

  it.each([[20, 'below'], [50, 'in line with'], [80, 'above']])('describes %s percent usage relative to elapsed time', (usedPercent, comparison) => {
    render(<UsageWindowProgress label="Usage" window={{ name: '5 hours', resetsAt: '2026-11-12T14:30:00Z', usedPercent }} />);
    expect(screen.getByRole('img')).toHaveAccessibleName(`50% of period elapsed. Usage is ${comparison} elapsed time.`);
  });

  it('updates the dot while open and cleans up its timer', () => {
    const view = render(<UsageWindowProgress label="Usage" window={{ name: '5 hours', resetsAt: '2026-11-12T14:30:00Z', usedPercent: 40 }} />);
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByRole('img')).toHaveStyle({ left: `${100 * (1 - 149 / 300)}%` });
    act(() => vi.advanceTimersByTime(149 * 60_000));
    expect(screen.queryByRole('img')).not.toBeInTheDocument();
    view.unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});
