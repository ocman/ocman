// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { RateLimitBanner } from './RateLimitBanner';
import type { SessionNotice } from '../lib/api';

function makeNotice(overrides: Partial<SessionNotice> = {}): SessionNotice {
  return {
    kind: 'rate_limit',
    message: 'rate limit exceeded',
    retryAt: 0,
    attempt: 0,
    ...overrides,
  };
}

afterEach(() => {
  vi.useRealTimers();
});

describe('RateLimitBanner', () => {
  it('renders generic error notices', () => {
    render(
      <RateLimitBanner notice={{ kind: 'error', message: 'x', retryAt: 0, attempt: 0 }} />,
    );
    expect(screen.getByText(/Error/)).toBeInTheDocument();
    expect(screen.getByText(/x/)).toBeInTheDocument();
  });

  it('renders provider overload notices', () => {
    render(
      <RateLimitBanner
        notice={{ kind: 'provider_overloaded', message: 'provider is overloaded', retryAt: 0, attempt: 0 }}
      />,
    );
    expect(screen.getByText(/Provider overloaded/)).toBeInTheDocument();
    expect(screen.getByText(/provider is overloaded/)).toBeInTheDocument();
  });

  it('renders the message and attempt', () => {
    render(<RateLimitBanner notice={makeNotice({ message: 'too fast', attempt: 2 })} />);
    expect(screen.getByText(/too fast/)).toBeInTheDocument();
    expect(screen.getByText(/attempt 2/)).toBeInTheDocument();
  });

  it('shows a countdown when retryAt is in the future', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 5 * 60 * 1000; // 5 minutes from now
    render(<RateLimitBanner notice={makeNotice({ retryAt })} />);

    expect(screen.getByText(/Retrying in/)).toBeInTheDocument();
    // Should show ~5m initially
    expect(screen.getByText(/5m/)).toBeInTheDocument();
  });

  it('counts down every second', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 65_000; // 1m 5s
    render(<RateLimitBanner notice={makeNotice({ retryAt })} />);

    expect(screen.getByText(/1m 5s/)).toBeInTheDocument();

    // Advance 10 seconds → 55s remaining (below 60s → "55s" format)
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(screen.getByText(/55s/)).toBeInTheDocument();
  });

  it('stops showing the countdown once it reaches zero', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 2_000; // 2 seconds
    render(<RateLimitBanner notice={makeNotice({ retryAt })} />);

    expect(screen.getByText(/Retrying in/)).toBeInTheDocument();

    // Advance past the deadline
    act(() => { vi.advanceTimersByTime(3_000); });
    expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
  });

  it('hides the countdown when retryAt is 0', () => {
    render(<RateLimitBanner notice={makeNotice({ retryAt: 0 })} />);
    expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
  });
});
