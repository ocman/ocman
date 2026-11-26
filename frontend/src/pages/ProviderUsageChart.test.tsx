// @vitest-environment jsdom
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ProviderUsageChart } from './ProviderUsageChart';

const chart = vi.hoisted(() => vi.fn());
vi.mock('react-chartjs-2', () => ({ Line: (props: { 'aria-label': string }) => {
  chart(props);
  return <div role="img" aria-label={props['aria-label']} />;
} }));

describe('ProviderUsageChart', () => {
  beforeEach(() => vi.resetAllMocks());
  afterEach(() => vi.restoreAllMocks());

  it('plots timestamped percentages with gaps and an accessible name', () => {
    const data = [{ x: 1793548800000, y: 20 }, { x: 1793549100000, y: null }, { x: 1793549400000, y: 30 }];
    render(<ProviderUsageChart name="Codex" series={[{ key: 'window:5 hours', label: '5 hours', data }]} />);
    expect(screen.getByRole('img', { name: 'Codex subscription usage history' })).toBeInTheDocument();
    const props = chart.mock.lastCall?.[0];
    expect(props.data.datasets[0]).toMatchObject({ label: '5 hours', data, spanGaps: false, pointRadius: 0 });
    expect(props.options.scales.x).toMatchObject({ type: 'linear', bounds: 'data' });
    expect(props.options.scales.x.ticks.maxTicksLimit).toBe(5);
    expect(props.options.scales.x.ticks.callback(data[0].x)).toContain('Nov');
    expect(props.options.scales.y).toMatchObject({ min: 0, max: 100 });
    expect(props.options.scales.y.ticks.callback(25)).toBe('25%');
    const tooltip = props.options.plugins.tooltip.callbacks;
    expect(tooltip.title([])).toBe('');
    expect(tooltip.title([{ parsed: { x: data[0].x } }])).toBe(new Date(data[0].x).toLocaleString());
    expect(tooltip.label({ dataset: { label: '5 hours' }, parsed: { y: 20 } })).toBe('5 hours: 20% used');
  });

  it('uses the selected time window without rounding into future dates', () => {
    const now = Date.now();
    vi.spyOn(Date, 'now').mockReturnValue(now);
    const view = render(<ProviderUsageChart name="Codex" series={[]} days={30} />);
    expect(chart.mock.lastCall?.[0].options.scales.x).toMatchObject({ min: now - 30 * 86400000, max: now });
    view.rerender(<ProviderUsageChart name="Codex" series={[]} days={0} />);
    expect(chart.mock.lastCall?.[0].options.scales.x.min).toBeUndefined();
    expect(chart.mock.lastCall?.[0].options.scales.x.max).toBe(now);
    view.rerender(<ProviderUsageChart name="Codex" series={[]} days={7} updatedAt={now + 300000} />);
    expect(chart.mock.lastCall?.[0].options.scales.x).toMatchObject({ min: now + 300000 - 7 * 86400000, max: now + 300000 });
  });

  it('shows plotted windows and budgets, keeping missing readings distinct from zero', () => {
    const now = 1793549400000;
    const at = now - 300_000;
    render(<ProviderUsageChart name="Codex" updatedAt={now} days={1} series={[
      { key: 'window:5h', label: '5 hours', data: [
        { x: now - 86_400_001, y: 90 }, { x: at, y: null }, { x: now - 1, y: null, lineBreak: true },
        { x: now, y: 42.125 }, { x: now + 1, y: 99 },
      ] },
      { key: 'budget:monthly', label: 'Monthly · Personal', data: [{ x: at, y: 0 }, { x: now, y: 20 }] },
    ]} />);
    fireEvent.click(screen.getByRole('button', { name: 'View chart data' }));
    const table = screen.getByRole('table', { name: 'Codex subscription usage history data' });
    const rows = within(table).getAllByRole('row').slice(1);
    expect(rows.map(row => within(row).getAllByRole('cell').slice(1).map(cell => cell.textContent))).toEqual([
      ['5 hours', '42.125%'], ['Monthly · Personal', '20%'], ['5 hours', 'No reading'], ['Monthly · Personal', '0%'],
    ]);
    expect(within(rows[2]).getByRole('time')).toHaveAttribute('datetime', new Date(at).toISOString());
  });

  it('pages long ranges without mounting every row and resets to the latest rows on range change', () => {
    const now = 1793549400000;
    const data = Array.from({ length: 53 }, (_, index) => ({ x: now - (52 - index) * 300_000, y: index }));
    const series = [{ key: 'window:5h', label: '5 hours', data }];
    const view = render(<ProviderUsageChart name="Codex" updatedAt={now} series={series} days={0} />);
    fireEvent.click(screen.getByRole('button', { name: 'View chart data' }));
    const rows = () => within(screen.getByRole('table')).getAllByRole('row');
    const older = screen.getByRole('button', { name: 'Older readings' });
    const newer = screen.getByRole('button', { name: 'Newer readings' });
    expect(newer).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(newer);
    expect(rows()).toHaveLength(26);
    expect(rows()[1]).toHaveTextContent('52%');
    fireEvent.click(older);
    expect(screen.getByRole('status')).toHaveTextContent('Rows 26–50 of 53');
    fireEvent.click(older);
    expect(older).toHaveAttribute('aria-disabled', 'true');
    fireEvent.click(older);
    expect(rows()).toHaveLength(4);
    expect(rows()[3]).toHaveTextContent('0%');
    fireEvent.click(newer);
    expect(rows()[1]).toHaveTextContent('27%');
    view.rerender(<ProviderUsageChart name="Codex" updatedAt={now} series={series} days={1} />);
    expect(rows()[1]).toHaveTextContent('52%');
  });

  it('explains when the displayed period has no readings', () => {
    render(<ProviderUsageChart name="Codex" series={[]} />);
    fireEvent.click(screen.getByRole('button', { name: 'View chart data' }));
    expect(screen.getByText('No readings in this period.')).toBeVisible();
    expect(screen.queryByRole('table')).not.toBeInTheDocument();
  });

  it.each([[25.00000005, 25], [25.49, 25], [25.5, 26], [0, 0], [100, 100]])('rounds %s percent in history labels to %s', (value, expected) => {
    render(<ProviderUsageChart name="Codex" series={[]} />);
    const props = chart.mock.lastCall?.[0];
    expect(props.options.scales.y.ticks.callback(value)).toBe(`${expected}%`);
    expect(props.options.plugins.tooltip.callbacks.label({ dataset: { label: '5 hours' }, parsed: { y: value } })).toBe(`5 hours: ${expected}% used`);
  });
});
