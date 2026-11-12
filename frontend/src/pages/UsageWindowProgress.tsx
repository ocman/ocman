import { useEffect, useState } from 'react';
import type { SubscriptionUsageWindow } from '../lib/api';

// Providers include the duration in quota labels, sometimes beside a model name.
// Do not guess a period for labels such as "Rolling" or "Monthly".
function periodMilliseconds(name: string): number | undefined {
  const duration = name.split(' · ').map(part => /^(\d+) (minute|hour|day)s?$/i.exec(part)).find(Boolean);
  if (!duration) return undefined;
  const units: Record<string, number> = { minute: 60_000, hour: 3_600_000, day: 86_400_000 };
  const milliseconds = Number(duration[1]) * units[duration[2].toLowerCase()];
  return Number.isFinite(milliseconds) && milliseconds > 0 ? milliseconds : undefined;
}

export function UsageWindowProgress({ label, window }: { label: string; window: SubscriptionUsageWindow }) {
  const [now, setNow] = useState(() => Date.now());
  const period = periodMilliseconds(window.name);
  const reset = window.resetsAt ? Date.parse(window.resetsAt) : NaN;

  useEffect(() => {
    if (!period || !Number.isFinite(reset)) return;
    const timer = setInterval(() => setNow(Date.now()), 60_000);
    return () => clearInterval(timer);
  }, [period, reset]);

  const elapsed = period && reset > now ? Math.max(0, 100 * (1 - (reset - now) / period)) : undefined;
  const comparison = elapsed === window.usedPercent ? 'in line with' : window.usedPercent > (elapsed ?? 0) ? 'above' : 'below';
  const description = `${Math.round(elapsed ?? 0)}% of period elapsed. Usage is ${comparison} elapsed time.`;

  return (
    <div className="subscription-progress">
      <progress aria-label={label} max={100} value={window.usedPercent} />
      {elapsed !== undefined && <span
        className="subscription-progress-dot"
        role="img"
        aria-label={description}
        title={description}
        style={{ left: `${elapsed}%` }}
      />}
    </div>
  );
}
