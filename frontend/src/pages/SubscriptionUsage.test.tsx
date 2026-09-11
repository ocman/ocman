// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useSubscriptionUsage } from '../lib/queries';
import { SubscriptionUsage } from './SubscriptionUsage';

vi.mock('../lib/queries', () => ({ useSubscriptionUsage: vi.fn() }));

describe('SubscriptionUsage', () => {
  beforeEach(() => vi.resetAllMocks());

  it('shows every subscription and quota window', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [
        { id: 'openai', name: 'OpenAI', plan: 'pro', status: 'ok', windows: [
          { name: '7 days', usedPercent: 10, resetsAt: '2026-09-15T12:00:00Z' },
          { name: 'Codex Spark · 5 hours', usedPercent: 45 },
        ] },
        { id: 'anthropic', name: 'Anthropic', status: 'ok', windows: [
          { name: '5 hours', usedPercent: 1, resetsAt: '2026-09-11T16:10:00Z' },
        ] },
      ] },
    } as never);

    render(<SubscriptionUsage />);

    expect(screen.getByRole('heading', { name: 'OpenAI' })).toBeInTheDocument();
    expect(screen.getByText('Pro')).toBeInTheDocument();
    expect(screen.getByRole('progressbar', { name: 'OpenAI 7 days usage' })).toHaveValue(10);
    expect(screen.getByRole('progressbar', { name: 'OpenAI Codex Spark · 5 hours usage' })).toHaveValue(45);
    expect(screen.getByRole('progressbar', { name: 'Anthropic 5 hours usage' })).toHaveValue(1);
    expect(screen.getAllByText(/% used/)).toHaveLength(3);
  });

  it('shows provider errors and retries the request', () => {
    const refetch = vi.fn();
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [{ id: 'anthropic', name: 'Anthropic', status: 'rate_limited', windows: [] }] },
      error: new Error('Could not load subscription usage.'),
      refetch,
    } as never);

    render(<SubscriptionUsage />);
    expect(screen.getByText('Temporarily rate limited')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load subscription usage.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(refetch).toHaveBeenCalledOnce();
  });

  it('shows loading and empty states', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({ isLoading: true } as never);
    const { rerender } = render(<SubscriptionUsage />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading subscription usage');

    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [] } } as never);
    rerender(<SubscriptionUsage />);
    expect(screen.getByText('No OpenCode subscription credentials found.')).toBeInTheDocument();
  });
});
