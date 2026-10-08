// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Modal } from './Modal';

describe('Modal', () => {
  it('dismisses only the focused nested drawer on Escape', async () => {
    const user = userEvent.setup();
    const closeGraph = vi.fn();
    const closeDrawer = vi.fn();
    const { rerender } = render(<Modal label="Graph" onClose={closeGraph}><button>Graph control</button></Modal>);
    rerender(<Modal label="Graph" onClose={closeGraph}><button>Graph control</button><Modal label="Drawer" onClose={closeDrawer}><button>Drawer control</button></Modal></Modal>);
    await user.keyboard('{Escape}');
    expect(closeDrawer).toHaveBeenCalledOnce();
    expect(closeGraph).not.toHaveBeenCalled();
  });

  it('provides dialog semantics and centralizes dismissal', () => {
    const onClose = vi.fn();
    const { rerender } = render(
      <Modal label="Example" onClose={onClose} backdropClassName="backdrop" dialogClassName="dialog">
        content
      </Modal>,
    );

    fireEvent.click(screen.getByRole('dialog', { name: 'Example' }));
    expect(onClose).not.toHaveBeenCalled();
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.click(document.querySelector('.backdrop')!);
    expect(onClose).toHaveBeenCalledTimes(2);

    rerender(
      <Modal canClose={false} label="Example" onClose={onClose} backdropClassName="backdrop" dialogClassName="dialog">
        content
      </Modal>,
    );
    fireEvent.keyDown(window, { key: 'Escape' });
    fireEvent.click(document.querySelector('.backdrop')!);
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});

function Harness({ children }: { children?: React.ReactNode }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)}>Open</button>
      <div data-testid="background">
        <button type="button">Background</button>
      </div>
      {open && (
        <Modal
          label="Example"
          onClose={() => setOpen(false)}
          backdropClassName="backdrop"
          dialogClassName="dialog"
        >
          {children}
        </Modal>
      )}
    </>
  );
}

describe('Modal focus management', () => {
  it('skips controls disabled by a fieldset and cycles to an expandable-group summary', () => {
    render(<Modal label="Saving" onClose={() => {}}><fieldset disabled><input aria-label="Disabled name" data-autofocus /></fieldset><details><summary>Options</summary></details><button type="button">Cancel</button></Modal>);
    const summary = screen.getByText('Options');
    expect(summary).toHaveFocus();
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    cancel.focus();
    fireEvent.keyDown(cancel, { key: 'Tab' });
    expect(summary).toHaveFocus();
  });

  it('keeps notifications interactive above the modal', async () => {
    const user = userEvent.setup();
    render(<><div><div data-prompt-toast-viewport=""><button type="button">Dismiss notification</button></div></div><Harness /></>);

    await user.click(screen.getByRole('button', { name: 'Open' }));

    expect(screen.getByRole('button', { name: 'Dismiss notification' }).closest('[inert]')).toBeNull();
  });

  it('focuses its content, keeps Tab inside and restores focus on close', async () => {
    const user = userEvent.setup();
    render(
      <Harness>
        <button type="button">First</button>
        <button type="button">Last</button>
      </Harness>,
    );

    const opener = screen.getByRole('button', { name: 'Open' });
    await user.click(opener);

    const first = screen.getByRole('button', { name: 'First' });
    const last = screen.getByRole('button', { name: 'Last' });
    expect(first).toHaveFocus();

    // Background content is taken out of the tab order and the a11y tree
    // by the platform's own `inert`.
    expect(screen.getByTestId('background')).toHaveAttribute('inert');
    expect(opener).toHaveAttribute('inert');

    await user.tab();
    expect(last).toHaveFocus();
    // Tab past the last focusable cycles back into the dialog instead of
    // escaping into the page behind it.
    await user.tab();
    expect(first).toHaveFocus();
    await user.tab({ shift: true });
    expect(last).toHaveFocus();

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.queryByRole('dialog')).toBeNull();
    expect(screen.getByTestId('background')).not.toHaveAttribute('inert');
    expect(opener).toHaveFocus();
  });

  it('focuses the dialog itself when it holds nothing focusable', async () => {
    const user = userEvent.setup();
    render(<Harness>just text</Harness>);

    await user.click(screen.getByRole('button', { name: 'Open' }));
    expect(screen.getByRole('dialog', { name: 'Example' })).toHaveFocus();
  });

  it('leaves an already-focused child alone (autoFocus wins)', async () => {
    const user = userEvent.setup();
    render(
      <Harness>
        <button type="button">First</button>
        <input aria-label="Name" autoFocus />
      </Harness>,
    );

    await user.click(screen.getByRole('button', { name: 'Open' }));
    expect(screen.getByRole('textbox', { name: 'Name' })).toHaveFocus();
  });
});
