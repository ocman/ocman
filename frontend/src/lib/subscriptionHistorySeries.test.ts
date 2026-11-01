import { describe, expect, it } from 'vitest';
import { subscriptionHistorySeries } from './subscriptionHistorySeries';
import type { SubscriptionUsageHistory, SubscriptionUsageSample } from './providerUsageHistory';

const interval = 300_000;
function sample(at: number, usedPercent: number, reset = '2026-11-02T00:00:00Z'): SubscriptionUsageSample {
  return { sampledAt: at, provider: { id: 'codex', name: 'Codex', plan: 'pro', status: 'ok', windows: [{ name: '5 hours', usedPercent, resetsAt: reset }] } };
}
const history = (samples: SubscriptionUsageSample[]): SubscriptionUsageHistory => ({ sampleIntervalMs: interval, samples });

describe('subscriptionHistorySeries', () => {
  it('groups platforms and sorts observations without changing the stored input', () => {
    const first = sample(interval, 10);
    const second = sample(interval * 2, 20);
    const claude = { ...first, provider: { ...first.provider, id: 'claudecode', name: 'Claude Code' } };
    const samples = [second, first, claude];
    const groups = subscriptionHistorySeries(history(samples));
    expect(groups.map(group => group.name)).toEqual(['Codex', 'Claude Code']);
    expect(groups[0].series[0].data).toEqual([{ x: interval, y: 10 }, { x: interval * 2, y: 20 }]);
    expect(samples[0]).toBe(second);
  });

  it('does not turn unavailable readings or absent windows into zeros', () => {
    const failed = sample(interval * 2, 0);
    failed.provider.status = 'upstream_error';
    const absent = sample(interval * 3, 0);
    absent.provider.windows = [];
    const group = subscriptionHistorySeries(history([sample(interval, 10), failed, absent, sample(interval * 4, 30)]))[0];
    expect(group.series[0].data.map(point => point.y)).toEqual([10, null, null, 30]);
  });

  it.each(['reset', 'plan', 'shutdown'])('breaks the line across a %s', reason => {
    const first = sample(interval, 80);
    const next = sample(interval * 2, 10);
    if (reason === 'reset') next.provider.windows[0].resetsAt = '2026-11-03T00:00:00Z';
    if (reason === 'plan') next.provider.plan = 'plus';
    if (reason === 'shutdown') next.sampledAt = interval * 5;
    const points = subscriptionHistorySeries(history([first, next]))[0].series[0].data;
    expect(points).toEqual([{ x: first.sampledAt, y: 80 }, { x: next.sampledAt - 1, y: null }, { x: next.sampledAt, y: 10 }]);
  });

  it('keeps budget IDs separate, labels their scope, and bounds quota percentages', () => {
    const value = sample(interval, -1);
    value.provider.budgets = [
      { id: 'personal', name: 'Monthly', description: 'Personal', spentUSD: 25, limitUSD: 100, remainingUSD: 75 },
      { id: 'workspace', name: 'Monthly', spentUSD: 20, limitUSD: 10, remainingUSD: 0 },
      { name: 'Blocked', spentUSD: 0, limitUSD: 0, remainingUSD: 0 },
    ];
    const series = subscriptionHistorySeries(history([value]))[0].series;
    expect(series.map(item => [item.key, item.label, item.data[0].y])).toEqual([
      ['window:5 hours', '5 hours', 0], ['budget:personal', 'Monthly · Personal', 25],
      ['budget:workspace', 'Monthly', 100], ['budget:Blocked', 'Blocked', 100],
    ]);
  });

  it('does not invent history for plans without any reported quota or budget', () => {
    const value = sample(interval, 1);
    value.provider.windows = [];
    expect(subscriptionHistorySeries(history([value]))).toEqual([]);
    expect(subscriptionHistorySeries(history([]))).toEqual([]);
  });

  it('connects adjacent hourly snapshots but still breaks at multi-hour outages', () => {
    const hour = 3_600_000;
    const samples = [1, 2, 5].map(index => ({ ...sample(index * hour, index * 10), sampleIntervalMs: hour }));
    const points = subscriptionHistorySeries(history(samples))[0].series[0].data;
    expect(points).toEqual([{ x: hour, y: 10 }, { x: 2 * hour, y: 20 }, { x: 5 * hour - 1, y: null }, { x: 5 * hour, y: 50 }]);
  });

  it('handles the transition between hourly and five-minute samples', () => {
    const hour = 3_600_000;
    const first = { ...sample(hour, 10), sampleIntervalMs: hour };
    const second = { ...sample(hour + interval, 20), sampleIntervalMs: interval };
    expect(subscriptionHistorySeries(history([first, second]))[0].series[0].data).toEqual([{ x: hour, y: 10 }, { x: hour + interval, y: 20 }]);
  });
});
