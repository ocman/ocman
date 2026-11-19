// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { ComposerFooter } from './ComposerChrome';

const tokenStats = { input: 1, output: 1, reasoning: 0, cacheRead: 0, cacheWrite: 0, totalCost: 0.5 };

it('renders the footer status before the session cost', () => {
  render(<ComposerFooter isRunning={false} effectiveAgent="" effectiveModel="" visibleDurationMs={0}
    tokenStats={tokenStats} status={<button type="button">tasks</button>} />);
  const status = screen.getByRole('button', { name: 'tasks' });
  const cost = screen.getByRole('button', { name: '$0.50' });
  expect(status.compareDocumentPosition(cost) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(status.closest('.oc-composer-footer-right')).toBe(cost.closest('.oc-composer-footer-right'));
});

it('renders the footer status without token stats', () => {
  render(<ComposerFooter isRunning={false} effectiveAgent="" effectiveModel="" visibleDurationMs={0}
    status={<button type="button">tasks</button>} />);
  expect(screen.getByRole('button', { name: 'tasks' }).closest('.oc-composer-footer-right')).not.toBeNull();
});

it.each([
  { tokens: 25_600, name: '25.6K (20%)', tone: null },
  { tokens: 25_601, name: '25.6K (20%)', tone: 'oc-context-warn' },
  { tokens: 51_200, name: '51.2K (40%)', tone: 'oc-context-warn' },
  { tokens: 51_201, name: '51.2K (40%)', tone: 'oc-context-danger' },
])('applies the context color for $tokens tokens', ({ tokens, name, tone }) => {
  render(<ComposerFooter isRunning={false} effectiveAgent="" effectiveModel="openai/gpt-4o" visibleDurationMs={0}
    tokenStats={tokenStats} contextTokens={tokens} />);
  const indicator = screen.getByRole('button', { name });
  expect(indicator.classList.contains('oc-context-warn')).toBe(tone === 'oc-context-warn');
  expect(indicator.classList.contains('oc-context-danger')).toBe(tone === 'oc-context-danger');
});

it.each(['unknown-model', 'anthropic/claude-opus-4-8'])('keeps context usage neutral without a known limit for %s', (model) => {
  render(<ComposerFooter isRunning={false} effectiveAgent="" effectiveModel={model} visibleDurationMs={0}
    tokenStats={tokenStats} contextTokens={150_000} />);
  const indicator = screen.getByRole('button', { name: '150.0K' });
  expect(indicator).not.toHaveClass('oc-context-warn');
  expect(indicator).not.toHaveClass('oc-context-danger');
});
