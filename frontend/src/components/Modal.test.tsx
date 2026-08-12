// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Modal } from './Modal';

describe('Modal', () => {
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
