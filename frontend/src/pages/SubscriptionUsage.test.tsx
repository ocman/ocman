// @vitest-environment jsdom
import { fireEvent, render as renderUI, screen, within } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import type { ReactElement } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useSubscriptionUsage } from '../lib/queries';
import { SubscriptionUsage, SubscriptionUsageContent } from './SubscriptionUsage';

vi.mock('../lib/queries', () => ({ useSubscriptionUsage: vi.fn() }));
vi.mock('./SubscriptionUsageHistory', () => ({ SubscriptionUsageHistory: () => <div>Subscription history panel</div> }));

function render(ui: ReactElement) { return renderUI(ui, { wrapper: MemoryRouter }); }

describe('SubscriptionUsage', () => {
  beforeEach(() => vi.resetAllMocks());

  it.each([false, true])('shows elapsed-time dots in provider usage, compact=%s', (compact) => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-12T12:00:00Z'));
    try {
      vi.mocked(useSubscriptionUsage).mockReturnValue({
        data: { providers: [{ id: 'codex', name: 'Codex', status: 'ok', windows: [
          { name: '5 hours', usedPercent: 70, resetsAt: '2026-11-12T14:30:00Z' },
        ] }] },
      } as never);
      render(<SubscriptionUsageContent compact={compact} />);
      expect(screen.getByRole('img', { name: '50% of period elapsed. Usage is above elapsed time.' })).toHaveStyle({ left: '50%' });
      expect(screen.getByRole('progressbar', { name: 'Codex 5 hours usage' })).toHaveValue(70);
    } finally {
      vi.useRealTimers();
    }
  });

  it('keeps named accounts of the same tool separate and preserves cards on rename', () => {
    const providers = [
      { id: 'cnx_work', name: 'Claude Work', platformName: 'Claude Code', machineName: 'This machine', status: 'ok', windows: [{ name: '7 days', usedPercent: 25 }] },
      { id: 'cnx_personal', name: 'Claude Personal', platformName: 'Claude Code', machineName: 'This machine', status: 'signed_out', windows: [] },
    ];
    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers } } as never);
    const view = render(<SubscriptionUsageContent />);
    const work = screen.getByRole('region', { name: 'Claude Work' });
    expect(within(work).getByText('Claude Code · This machine')).toBeInTheDocument();
    expect(within(work).getByRole('progressbar', { name: 'Claude Work 7 days usage' })).toHaveValue(25);
    const personal = screen.getByRole('region', { name: 'Claude Personal' });
    expect(within(personal).getByText('Sign in using the platform app to view usage.')).toBeInTheDocument();
    expect(within(personal).queryByRole('progressbar')).not.toBeInTheDocument();
    providers[0] = { ...providers[0], name: 'Claude Office' };
    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers } } as never);
    view.rerender(<SubscriptionUsageContent />);
    expect(screen.getByRole('region', { name: 'Claude Office' })).toBe(work);
    expect(screen.getByRole('progressbar', { name: 'Claude Office 7 days usage' })).toHaveValue(25);
  });

  it.each([
    [0, 'normal'], [80, 'normal'], [80.1, 'warning'],
    [99.9, 'warning'], [100, 'danger'], [120, 'danger'],
  ])('accents quota bars and percentages at %s percent as %s', (usedPercent, level) => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [{ id: 'codex', name: 'Codex', status: 'ok', windows: [
        { name: '5 hours', usedPercent },
      ] }] },
    } as never);
    render(<SubscriptionUsageContent />);
    const progress = screen.getByRole('progressbar', { name: 'Codex 5 hours usage' });
    expect(progress.closest('.subscription-window')).toHaveAttribute('data-usage-level', level);
    expect(screen.getByText(`${usedPercent}% used`).closest('.subscription-window')).toBe(progress.closest('.subscription-window'));
  });

  it('keeps the selected view and unrelated parameters in the URL', async () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [] } } as never);
    function Location() { return <output data-testid="view-location">{useLocation().search}</output>; }
    renderUI(<MemoryRouter initialEntries={['/provider-usage?days=30&remoteId=local']}><SubscriptionUsage /><Location /></MemoryRouter>);
    fireEvent.keyDown(screen.getByRole('tab', { name: 'History' }), { key: 'Enter' });
    expect(await screen.findByText('Subscription history panel')).toBeInTheDocument();
    expect(screen.getByTestId('view-location')).toHaveTextContent('?days=30&remoteId=local&view=history');
    fireEvent.keyDown(screen.getByRole('tab', { name: 'Current usage' }), { key: 'Enter' });
    expect(screen.getByTestId('view-location')).toHaveTextContent('?days=30&remoteId=local');
    expect(screen.queryByText('Subscription history panel')).not.toBeInTheDocument();
  });

  it('shows remaining platform-reported budgets without a token estimate', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [{ id: 'opencode', name: 'OpenCode', status: 'ok', windows: [], budgets: [{
        name: 'Monthly budget', limitUSD: 100, spentUSD: 25, remainingUSD: 75, resetsAt: '2026-12-01T00:00:00Z',
      }] }] },
    } as never);
    render(<SubscriptionUsage />);
    expect(screen.getByText('$75.00 remaining')).toBeInTheDocument();
    expect(screen.getByText('$25.00 of $100.00 used')).toBeInTheDocument();
    expect(screen.queryByText('No quota windows reported.')).not.toBeInTheDocument();
    expect(screen.queryByText(/Estimated cost/)).not.toBeInTheDocument();
    expect(screen.getByRole('progressbar', { name: 'Monthly budget budget usage' })).toHaveValue(25);
  });

  it('shows provider usage with a scoped seven-day cost estimate', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      data: { providers: [{ id: 'typesafe', name: 'TypeSafe', status: 'ok', windows: [], spend: {
        estimatedUSD: 0.0042, inputTokens: 100000, outputTokens: 20, requests: 2,
        unpricedRequests: 1, periodStart: '2026-10-12T12:00:00Z', periodEnd: '2026-10-19T12:00:00Z',
      } }] },
    } as never);
    render(<SubscriptionUsage />);
    expect(screen.getByRole('heading', { name: 'Provider usage' })).toBeInTheDocument();
    expect(screen.getByText('$0.0042')).toBeInTheDocument();
    expect(screen.getByText('Estimated cost · last 7 days')).toBeInTheDocument();
    expect(screen.getByText(/Recorded by this ocman instance only/)).toBeInTheDocument();
    expect(screen.getByText(/1 request could not be priced/)).toBeInTheDocument();
    expect(screen.queryByText('No quota windows reported.')).not.toBeInTheDocument();
  });

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
      error: new Error('Could not load provider usage.'),
      refetch,
      isFetching: false,
    };
    vi.mocked(useSubscriptionUsage).mockReturnValue(result as never);

    const { rerender } = render(<SubscriptionUsage />);
    expect(screen.getByText('Temporarily rate limited')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Could not load provider usage.');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(refetch).toHaveBeenCalledOnce();
    vi.mocked(useSubscriptionUsage).mockReturnValue({ ...result, isFetching: true } as never);
    rerender(<SubscriptionUsage />);
    expect(screen.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Retry' })).toHaveAttribute('aria-busy', 'true');
    expect(screen.getByRole('heading', { name: 'Anthropic' })).toBeInTheDocument();
    expect(screen.queryByText('Loading provider usage')).not.toBeInTheDocument();
  });

  it('asks signed-out users to sign in through their platform app', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [{ id: 'codex', name: 'Codex', status: 'signed_out', windows: [] }] } } as never);
    render(<SubscriptionUsage />);
    expect(screen.getByText('Sign in using the platform app to view usage.')).toBeInTheDocument();
  });

  it('shows loading and empty states', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({ isLoading: true } as never);
    const { rerender } = render(<SubscriptionUsage />);
    expect(screen.getByRole('status')).toHaveTextContent('Loading provider usage');
    expect(screen.getByRole('status').firstElementChild).toHaveAttribute('aria-hidden', 'true');

    vi.mocked(useSubscriptionUsage).mockReturnValue({ data: { providers: [] } } as never);
    rerender(<SubscriptionUsage />);
    expect(screen.getByText('No enabled platform apps report provider usage.')).toBeInTheDocument();
    expect(screen.queryByText('Loading provider usage')).not.toBeInTheDocument();
  });

  it('keeps existing quota data visible while loading', () => {
    vi.mocked(useSubscriptionUsage).mockReturnValue({
      isLoading: true,
      isFetching: true,
      data: { providers: [{ id: 'openai', name: 'OpenAI', status: 'ok', windows: [
        { name: '5 hours', usedPercent: 10 },
      ] }] },
    } as never);
    render(<SubscriptionUsage />);
    expect(screen.getByRole('progressbar', { name: 'OpenAI 5 hours usage' })).toHaveValue(10);
    expect(screen.queryByText('Loading provider usage')).not.toBeInTheDocument();
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
