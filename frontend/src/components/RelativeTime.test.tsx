// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen } from '@testing-library/react';
import { RelativeTime } from './RelativeTime';

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
