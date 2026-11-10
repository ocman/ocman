import type { ProviderBudget as Budget } from '../lib/api.providerUsage';
import { formatCurrency, formatSubscriptionResetDate } from '../lib/format';
import { providerUsageLevel } from '../lib/providerUsageLevel';

export function ProviderBudget({ budget }: { budget: Budget }) {
  const percent = budget.limitUSD > 0 ? Math.max(0, Math.min(100, budget.spentUSD / budget.limitUSD * 100)) : 100;
  return (
    <div className="subscription-window" data-usage-level={providerUsageLevel(percent)}>
      <div className="subscription-window-label">
        <strong>{budget.name}</strong>
        <span>{formatCurrency(budget.remainingUSD, 2)} remaining</span>
      </div>
      <progress aria-label={`${budget.name} budget usage`} max={100} value={percent} />
      {budget.description && <span className="subscription-status">{budget.description}</span>}
      <span className="subscription-status">{formatCurrency(budget.spentUSD, 2)} of {formatCurrency(budget.limitUSD, 2)} used</span>
      {budget.resetsAt && <span className="subscription-reset">Resets <time dateTime={budget.resetsAt}>{formatSubscriptionResetDate(budget.resetsAt)}</time></span>}
    </div>
  );
}
