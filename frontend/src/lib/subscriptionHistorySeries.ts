import type { SubscriptionUsageHistory, SubscriptionUsageSample } from './providerUsageHistory';

interface Observation {
  key: string;
  label: string;
  percent: number;
  reset?: string;
}

export interface SubscriptionHistorySeries {
  key: string;
  label: string;
  data: { x: number; y: number | null }[];
}

function observations(sample: SubscriptionUsageSample): Observation[] {
  if (sample.provider.status !== 'ok') return [];
  return [
    ...sample.provider.windows.map(window => ({ key: `window:${window.name}`, label: window.name, percent: window.usedPercent, reset: window.resetsAt })),
    ...(sample.provider.budgets ?? []).map(budget => ({
      key: `budget:${budget.id ?? budget.name}`,
      label: [budget.name, budget.description].filter(Boolean).join(' · '),
      percent: budget.limitUSD > 0 ? budget.spentUSD / budget.limitUSD * 100 : 100,
      reset: budget.resetsAt,
    })),
  ];
}

// Plot observations, never cumulative usage estimates. Break a line at quota
// resets, plan changes and missing probes, including periods Ocman was stopped.
export function subscriptionHistorySeries(history: SubscriptionUsageHistory) {
  const groups = new Map<string, SubscriptionUsageSample[]>();
  for (const sample of history.samples) {
    const samples = groups.get(sample.provider.id) ?? [];
    samples.push(sample);
    groups.set(sample.provider.id, samples);
  }
  return [...groups].map(([id, unsorted]) => {
    const samples = [...unsorted].sort((a, b) => a.sampledAt - b.sampledAt);
    const definitions = new Map<string, Observation>();
    for (const sample of samples) for (const metric of observations(sample)) definitions.set(metric.key, metric);
    const series: SubscriptionHistorySeries[] = [...definitions.values()].map(metric => {
      let previous: { at: number; interval: number; reset?: string; plan?: string } | undefined;
      const data: SubscriptionHistorySeries['data'] = [];
      for (const sample of samples) {
        const value = observations(sample).find(item => item.key === metric.key);
        const interval = sample.sampleIntervalMs ?? history.sampleIntervalMs;
        if (previous && value && (sample.sampledAt - previous.at > Math.max(previous.interval, interval) * 2 || previous.reset !== value.reset || previous.plan !== sample.provider.plan)) {
          data.push({ x: sample.sampledAt - 1, y: null });
        }
        data.push({ x: sample.sampledAt, y: value ? Math.max(0, Math.min(100, value.percent)) : null });
        previous = value ? { at: sample.sampledAt, interval, reset: value.reset, plan: sample.provider.plan } : undefined;
      }
      return { key: metric.key, label: metric.label, data };
    });
    const latest = samples[samples.length - 1].provider;
    return { id, name: latest.name, platformName: latest.platformName, machineName: latest.machineName, series };
  }).filter(group => group.series.length > 0);
}
