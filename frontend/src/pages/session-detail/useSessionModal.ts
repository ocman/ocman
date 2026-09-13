import { useCallback, useMemo, useState } from 'react';

export type SessionModal = 'share' | 'rename' | 'fork' | 'jump' | 'move' | 'movePath';
const MODALS: SessionModal[] = ['share', 'rename', 'fork', 'jump', 'move', 'movePath'];

export interface UseSessionModalResult {
  openModal: SessionModal | null;
  open: (modal: SessionModal) => void;
  close: () => void;
  /** Boolean-setter adapter for hooks that still speak `setShowX(true)`. Stable per modal. */
  setterFor: (modal: SessionModal) => (show: boolean) => void;
}

/** At most one session dialog is open at a time; this is that slot. */
export function useSessionModal(): UseSessionModalResult {
  const [openModal, setOpenModal] = useState<SessionModal | null>(null);
  const open = useCallback((modal: SessionModal) => setOpenModal(modal), []);
  const close = useCallback(() => setOpenModal(null), []);
  const setters = useMemo(() => new Map(MODALS.map((modal) => [
    modal,
    (show: boolean) => setOpenModal((current) => (show ? modal : current === modal ? null : current)),
  ] as const)), []);
  const setterFor = useCallback((modal: SessionModal) => setters.get(modal)!, [setters]);
  return { openModal, open, close, setterFor };
}
