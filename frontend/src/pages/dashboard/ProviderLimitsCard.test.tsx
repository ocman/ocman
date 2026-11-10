// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProviderLimitsCard } from './ProviderLimitsCard';
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

function renderSettings(node = <ProviderLimitsCard />) {
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

describe('ProviderLimitsCard', () => {
  it('graphs observed usage in analytics only when an account exists', async () => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: historyData } as never);
    providerAccounts.mockResolvedValueOnce({
      accounts: [{
        platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro',
        rateLimits: [{ name: 'five_hour', utilization: 0.5, resetsAt: 0 }],
      }],
    });
    const { unmount } = renderSettings(<ProviderLimitsCard days={30} />);
    expect(await screen.findByRole('heading', { name: 'Subscription burndown' })).toBeInTheDocument();
    expect(await screen.findByRole('img', { name: 'Claude Code subscription usage history' })).toBeInTheDocument();
    expect(screen.queryByRole('progressbar')).not.toBeInTheDocument();
    expect(useProviderUsageHistory).toHaveBeenCalledWith(30);
    expect(screen.getByText(/Last 30 days/)).toBeInTheDocument();
    expect(chart.mock.lastCall?.[0].data.datasets).toMatchObject([
      { label: '5 hours', data: [{ y: 20 }, { y: 30 }] },
      { label: 'Monthly budget', data: [{ y: 25 }, { y: 25 }] },
    ]);
    unmount();

    providerAccounts.mockResolvedValueOnce({ accounts: [] });
    const { container } = renderSettings(<ProviderLimitsCard />);
    await vi.waitFor(() => expect(providerAccounts).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(container).toBeEmptyDOMElement());
  });

  it('shows history loading and empty states in analytics', async () => {
    providerAccounts.mockResolvedValue({ accounts: [{ platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro', rateLimits: [] }] });
    vi.mocked(useProviderUsageHistory).mockReturnValue({ isLoading: true } as never);
    const view = renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByRole('status', { name: 'Loading subscription history' })).toBeInTheDocument();
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: { sampleIntervalMs: 300000, samples: [] } } as never);
    view.rerender(<QueryClientProvider client={new QueryClient()}><ProviderLimitsCard /></QueryClientProvider>);
    expect(await screen.findByText(/Graphs start with the next successful platform reading/)).toBeInTheDocument();
  });

  it('shows a chart skeleton while subscription accounts are loading', () => {
    providerAccounts.mockReturnValue(new Promise(() => {}));
    renderSettings(<ProviderLimitsCard />);
    expect(screen.getByRole('status', { name: 'Loading subscription history' })).toBeInTheDocument();
  });

  it.each([[0, 'All history'], [1, 'Last 24 hours']])('labels the selected %s-day period', async (days, label) => {
    providerAccounts.mockResolvedValue({ accounts: [{ platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro', rateLimits: [] }] });
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: historyData } as never);
    renderSettings(<ProviderLimitsCard days={Number(days)} />);
    expect(await screen.findByText(`${label}. Gaps indicate missing readings or quota resets.`)).toBeInTheDocument();
    expect(useProviderUsageHistory).toHaveBeenCalledWith(days);
  });

  it('keeps the last graph during history errors and retries the history request', async () => {
    providerAccounts.mockResolvedValue({ accounts: [{ platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro', rateLimits: [] }] });
    const refetch = vi.fn();
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: historyData, isLoading: true, error: new Error('History unavailable'), refetch } as never);
    renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByRole('img')).toBeInTheDocument();
    expect(screen.queryByRole('status', { name: 'Loading subscription history' })).not.toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('History unavailable');
    expect(screen.queryByText(/Graphs start/)).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(refetch).toHaveBeenCalledTimes(2);
  });
});
