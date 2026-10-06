// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { Drawer } from './Drawer';

it('renders outside its owner and uses modal focus and dismissal behavior', () => {
  const onClose = vi.fn();
  const { container, unmount } = render(<Drawer title="Edit remote" onClose={onClose}><input aria-label="Name" data-autofocus /></Drawer>);
  const dialog = screen.getByRole('dialog', { name: 'Edit remote' });
  expect(container).not.toContainElement(dialog);
  expect(container).toHaveAttribute('inert');
  expect(screen.getByRole('textbox', { name: 'Name' })).toHaveFocus();
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(onClose).toHaveBeenCalledOnce();
  fireEvent.click(screen.getByRole('button', { name: 'Close Edit remote' }));
  expect(onClose).toHaveBeenCalledTimes(2);
  unmount();
  expect(container).not.toHaveAttribute('inert');
});

it('blocks close controls and backdrop dismissal while busy', () => {
  const onClose = vi.fn();
  render(<Drawer title="Edit remote" canClose={false} onClose={onClose}>Saving</Drawer>);
  const dialog = screen.getByRole('dialog');
  expect(screen.getByRole('button', { name: 'Close Edit remote' })).toBeDisabled();
  fireEvent.keyDown(window, { key: 'Escape' });
  fireEvent.click(dialog.parentElement!);
  expect(onClose).not.toHaveBeenCalled();
});
