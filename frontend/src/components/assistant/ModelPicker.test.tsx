// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionModelEntry } from '../../lib/api';
import { ModelPicker } from './ModelPicker';

describe('ModelPicker cooldown', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const entries: SessionModelEntry[] = [
    { provider: 'a', model: 'x', isAvailable: true, cooldownUntil: '2026-01-01T00:01:30Z' },
    { provider: 'b', model: 'y', isAvailable: true },
  ];

  function renderPicker() {
    const onSelect = vi.fn();
    render(<ModelPicker open models={[]} modelEntries={entries} onSelect={onSelect} onClose={vi.fn()} />);
    return { onSelect };
  }

  const row = (name: string) => screen.getAllByRole('option').find((o) => o.textContent?.includes(name))!;

  it('marks only the cooled-down model and keeps it selectable', () => {
    const { onSelect } = renderPicker();
    expect(row('x').textContent).toContain('unavailable · 1m 30s');
    expect(row('y').textContent).not.toContain('unavailable');
    expect(row('y').querySelector('.oc-model-picker-badge--cooldown')).toBeNull();
    fireEvent.click(row('x'));
    expect(onSelect).toHaveBeenCalledWith('a/x');
  });

  it('counts down and clears the mark once the cooldown expires', () => {
    renderPicker();
    act(() => { vi.advanceTimersByTime(30_000); });
    expect(row('x').textContent).toContain('unavailable · 1m 0s');
    act(() => { vi.advanceTimersByTime(61_000); });
    expect(row('x').textContent).not.toContain('unavailable');
  });
});
