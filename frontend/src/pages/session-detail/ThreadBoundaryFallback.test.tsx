// @vitest-environment jsdom

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it, vi } from 'vitest';
import { ThreadBoundaryFallback } from './ThreadBoundaryFallback';

describe('ThreadBoundaryFallback', () => {
  it('auto-triggers a reload once for recoverable crashes', async () => {
    const onReload = vi.fn();

    const { rerender } = render(
      <ThreadBoundaryFallback
        error={new Error('useClientLookup: Index 1 out of bounds (length: 1)')}
        reset={vi.fn()}
        autoRecover
        onReload={onReload}
      />, 
    );

    await waitFor(() => {
      expect(onReload).toHaveBeenCalledTimes(1);
    });
    expect(screen.getByRole('alert')).toHaveTextContent('Recovering session thread');

    rerender(
      <ThreadBoundaryFallback
        error={new Error('useClientLookup: Index 1 out of bounds (length: 1)')}
        reset={vi.fn()}
        autoRecover
        onReload={onReload}
      />, 
    );

    expect(onReload).toHaveBeenCalledTimes(1);
  });

  it('shows manual recovery actions for non-auto-recoverable crashes', async () => {
    const user = userEvent.setup();
    const onReload = vi.fn();
    const reset = vi.fn();

    render(
      <ThreadBoundaryFallback
        error={new Error('boom')}
        reset={reset}
        autoRecover={false}
        onReload={onReload}
      />, 
    );

    await user.click(screen.getByRole('button', { name: 'Reload thread' }));
    await user.click(screen.getByRole('button', { name: 'Try again' }));

    expect(onReload).toHaveBeenCalledTimes(1);
    expect(reset).toHaveBeenCalledTimes(1);
  });
});
