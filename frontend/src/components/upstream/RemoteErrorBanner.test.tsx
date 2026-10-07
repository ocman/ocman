// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { UpstreamApiError } from '../../lib/upstreamApi';
import { RemoteErrorBanner } from './RemoteErrorBanner';

describe('RemoteErrorBanner', () => {
  it.each([
    new UpstreamApiError(null, 404),
    new UpstreamApiError({ error: { code: 'upstream_status', status: 404, message: 'Not found' } }, 502),
    new UpstreamApiError({ error: { code: 'upstream_status', message: 'github /repos/owner/repo/pulls: status 404' } }, 502),
  ])('renders a not-found response as a muted retryable notice', (error) => {
    const retry = vi.fn();
    render(<RemoteErrorBanner error={error} onRetry={retry} />);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    expect(screen.getByRole('status')).toHaveClass('oc-upstream-empty');
    expect(screen.getByRole('status')).toHaveTextContent('Check your forge credentials and repository access.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it.each([
    new Error('Network unavailable'),
    new UpstreamApiError({ error: { code: 'upstream_status', message: 'forgejo /repos/404/pulls: status 500' } }, 502),
    new UpstreamApiError({ error: { code: 'other', message: 'unexpected: status 404' } }, 502),
  ])('keeps other failures visible as errors', (error) => {
    const retry = vi.fn();
    render(<RemoteErrorBanner error={error} onRetry={retry} />);
    expect(screen.getByRole('alert')).toHaveTextContent(error.message);
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
  });

  it('preserves the authentication hint', () => {
    render(<RemoteErrorBanner error={new UpstreamApiError({ error: { code: 'auth_required', message: 'Login required' } }, 401)} onRetry={vi.fn()} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Not authenticated.');
    expect(screen.getByRole('alert')).toHaveTextContent('tea login add');
  });

  it('preserves rate-limit retry timing', () => {
    render(<RemoteErrorBanner error={new UpstreamApiError({ error: { code: 'rate_limited', message: 'Rate limited', retryAfter: new Date(Date.now() + 120_000).toISOString() } }, 429)} onRetry={vi.fn()} />);
    expect(screen.getByRole('alert')).toHaveTextContent('Retry in');
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
  });
});
