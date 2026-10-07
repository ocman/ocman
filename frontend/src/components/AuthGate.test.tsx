// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, cleanup, render, screen } from '@testing-library/react';
import { useAuthStore } from '../lib/authStore';
import { AuthGate } from './AuthGate';

const original = useAuthStore.getState();
beforeEach(() => {
  useAuthStore.setState({ checking: false, authRequired: false, authenticated: true, error: null, submitting: false, bootstrap: vi.fn().mockResolvedValue(undefined) });
});
afterEach(() => {
  cleanup();
  useAuthStore.setState(original);
  vi.useRealTimers();
});

describe('AuthGate', () => {
  it('replaces the loading state with the backend banner after the boot timeout', () => {
    vi.useFakeTimers();
    const bootstrap = vi.fn(() => new Promise<void>(() => {}));
    useAuthStore.setState({ checking: true, bootstrap });
    render(<AuthGate><p>app</p></AuthGate>);
    expect(screen.getByRole('status')).toHaveTextContent('Checking authentication…');
    expect(screen.queryByText('app')).not.toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(8_000); });
    expect(screen.getByTestId('backend-status-banner')).toHaveTextContent('Backend is not responding.');
    act(() => { screen.getByRole('button', { name: 'Retry' }).click(); });
    expect(bootstrap).toHaveBeenCalledTimes(2);
    act(() => { useAuthStore.setState({ checking: false }); });
    expect(screen.getByText('app')).toBeInTheDocument();
  });

  it('keeps the app behind the login screen when authentication is required', () => {
    useAuthStore.setState({ authRequired: true, authenticated: false });
    render(<AuthGate><p>app</p></AuthGate>);
    expect(screen.getByRole('heading', { name: 'ocman' })).toBeInTheDocument();
    expect(screen.queryByText('app')).not.toBeInTheDocument();
  });

  it.each([
    { authRequired: false, authenticated: false },
    { authRequired: true, authenticated: true },
  ])('opens the app for %j', (state) => {
    useAuthStore.setState(state);
    render(<AuthGate><p>app</p></AuthGate>);
    expect(screen.getByText('app')).toBeInTheDocument();
  });
});
