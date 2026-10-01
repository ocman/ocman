// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { Tooltip, TOOLTIP_DELAY_MS } from './Tooltip';
import { formatFullDateTime } from '../lib/format';

afterEach(() => { vi.useRealTimers(); });

it('shows content only after the shared hover delay', () => {
  vi.useFakeTimers();
  render(<Tooltip content="full time"><span>16:32</span></Tooltip>);
  fireEvent.pointerMove(screen.getByText('16:32'), { pointerType: 'mouse' });
  act(() => { vi.advanceTimersByTime(TOOLTIP_DELAY_MS - 1); });
  expect(screen.queryByRole('tooltip')).toBeNull();
  act(() => { vi.advanceTimersByTime(1); });
  expect(screen.getByRole('tooltip').textContent).toBe('full time');
});

it('formats an unambiguous 24h timestamp with the date', () => {
  const s = formatFullDateTime(new Date(2026, 9, 1, 16, 32, 5).getTime());
  expect(s).toContain('2026');
  expect(s).toContain('16:32:05');
});
