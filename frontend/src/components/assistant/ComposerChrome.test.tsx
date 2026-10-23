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
