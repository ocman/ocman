// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { ReasoningPicker } from './ReasoningPicker';

describe('ReasoningPicker', () => {
  // jsdom lacks scrollIntoView, which the picker calls when the
  // selection moves.
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  function renderPicker(overrides: Partial<React.ComponentProps<typeof ReasoningPicker>> = {}) {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(
      <ReasoningPicker
        open
        options={['low', 'high']}
        current="low"
        onSelect={onSelect}
        onClose={onClose}
        {...overrides}
      />,
    );
    return { onSelect, onClose };
  }

  it('renders as a dialog whose options are buttons', () => {
    renderPicker();

    expect(screen.getByRole('dialog', { name: 'Reasoning level' })).toBeInTheDocument();
    expect(screen.getAllByRole('button').map((b) => b.textContent)).toEqual([
      'default',
      'low',
      'high',
    ]);
  });

  it('focuses the current option and picks with the keyboard', async () => {
    const user = userEvent.setup();
    const { onSelect, onClose } = renderPicker();

    expect(screen.getByRole('button', { name: 'low' })).toHaveFocus();

    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('button', { name: 'high' })).toHaveFocus();

    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledWith('high');
    expect(onClose).toHaveBeenCalled();
  });

  it('activates a focused option with Space', async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPicker();

    await user.keyboard('{ArrowUp}');
    expect(screen.getByRole('button', { name: 'default' })).toHaveFocus();

    await user.keyboard(' ');
    expect(onSelect).toHaveBeenCalledWith('');
  });
});
