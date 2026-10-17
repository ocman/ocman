import { useLayoutEffect, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { usePhoneOverlay } from '../lib/usePhoneOverlay';
import { IconButton } from './IconButton';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import './PhoneTerminalAccess.css';

/** The phone terminal is launched from session details, never the chat footer. */
export function PhoneTerminalAccess({ children }: { children: ReactNode }) {
  const [overlay, setOverlay] = usePhoneOverlay<'terminal'>('session-terminal');
  const [target, setTarget] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const syncTarget = () => setTarget(document.getElementById('phone-terminal-slot'));
    syncTarget();
    // Opening or collapsing details can replace the strip's DOM node.
    const observer = new MutationObserver(syncTarget);
    observer.observe(document.body, { childList: true, subtree: true });
    return () => observer.disconnect();
  }, []);
  return <>
    {target && createPortal(<IconButton label="Open terminal" icon="bi-terminal" variant="ghost"
      className="phone-terminal-launcher" onClick={() => setOverlay('terminal')} />, target)}
    {overlay === 'terminal' && createPortal(<div className="phone-terminal-view">
      <Modal label="Terminal" onClose={() => setOverlay(null)}>
        <ModalHeader title="Terminal" closeLabel="Close terminal view" onClose={() => setOverlay(null)} />
        {children}
      </Modal>
    </div>, document.body)}
  </>;
}
