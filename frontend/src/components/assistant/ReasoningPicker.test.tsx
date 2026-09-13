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

  it('renders a dialog with a default row and no search input', () => {
    renderPicker();

    expect(screen.getByRole('dialog', { name: 'Reasoning level' })).toBeInTheDocument();
    expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
    expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['default', 'low', 'high']);
  });

  it('renders nothing without options', () => {
    renderPicker({ options: [] });
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('highlights the current option and picks with the keyboard', async () => {
    const user = userEvent.setup();
    const { onSelect, onClose } = renderPicker();

    expect(screen.getByRole('option', { name: 'low' })).toHaveAttribute('aria-selected', 'true');

    await user.keyboard('{ArrowDown}');
    expect(screen.getByRole('option', { name: 'high' })).toHaveAttribute('aria-selected', 'true');

    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledWith('high');
    expect(onClose).toHaveBeenCalled();
  });

  it('selects the default row when no current value is set', async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPicker({ current: undefined });

    expect(screen.getByRole('option', { name: 'default' })).toHaveAttribute('aria-selected', 'true');
    await user.keyboard('{Enter}');
    expect(onSelect).toHaveBeenCalledWith('');
  });

  it('follows the mouse and picks on click', async () => {
    const user = userEvent.setup();
    const { onSelect } = renderPicker();

    await user.hover(screen.getByRole('option', { name: 'high' }));
    expect(screen.getByRole('option', { name: 'high' })).toHaveAttribute('aria-selected', 'true');

    await user.click(screen.getByRole('option', { name: 'default' }));
    expect(onSelect).toHaveBeenCalledWith('');
  });

  it('closes on Escape', async () => {
    const user = userEvent.setup();
    const { onClose } = renderPicker();
    await user.keyboard('{Escape}');
    expect(onClose).toHaveBeenCalled();
  });
});
