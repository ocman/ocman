// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { BackendStatusBanner } from './BackendStatusBanner';
import { useBackendStatus } from '../lib/backendStatus';

beforeEach(() => {
  useBackendStatus.setState({ unreachable: false, since: null, error: null });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('BackendStatusBanner', () => {
  it('renders only while unreachable', () => {
    render(<BackendStatusBanner />);
    expect(screen.queryByTestId('backend-status-banner')).toBeNull();
    act(() => useBackendStatus.setState({ unreachable: true, since: 1, error: 'HTTP 502 Bad Gateway' }));
    expect(screen.getByRole('alert').textContent).toContain('Backend is not responding.');
    expect(screen.getByRole('alert').textContent).toContain('HTTP 502 Bad Gateway');
  });

  it('forced mode renders and Retry invokes the handler', () => {
    const onRetry = vi.fn();
    render(<BackendStatusBanner force onRetry={onRetry} />);
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(onRetry).toHaveBeenCalledOnce();
  });

  it('default Retry probes the backend and clears on success', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.resolve(new Response('{"authRequired":false,"authenticated":true}', { status: 200 }))));
    useBackendStatus.setState({ unreachable: true, since: 1, error: null });
    render(<BackendStatusBanner />);
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    });
    expect(useBackendStatus.getState().unreachable).toBe(false);
    expect(screen.queryByTestId('backend-status-banner')).toBeNull();
  });
});
