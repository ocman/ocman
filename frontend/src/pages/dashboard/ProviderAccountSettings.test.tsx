// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProviderAccountSettings, ProviderLimitsCard } from './ProviderAccountSettings';
import { useProviderUsageHistory } from '../../lib/providerUsageHistory';

vi.mock('../../lib/api', () => ({ api: { providerAccounts: vi.fn() } }));
vi.mock('../../lib/providerUsageHistory', () => ({ useProviderUsageHistory: vi.fn() }));
const chart = vi.hoisted(() => vi.fn());
vi.mock('react-chartjs-2', () => ({ Line: (props: { 'aria-label': string }) => {
  chart(props);
  return <div role="img" aria-label={props['aria-label']} />;
} }));
import { api } from '../../lib/api';

const providerAccounts = vi.mocked(api.providerAccounts);

function renderSettings(node = <ProviderAccountSettings />) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

afterEach(() => vi.clearAllMocks());
beforeEach(() => vi.mocked(useProviderUsageHistory).mockReturnValue({ refetch: vi.fn() } as never));

const historyData = { sampleIntervalMs: 300000, samples: [0, 1].map(index => ({
  sampledAt: 1793548800000 + index * 300000,
  provider: { id: 'claudecode', name: 'Claude Code', status: 'ok', windows: [{ name: '5 hours', usedPercent: 20 + index * 10 }],
    budgets: [{ name: 'Monthly budget', limitUSD: 100, spentUSD: 25, remainingUSD: 75 }],
  },
})) };

describe('ProviderAccountSettings', () => {
  it('shows the subscription type and both limit windows from the provider', async () => {
    providerAccounts.mockResolvedValue({
      accounts: [
        {
          platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'max',
          rateLimits: [
            { name: 'five_hour', utilization: 0.28, resetsAt: Date.now() + 2 * 3600_000 },
            { name: 'weekly', utilization: 0.765, resetsAt: 0 },
          ],
        },
        { platform: 'r-x:claudecode', displayName: 'Claude Code (x)', status: 'signed_out', subscriptionType: '', rateLimits: [] },
      ],
    });
    renderSettings();

    expect(await screen.findByText('Subscription: Max')).toBeInTheDocument();
    expect(screen.getByText(/^5-hour: 28% used · resets in/)).toBeInTheDocument();
    expect(screen.getByText('Weekly: 77% used')).toBeInTheDocument();
    expect(screen.getByLabelText('Claude Code Weekly utilisation')).toHaveAttribute('value', '77');
    expect(screen.getByText('Not signed in.')).toBeInTheDocument();
  });

  it('marks windows not reported yet, and says when no account exists', async () => {
    providerAccounts.mockResolvedValueOnce({
      accounts: [{
        platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'Claude Team',
        rateLimits: [{ name: 'weekly', utilization: 0.76, resetsAt: 0 }],
      }],
    });
    const { unmount } = renderSettings();
    expect(await screen.findByText('Subscription: Claude Team')).toBeInTheDocument();
    expect(screen.getByText('5-hour: not reported yet')).toBeInTheDocument();
    expect(screen.queryByLabelText('Claude Code 5-hour utilisation')).not.toBeInTheDocument();
    expect(screen.getByText('Weekly: 76% used')).toBeInTheDocument();
    unmount();

    providerAccounts.mockResolvedValueOnce({ accounts: [] });
    renderSettings();
    expect(await screen.findByText('No enabled platform reports a subscription account.')).toBeInTheDocument();
  });

  it('shows a failed request', async () => {
    providerAccounts.mockRejectedValueOnce(new Error('HTTP 403'));
    renderSettings();
    expect(await screen.findByRole('alert')).toHaveTextContent('HTTP 403');
  });

  it('graphs observed usage in analytics only when an account exists', async () => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: historyData } as never);
    providerAccounts.mockResolvedValueOnce({
      accounts: [{
        platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro',
        rateLimits: [{ name: 'five_hour', utilization: 0.5, resetsAt: 0 }],
      }],
    });
    const { unmount } = renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByRole('heading', { name: 'Subscription limits' })).toBeInTheDocument();
    expect(await screen.findByRole('img', { name: 'Claude Code subscription usage history' })).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    expect(useProviderUsageHistory).toHaveBeenCalledWith(7);
    expect(chart.mock.lastCall?.[0].data.datasets).toMatchObject([
      { label: '5 hours', data: [{ y: 20 }, { y: 30 }] },
      { label: 'Monthly budget', data: [{ y: 25 }, { y: 25 }] },
    ]);
    unmount();

    providerAccounts.mockResolvedValueOnce({ accounts: [] });
    const { container } = renderSettings(<ProviderLimitsCard />);
    await vi.waitFor(() => expect(providerAccounts).toHaveBeenCalledTimes(2));
    expect(container).toBeEmptyDOMElement();
  });

  it('shows history loading and empty states without replacing account settings', async () => {
    providerAccounts.mockResolvedValue({ accounts: [{ platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro', rateLimits: [] }] });
    vi.mocked(useProviderUsageHistory).mockReturnValue({ isLoading: true } as never);
    const view = renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByText('Loading subscription history')).toBeInTheDocument();
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: { sampleIntervalMs: 300000, samples: [] } } as never);
    view.rerender(<QueryClientProvider client={new QueryClient()}><ProviderLimitsCard /></QueryClientProvider>);
    expect(await screen.findByText(/Graphs start with the next successful platform reading/)).toBeInTheDocument();
  });

  it('keeps the last graph during history errors and retries the history request', async () => {
    providerAccounts.mockResolvedValue({ accounts: [{ platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro', rateLimits: [] }] });
    const refetch = vi.fn();
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: historyData, error: new Error('History unavailable'), refetch } as never);
    renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByRole('img')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('History unavailable');
    expect(screen.queryByText(/Graphs start/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(refetch).toHaveBeenCalledTimes(2);
  });
});
