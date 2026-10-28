// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it } from 'vitest';
import { ConfirmProvider } from './ConfirmDialog';
import { useConfirm, type ConfirmOptions } from '../lib/useConfirm';

function Harness({ options, onResult }: { options: ConfirmOptions; onResult: (ok: boolean) => void }) {
  const confirm = useConfirm();
  return <button onClick={() => void confirm(options).then(onResult)}>Ask</button>;
}

function mount(options: ConfirmOptions) {
  const results: boolean[] = [];
  render(<ConfirmProvider><Harness options={options} onResult={(ok) => results.push(ok)} /></ConfirmProvider>);
  fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
  return results;
}

it('names the item and action and focuses Cancel for destructive actions', async () => {
  const results = mount({ title: 'Delete item Desk lamp?', description: 'This cannot be undone.', confirmLabel: 'Delete item', destructive: true });
  const dialog = screen.getByRole('dialog', { name: 'Delete item Desk lamp?' });
  expect(dialog).toHaveTextContent('This cannot be undone.');
  expect(screen.getByRole('button', { name: 'Cancel' })).toHaveFocus();
  expect(screen.getByRole('button', { name: 'Delete item' })).toHaveClass('oc-button--danger');
  fireEvent.click(screen.getByRole('button', { name: 'Delete item' }));
  await waitFor(() => expect(results).toEqual([true]));
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('focuses the action for non-destructive confirmations and resolves false on Cancel or Escape', async () => {
  const results = mount({ title: 'Publish item?', confirmLabel: 'Publish', cancelLabel: 'Not now' });
  expect(screen.getByRole('button', { name: 'Publish' })).toHaveFocus();
  fireEvent.click(screen.getByRole('button', { name: 'Not now' }));
  await waitFor(() => expect(results).toEqual([false]));
  fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
  fireEvent.keyDown(window, { key: 'Escape' });
  await waitFor(() => expect(results).toEqual([false, false]));
});

it('cancels an unanswered confirmation when a newer one opens', async () => {
  const results = mount({ title: 'First?', confirmLabel: 'First' });
  fireEvent.click(screen.getByRole('button', { name: 'Ask' }));
  await waitFor(() => expect(results).toEqual([false]));
  expect(screen.getAllByRole('dialog')).toHaveLength(1);
});

it('requires a provider', () => {
  function Orphan() { useConfirm(); return null; }
  expect(() => render(<Orphan />)).toThrow(/ConfirmProvider/);
});
