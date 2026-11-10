// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ProviderBudget } from './ProviderBudget';

describe('ProviderBudget', () => {
  it.each([
    [80, 'normal'], [80.1, 'warning'], [99.9, 'warning'],
    [100, 'danger'], [120, 'danger'],
  ])('accents budget usage at %s percent as %s', (spentUSD, level) => {
    render(<ProviderBudget budget={{ name: 'Budget', limitUSD: 100, spentUSD, remainingUSD: 100 - spentUSD }} />);
    expect(screen.getByRole('progressbar').closest('.subscription-window')).toHaveAttribute('data-usage-level', level);
  });

  it.each([
    { limitUSD: 0, spentUSD: 0, percent: 100 },
    { limitUSD: 10, spentUSD: 20, percent: 100 },
    { limitUSD: 10, spentUSD: -1, percent: 0 },
  ])('bounds usage for a $limitUSD limit and $spentUSD spend', ({ limitUSD, spentUSD, percent }) => {
    render(<ProviderBudget budget={{ name: 'Budget', limitUSD, spentUSD, remainingUSD: 0 }} />);
    expect(screen.getByRole('progressbar')).toHaveValue(percent);
    expect(screen.queryByText(/Resets/)).not.toBeInTheDocument();
  });

  it('keeps the budget scope and per-model meaning visible', () => {
    render(<ProviderBudget budget={{ name: 'Personal monthly budget', description: 'Anthropic. Per-model limit; usage shown for the most-used model.', limitUSD: 100, spentUSD: 30, remainingUSD: 70 }} />);
    expect(screen.getByText('Anthropic. Per-model limit; usage shown for the most-used model.')).toBeInTheDocument();
  });
});
