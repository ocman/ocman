// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { RelativeTime, RelativeTimestamp } from './RelativeTime';

describe('RelativeTime', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T12:00:00Z'));
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('counts up every second while under a minute old', () => {
    render(<RelativeTime iso="2026-01-01T11:59:55Z" />);
    expect(screen.getByText('5s ago')).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(3_000); });
    expect(screen.getByText('8s ago')).toBeInTheDocument();
  });

  it('keeps counting after crossing into minutes', () => {
    render(<RelativeTime iso="2026-01-01T11:59:55Z" />);

    act(() => { vi.advanceTimersByTime(65_000); });
    expect(screen.getByText('1m ago')).toBeInTheDocument();

    // Now on the once-a-minute tick.
    act(() => { vi.advanceTimersByTime(60_000); });
    expect(screen.getByText('2m ago')).toBeInTheDocument();
  });

  it('renders nothing and starts no timer for an invalid timestamp', () => {
    const { container } = render(<RelativeTime iso="not-a-date" />);
    expect(container.textContent).toBe('');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('clears its timer on unmount', () => {
    const { unmount } = render(<RelativeTime iso="2026-01-01T11:59:55Z" />);
    expect(vi.getTimerCount()).toBe(1);
    unmount();
    expect(vi.getTimerCount()).toBe(0);
  });
});

describe('RelativeTimestamp', () => {
  it('keeps the accessible text in step with the visible time and adds the exact date', () => {
    vi.useFakeTimers({ now: new Date(2026, 9, 28, 12) });
    try {
      render(<RelativeTimestamp value={new Date(2026, 9, 28, 10)} />);
      const time = screen.getByText('2h ago').closest('time')!;
      expect(time).toHaveTextContent('2h ago, Oct 28, 10:00');
      expect(time).not.toHaveAttribute('aria-label');
      act(() => { vi.advanceTimersByTime(60 * 60_000); });
      expect(time).toHaveTextContent('3h ago, Oct 28, 10:00');
    } finally { vi.useRealTimers(); }
  });

  it('renders nothing for an invalid value', () => {
    const { container } = render(<RelativeTimestamp value="not a date" />);
    expect(container).toBeEmptyDOMElement();
  });
});
