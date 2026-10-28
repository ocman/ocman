import { useCallback, useRef, useState, type ReactNode } from 'react';
import { ConfirmContext, type Confirm, type ConfirmOptions } from '../lib/useConfirm';
import { Button } from './Control';
import { Modal } from './Modal';
import { ModalFooter } from './ModalFooter';
import { ModalHeader } from './ModalHeader';

type Pending = ConfirmOptions & { settle: (confirmed: boolean) => void };

export function ConfirmProvider({ children }: { children: ReactNode }) {
  const [pending, setPending] = useState<Pending | null>(null);
  const current = useRef<Pending | null>(null);
  const confirm = useCallback<Confirm>((options) => new Promise<boolean>((resolve) => {
    // A newer request supersedes an unanswered one, which counts as cancelled.
    current.current?.settle(false);
    const next: Pending = { ...options, settle: (confirmed) => {
      if (current.current !== next) return;
      current.current = null;
      setPending(null);
      resolve(confirmed);
    } };
    current.current = next;
    setPending(next);
  }), []);
  return <ConfirmContext.Provider value={confirm}>
    {children}
    {pending && <Modal label={pending.title} onClose={() => pending.settle(false)}>
      <ModalHeader title={pending.title} description={pending.description} onClose={() => pending.settle(false)} closeLabel="Close" />
      <ModalFooter label="Confirmation actions">
        <Button autoFocus={pending.destructive} onClick={() => pending.settle(false)}>{pending.cancelLabel ?? 'Cancel'}</Button>
        <Button autoFocus={!pending.destructive} variant={pending.destructive ? 'danger' : 'accent'} onClick={() => pending.settle(true)}>{pending.confirmLabel}</Button>
      </ModalFooter>
    </Modal>}
  </ConfirmContext.Provider>;
}
