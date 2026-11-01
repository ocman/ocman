// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useProviderUsageHistory } from '../lib/providerUsageHistory';
import { SubscriptionUsageHistory } from './SubscriptionUsageHistory';

const chart = vi.hoisted(() => vi.fn());
vi.mock('../lib/providerUsageHistory', () => ({ useProviderUsageHistory: vi.fn() }));
vi.mock('react-chartjs-2', () => ({ Line: (props: { 'aria-label': string }) => { chart(props); return <div role="img" aria-label={props['aria-label']} />; } }));

function Location() { return <div data-testid="location">{useLocation().search}</div>; }
function mount(path = '/provider-usage?view=history') {
  return render(<MemoryRouter initialEntries={[path]}><SubscriptionUsageHistory /><Location /></MemoryRouter>);
}
const data = { sampleIntervalMs: 300000, samples: [{ sampledAt: 1793548800000, provider: {
  id: 'codex', name: 'Codex', status: 'ok', windows: [{ name: '5 hours', usedPercent: 20 }],
} }] };

describe('SubscriptionUsageHistory', () => {
  beforeEach(() => vi.resetAllMocks());

  it('graphs observed utilization and preserves URL scope when changing period', () => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data, refetch: vi.fn() } as never);
    mount('/provider-usage?view=history&days=30&remoteId=local');
    expect(useProviderUsageHistory).toHaveBeenCalledWith(30);
    expect(screen.getByRole('img', { name: 'Codex subscription usage history' })).toBeInTheDocument();
    const props = chart.mock.lastCall?.[0];
    expect(props.data.datasets[0]).toMatchObject({ label: '5 hours', spanGaps: false, data: [{ x: 1793548800000, y: 20 }] });
    expect(props.options.scales.y).toMatchObject({ min: 0, max: 100 });
    expect(props.options.scales.y.ticks.callback(25)).toBe('25%');
    expect(props.options.scales.x.ticks.callback(1793548800000)).toContain('Nov');
    expect(props.options.plugins.tooltip.callbacks.title([])).toBe('');
    expect(props.options.plugins.tooltip.callbacks.title([{ parsed: { x: 1793548800000 } }])).not.toBe('');
    fireEvent.change(screen.getByRole('combobox', { name: 'History period' }), { target: { value: '1' } });
    expect(useProviderUsageHistory).toHaveBeenLastCalledWith(1);
    expect(screen.getByTestId('location')).toHaveTextContent('?view=history&days=1&remoteId=local');
  });

  it('shows initial loading, then explains why new histories are empty', () => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ isLoading: true } as never);
    const view = mount('/provider-usage?view=history&days=invalid');
    expect(useProviderUsageHistory).toHaveBeenCalledWith(7);
    expect(screen.getByRole('status')).toHaveTextContent('Loading subscription history');
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data: { ...data, samples: [] } } as never);
    view.rerender(<MemoryRouter><SubscriptionUsageHistory /></MemoryRouter>);
    expect(screen.getByText(/History starts with the next successful platform reading/)).toBeInTheDocument();
  });

  it('keeps the last successful graph visible during refresh failures and retries', () => {
    const refetch = vi.fn();
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data, error: new Error('History read failed'), isLoading: true, refetch } as never);
    mount();
    expect(screen.getByRole('img')).toBeInTheDocument();
    expect(screen.queryByText('Loading subscription history')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    fireEvent.click(screen.getByRole('button', { name: 'Refresh' }));
    expect(refetch).toHaveBeenCalledTimes(2);
  });

  it('does not present an initial failure as an empty history', () => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ error: new Error('History unavailable'), refetch: vi.fn() } as never);
    mount();
    expect(screen.getByRole('alert')).toHaveTextContent('History unavailable');
    expect(screen.queryByText(/No quota windows or budgets recorded/)).not.toBeInTheDocument();
  });

  it.each([0, 90, 365])('allows browsing hourly history with days=%s', days => {
    vi.mocked(useProviderUsageHistory).mockReturnValue({ data, refetch: vi.fn() } as never);
    mount(`/provider-usage?view=history&days=${days}`);
    expect(useProviderUsageHistory).toHaveBeenCalledWith(days);
    expect(screen.getByRole('combobox', { name: 'History period' })).toHaveValue(String(days));
    expect(screen.getByText(/latest snapshot per hour kept indefinitely/)).toBeInTheDocument();
  });
});
