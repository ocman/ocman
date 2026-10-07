// @vitest-environment jsdom
import { useRef, useState } from 'react';
import { act, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { Popover } from './Popover';
import { SearchSelect } from './SearchSelect';

function Example({ onClose = () => {} }: { onClose?: () => void }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return <>
    <button ref={trigger} onClick={() => setOpen(!open)}>Open popover</button>
    <Popover id="example-popover" label="Example" open={open} triggerRef={trigger} onClose={() => { setOpen(false); onClose(); }}>
      <SearchSelect value="" options={[]} ariaLabel="Choice" placeholder="Pick" searchLabel="Search choices" onChange={() => {}} />
      <button>Inside</button>
    </Popover>
  </>;
}

it('portals the shared shell, focuses it, and leaves inside and trigger clicks to their controls', async () => {
  const onClose = vi.fn();
  render(<Example onClose={onClose} />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('button', { name: 'Open popover' }));
  const dialog = screen.getByRole('dialog', { name: 'Example' });
  expect(dialog.parentElement).toBe(document.body);
  expect(dialog).toHaveClass('oc-popover');
  expect(dialog).toHaveFocus();
  await userEvent.click(screen.getByRole('button', { name: 'Inside' }));
  expect(onClose).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole('button', { name: 'Open popover' }));
  expect(onClose).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('lets a nested picker handle the first Escape, then restores trigger focus on dismissal', async () => {
  render(<Example />);
  const trigger = screen.getByRole('button', { name: 'Open popover' });
  await userEvent.click(trigger);
  await userEvent.click(screen.getByRole('combobox', { name: 'Choice' }));
  await userEvent.keyboard('{Escape}');
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  await userEvent.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});

it('dismisses outside clicks and resize, and removes listeners when unmounted', async () => {
  const onClose = vi.fn();
  const view = render(<Example onClose={onClose} />);
  await userEvent.click(screen.getByRole('button', { name: 'Open popover' }));
  await userEvent.click(document.body);
  expect(onClose).toHaveBeenCalledTimes(1);
  await userEvent.click(screen.getByRole('button', { name: 'Open popover' }));
  act(() => window.dispatchEvent(new Event('resize')));
  expect(onClose).toHaveBeenCalledTimes(2);
  await userEvent.click(screen.getByRole('button', { name: 'Open popover' }));
  view.unmount();
  act(() => window.dispatchEvent(new Event('resize')));
  await userEvent.click(document.body);
  expect(onClose).toHaveBeenCalledTimes(2);
});
