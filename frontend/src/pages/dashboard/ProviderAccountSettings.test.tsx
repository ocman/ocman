// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { ProviderAccountSettings, ProviderLimitsCard } from './ProviderAccountSettings';

vi.mock('../../lib/api', () => ({ api: { providerAccounts: vi.fn() } }));
import { api } from '../../lib/api';

const providerAccounts = vi.mocked(api.providerAccounts);

function renderSettings(node = <ProviderAccountSettings />) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{node}</QueryClientProvider>);
}

afterEach(() => vi.clearAllMocks());

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

  it('shows limit utilisation on the usage views only when an account exists', async () => {
    providerAccounts.mockResolvedValueOnce({
      accounts: [{
        platform: 'claudecode', displayName: 'Claude Code', status: 'ok', subscriptionType: 'pro',
        rateLimits: [{ name: 'five_hour', utilization: 0.5, resetsAt: 0 }],
      }],
    });
    const { unmount } = renderSettings(<ProviderLimitsCard />);
    expect(await screen.findByRole('heading', { name: 'Subscription limits' })).toBeInTheDocument();
    expect(await screen.findByText('5-hour: 50% used')).toBeInTheDocument();
    unmount();

    providerAccounts.mockResolvedValueOnce({ accounts: [] });
    const { container } = renderSettings(<ProviderLimitsCard />);
    await vi.waitFor(() => expect(providerAccounts).toHaveBeenCalledTimes(2));
    expect(container).toBeEmptyDOMElement();
  });
});
