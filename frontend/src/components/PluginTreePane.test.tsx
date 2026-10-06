// @vitest-environment jsdom

import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { PluginTreePane } from './PluginTreePane';

it('renders parent-child tickets as a plain-text tree and retries errors', async () => {
  const refresh = vi.fn();
  render(
    <PluginTreePane
      label="Items"
      status={{
        available: true,
        warning: true,
        nodes: [
          { id: 'parent', title: '<b>Parent</b>', status: 'open', badge: 'P1', kind: 'epic' },
          { id: 'child', title: 'Child', status: 'blocked', badge: 'P2', kind: 'task', parentId: 'parent' },
        ],
      }}
      loading={false}
      error={null}
      refresh={refresh}
    />,
  );

  const tree = screen.getByRole('list', { name: 'Items tree' });
  expect(tree.querySelector('b')).toBeNull();
  const parent = screen.getByText('<b>Parent</b>').closest('li');
  expect(parent).not.toBeNull();
  expect(within(parent!).getByText('child')).toBeInTheDocument();
  expect(screen.getByLabelText('open')).toHaveClass('open');
  expect(screen.getByLabelText('blocked')).toHaveClass('blocked');
  expect(screen.getByLabelText('open').parentElement?.parentElement?.style.getPropertyValue('--oc-plugin-tree-marker-width')).toBe('17px');
  expect(screen.getByLabelText('blocked').parentElement?.parentElement?.style.getPropertyValue('--oc-plugin-tree-marker-width')).toBe('29px');
  expect(screen.getByText('[epic]')).toBeInTheDocument();
  expect(screen.getByText('[task]')).toBeInTheDocument();
  expect(screen.getByText('<b>Parent</b>').parentElement).toBe(screen.getByText('P1').parentElement);
  expect(screen.getByText('parent').parentElement).toBe(screen.getByText('[epic]').parentElement);
  expect(screen.getByText('parent').parentElement).not.toBe(screen.getByText('P1').parentElement);
  expect(screen.getByRole('alert')).toHaveTextContent('Could not refresh Items');
  await userEvent.click(screen.getByRole('button', { name: 'Retry Items' }));
  expect(refresh).toHaveBeenCalledOnce();
});
