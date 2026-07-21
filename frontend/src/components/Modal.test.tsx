// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
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
