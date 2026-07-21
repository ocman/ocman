// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import * as Toast from '@radix-ui/react-toast';
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

function renderBanner(notice: SessionNotice) {
  return render(
    <Toast.Provider>
      <RateLimitBanner notice={notice} onChangeModel={() => {}} />
      <Toast.Viewport />
    </Toast.Provider>,
  );
}

afterEach(() => {
  vi.useRealTimers();
});

describe('RateLimitBanner', () => {
  it('renders generic error notices', () => {
    renderBanner({ kind: 'error', message: 'x', retryAt: 0, attempt: 0 });
    expect(screen.getByText(/Error/)).toBeInTheDocument();
    expect(screen.getByText(/x/)).toBeInTheDocument();
  });

  it('renders provider overload notices', () => {
    renderBanner({ kind: 'provider_overloaded', message: 'provider is overloaded', retryAt: 0, attempt: 0 });
    expect(screen.getByText(/Provider overloaded/)).toBeInTheDocument();
    expect(screen.getByText(/provider is overloaded/)).toBeInTheDocument();
  });

  it('renders the message and attempt', () => {
    renderBanner(makeNotice({ message: 'too fast', attempt: 2 }));
    expect(screen.getByText(/too fast/)).toBeInTheDocument();
    expect(screen.getByText(/attempt 2/)).toBeInTheDocument();
  });

  it('suggests changing the model for rate limits', () => {
    const onChangeModel = vi.fn();
    render(
      <Toast.Provider>
        <RateLimitBanner notice={makeNotice()} onChangeModel={onChangeModel} />
        <Toast.Viewport />
      </Toast.Provider>,
    );

    expect(screen.getByText('Try another model to continue.')).toBeInTheDocument();
    screen.getByRole('button', { name: 'Change model' }).click();
    expect(onChangeModel).toHaveBeenCalledOnce();
  });

  it('hides the model action when model selection is unavailable', () => {
    render(
      <Toast.Provider>
        <RateLimitBanner notice={makeNotice()} />
        <Toast.Viewport />
      </Toast.Provider>,
    );

    expect(screen.queryByRole('button', { name: 'Change model' })).toBeNull();
  });

  it('shows a countdown when retryAt is in the future', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 5 * 60 * 1000; // 5 minutes from now
    renderBanner(makeNotice({ retryAt }));

    expect(screen.getByText(/Retrying in/)).toBeInTheDocument();
    // Should show ~5m initially
    expect(screen.getByText(/5m/)).toBeInTheDocument();
  });

  it('counts down every second', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 65_000; // 1m 5s
    renderBanner(makeNotice({ retryAt }));

    expect(screen.getByText(/1m 5s/)).toBeInTheDocument();

    // Advance 9 seconds (below the 10 s toast auto-hide) → 56s
    // remaining (below 60s → "56s" format)
    act(() => { vi.advanceTimersByTime(9_000); });
    expect(screen.getByText(/56s/)).toBeInTheDocument();
  });

  it('stops showing the countdown once it reaches zero', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    vi.setSystemTime(new Date('2025-01-01T00:00:00Z'));

    const retryAt = Date.now() + 2_000; // 2 seconds
    renderBanner(makeNotice({ retryAt }));

    expect(screen.getByText(/Retrying in/)).toBeInTheDocument();

    // Advance past the deadline
    act(() => { vi.advanceTimersByTime(3_000); });
    expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
  });

  it('hides the countdown when retryAt is 0', () => {
    renderBanner(makeNotice({ retryAt: 0 }));
    expect(screen.queryByText(/Retrying in/)).not.toBeInTheDocument();
  });

  it('auto-hides after 10 seconds', () => {
    vi.useFakeTimers({ shouldAdvanceTime: false });
    renderBanner(makeNotice({ message: 'slow down' }));
    expect(screen.getByTestId('rate-limit-banner')).toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(10_500); });
    expect(screen.queryByTestId('rate-limit-banner')).toBeNull();
  });
});
