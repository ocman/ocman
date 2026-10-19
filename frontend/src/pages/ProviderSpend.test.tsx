// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { ProviderSpend } from './ProviderSpend';
import type { ProviderSpend as Spend } from '../lib/api.providerUsage';

const spend: Spend = {
  estimatedUSD: null, inputTokens: 0, outputTokens: 0, requests: 0, unpricedRequests: 0,
  periodStart: '2026-10-12T12:00:00Z', periodEnd: '2026-10-19T12:00:00Z',
};

describe('ProviderSpend', () => {
  it('does not present missing history as zero spend', () => {
    render(<ProviderSpend spend={spend} />);
    expect(screen.getByText('Unavailable')).toBeInTheDocument();
    expect(screen.getByText('No recorded requests yet.')).toBeInTheDocument();
    expect(screen.getByText(/earlier requests and other apps are not included/)).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'View account billing' })).toHaveAttribute('href', 'https://console.typesafe.ai');
  });

  it.each([
    [0, '$0.00'], [0.0042, '$0.0042'], [0.000000042, '<$0.000001'], [12.34, '$12.34'],
  ])('formats an estimate of %s without rounding tiny charges to zero', (estimatedUSD, expected) => {
    render(<ProviderSpend spend={{ ...spend, estimatedUSD, requests: 1, inputTokens: 100, outputTokens: 5 }} />);
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.getByText('1 request · 100 input tokens · 5 output tokens')).toBeInTheDocument();
  });

  it('labels multiple unpriced requests', () => {
    render(<ProviderSpend spend={{ ...spend, unpricedRequests: 2, requests: 2 }} />);
    expect(screen.getByText('2 requests could not be priced. Estimate excludes these requests.')).toBeInTheDocument();
  });
});
