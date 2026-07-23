// @vitest-environment jsdom

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { BeadsPane } from './BeadsPane';

it('renders parent-child tickets as a plain-text tree and retries errors', async () => {
  const refresh = vi.fn();
  render(
    <BeadsPane
      status={{
        available: true,
        error: 'status_unavailable',
        tickets: [
          { id: 'bd-parent', title: '<b>Parent</b>', status: 'open', priority: 1, issueType: 'epic' },
          { id: 'bd-child', title: 'Child', status: 'blocked', priority: 2, issueType: 'task', parentId: 'bd-parent' },
        ],
      }}
      loading={false}
      error={null}
      refresh={refresh}
    />,
  );

  const tree = screen.getByRole('list', { name: 'Beads tickets' });
  expect(tree.querySelector('b')).toBeNull();
  const parent = screen.getByText('<b>Parent</b>').closest('li');
  expect(parent).not.toBeNull();
  expect(within(parent!).getByText('bd-child')).toBeInTheDocument();
  expect(screen.getByLabelText('open')).toHaveClass('open');
  expect(screen.getByLabelText('blocked')).toHaveClass('blocked');
  expect(screen.getByLabelText('open').parentElement?.parentElement?.style.getPropertyValue('--oc-beads-marker-width')).toBe('17px');
  expect(screen.getByLabelText('blocked').parentElement?.parentElement?.style.getPropertyValue('--oc-beads-marker-width')).toBe('29px');
  expect(screen.getByText('[epic]')).toBeInTheDocument();
  expect(screen.getByText('[task]')).toBeInTheDocument();
  expect(screen.getByText('<b>Parent</b>').parentElement).toBe(screen.getByText('P1').parentElement);
  expect(screen.getByText('bd-parent').parentElement).toBe(screen.getByText('[epic]').parentElement);
  expect(screen.getByText('bd-parent').parentElement).not.toBe(screen.getByText('P1').parentElement);
  expect(screen.getByRole('alert')).toHaveTextContent('Could not refresh Beads tickets');
  await userEvent.click(screen.getByRole('button', { name: 'Retry Beads tickets' }));
  expect(refresh).toHaveBeenCalledOnce();
});
