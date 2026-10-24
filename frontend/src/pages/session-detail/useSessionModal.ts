import { useState } from 'react';

export type SessionModal = 'share' | 'rename' | 'fork' | 'jump' | 'move' | 'movePath';

export interface UseSessionModalResult {
  openModal: SessionModal | null;
  open: (modal: SessionModal) => void;
  close: () => void;
}

/** At most one session dialog is open at a time; this is that slot. */
export function useSessionModal(): UseSessionModalResult {
  const [openModal, setOpenModal] = useState<SessionModal | null>(null);
  const open = (modal: SessionModal) => setOpenModal(modal);
  const close = () => setOpenModal(null);
  return { openModal, open, close };
}
