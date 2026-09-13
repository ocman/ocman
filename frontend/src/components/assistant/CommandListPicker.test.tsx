// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeAll, describe, expect, it, vi } from 'vitest';
import { CommandListPicker } from './CommandListPicker';

interface Entry { value: string; label: string }

const entries: Entry[] = [
  { value: 'a', label: 'alpha' },
  { value: 'b', label: 'beta' },
  { value: 'c', label: 'gamma' },
];

describe('CommandListPicker', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  function renderPicker(overrides: Partial<React.ComponentProps<typeof CommandListPicker<Entry>>> = {}) {
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(
      <CommandListPicker<Entry>
        open
        entries={entries}
        fuseKeys={['label']}
        renderRow={(e) => <span>{e.label}</span>}
        placeholder={(n) => `Pick (${n})`}
        emptyMessage="Nothing"
        isCurrent={(e) => e.value === 'b'}
        onSelect={onSelect}
        onClose={onClose}
        {...overrides}
      />,
    );
    return { onSelect, onClose };
  }

  describe('searchable (default)', () => {
    it('starts at the first row and filters by query', async () => {
      const user = userEvent.setup();
      const { onSelect } = renderPicker();

      const input = screen.getByRole('combobox');
      expect(screen.getByRole('option', { name: 'alpha' })).toHaveAttribute('aria-selected', 'true');

      await user.type(input, 'gam');
      expect(screen.getAllByRole('option').map((o) => o.textContent)).toEqual(['gamma']);

      await user.keyboard('{Enter}');
      expect(onSelect).toHaveBeenCalledWith('c');
    });

    it('shows the empty message when nothing matches', async () => {
      const user = userEvent.setup();
      renderPicker();
      await user.type(screen.getByRole('combobox'), 'zzzz');
      expect(screen.getByText('Nothing')).toBeInTheDocument();
    });
  });

  describe('searchable={false}', () => {
    it('renders a title instead of an input and starts on the current row', async () => {
      const user = userEvent.setup();
      const { onSelect } = renderPicker({ searchable: false, dialogClassName: 'x-narrow' });

      expect(screen.queryByRole('combobox')).not.toBeInTheDocument();
      expect(screen.getByText('Pick (3)')).toBeInTheDocument();
      expect(screen.getByRole('dialog')).toHaveClass('x-narrow');
      expect(screen.getByRole('option', { name: 'beta' })).toHaveAttribute('aria-selected', 'true');

      await user.keyboard('{ArrowUp}{ArrowUp}{ArrowDown}');
      expect(screen.getByRole('option', { name: 'beta' })).toHaveAttribute('aria-selected', 'true');

      await user.keyboard('{Enter}');
      expect(onSelect).toHaveBeenCalledWith('b');
    });

    it('falls back to the first row when nothing is current', () => {
      renderPicker({ searchable: false, isCurrent: () => false });
      expect(screen.getByRole('option', { name: 'alpha' })).toHaveAttribute('aria-selected', 'true');
    });
  });
});
