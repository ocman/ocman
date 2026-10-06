// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { expect, it } from 'vitest';
import { RoutineStateBadge } from './RoutineStateBadge';

it('shows the run state by default and preserves custom outcome links', () => {
  const { rerender } = render(<RoutineStateBadge state="expired" />);
  expect(screen.getByText('expired')).toHaveAttribute('data-state', 'expired');
  rerender(<RoutineStateBadge state="success"><a href="/session/run-1">Open completed run</a></RoutineStateBadge>);
  expect(screen.getByRole('link', { name: 'Open completed run' })).toHaveAttribute('href', '/session/run-1');
  expect(screen.queryByText('expired')).not.toBeInTheDocument();
});
