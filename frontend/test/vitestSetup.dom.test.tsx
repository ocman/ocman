// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { getConfig, render, screen } from '@testing-library/react';

it('preserves DOM matchers and the shared async timeout', () => {
  expect(getConfig().asyncUtilTimeout).toBe(5000);
  render(<div>Setup cleanup probe</div>);
  expect(screen.getByText('Setup cleanup probe')).toBeInTheDocument();
});

it('cleans up the previous test render', () => {
  expect(screen.queryByText('Setup cleanup probe')).not.toBeInTheDocument();
});
