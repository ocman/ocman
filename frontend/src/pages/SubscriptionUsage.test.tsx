// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useSubscriptionUsage } from '../lib/queries';
import { SubscriptionUsage, SubscriptionUsageContent } from './SubscriptionUsage';

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
    expect(screen.getByRole('button', { name: 'Refresh' })).toHaveClass('oc-button', 'oc-button--small');
  });

  it('shows the time left until each window resets', () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-09-11T12:00:00Z'));
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [
        { id: 'anthropic', name: 'Anthropic', status: 'ok', windows: [
          { name: '5 hours', usedPercent: 1, resetsAt: '2026-09-11T16:10:00Z' },
          { name: 'expired', usedPercent: 2, resetsAt: '2026-09-10T16:10:00Z' },
        ] },
      ] },
    } as never);

    render(<SubscriptionUsage />);
    expect(screen.getByText('(in 4h 10m)')).toBeInTheDocument();
    expect(screen.queryByText(/\(in 0s\)/)).not.toBeInTheDocument();
    vi.useRealTimers();
  });

  it('formats reset dates with the weekday and ordinal day', () => {
    const resetsAt = new Date(2026, 8, 19, 13, 38, 36).toISOString();
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [
        { id: 'anthropic', name: 'Anthropic', status: 'ok', windows: [
          { name: '5 hours', usedPercent: 1, resetsAt },
        ] },
      ] },
    } as never);

    render(<SubscriptionUsage />);
    expect(screen.getByText('Saturday Sept 19th 2026, 13:38:36')).toBeInTheDocument();
  });

  it('uses short reset labels in compact mode', () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    vi.setSystemTime(new Date('2026-09-11T12:00:00Z'));
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [{ id: 'anthropic', name: 'Anthropic', status: 'ok', windows: [
        { name: '5 hours', usedPercent: 1, resetsAt: '2026-09-11T16:10:00Z' },
      ] }] },
    } as never);

    render(<SubscriptionUsageContent compact />);
    expect(screen.getByText('Resets in 4h 10m')).toBeInTheDocument();
    expect(screen.queryByText(/Thursday Sept/)).not.toBeInTheDocument();
    vi.useRealTimers();
  });

  it('shows provider errors and retries the request', () => {
    const refetch = vi.fn();
    const result = {
      data: { providers: [{ id: 'anthropic', name: 'Anthropic', status: 'rate_limited', windows: [] }] },
      error: new Error('Could not load subscription usage.'),
      refetch,
      isFetching: false,
    };
    vi.mocked(useSubscriptionUsage).mockReturnValue(result as never);

    const { rerender } = render(<SubscriptionUsage />);
    expect(screen.getByText('Temporarily rate limited')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load subscription usage.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(refetch).toHaveBeenCalledOnce();
    vi.mocked(useSubscriptionUsage).mockReturnValue({ ...result, isFetching: true } as never);
    rerender(<SubscriptionUsage />);
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Retry' })).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('heading', { name: 'Anthropic' })).toBeInTheDocument();
  });

  it('shows loading and empty states', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({ isLoading: true } as never);
    const { rerender } = render(<SubscriptionUsage />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading subscription usage');

    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [] } } as never);
    rerender(<SubscriptionUsage />);
    expect(screen.getByText('No OpenCode subscription credentials found.')).toBeInTheDocument();
  });

  it('refreshes usage and exposes the fetching state on the shared control', () => {
    const refetch = vi.fn();
    vi.mocked(useSubscriptionUsage).mockReturnValue({ refetch, isFetching: false } as never);
    const { rerender } = render(<SubscriptionUsage />);
    const button = screen.getByRole('button', { name: 'Refresh' });
    fireEvent.click(button);
    expect(refetch).toHaveBeenCalledOnce();

    vi.mocked(useSubscriptionUsage).mockReturnValue({ refetch, isFetching: true } as never);
    rerender(<SubscriptionUsage />);
    expect(button).toBeDisabled();
    expect(button).toHaveAttribute('aria-busy', 'true');
    fireEvent.click(button);
    expect(refetch).toHaveBeenCalledOnce();

    vi.mocked(useSubscriptionUsage).mockReturnValue({ refetch, isFetching: false } as never);
    rerender(<SubscriptionUsage />);
    expect(button).toBeEnabled();
    expect(button).toHaveAttribute('aria-busy', 'false');
  });
});
