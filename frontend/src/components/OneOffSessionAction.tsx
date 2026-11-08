import { useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { postJSON } from '../lib/api.requests';
import { newSessionPath } from '../lib/newSessionPath';
import { useShortcut } from '../lib/shortcutRegistry';
import { Button } from './Control';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';

function OneOffSessionShortcut({ start }: { start: () => Promise<void> }) {
  useShortcut({
    id: 'site.one-off-session', scope: 'site',
    keys: { code: 'KeyO', alt: true }, description: 'New one-off session',
    runInEditable: true, handler: () => { void start(); },
  });
  return null;
}

export function OneOffSessionAction({ onNavigate, registerShortcut = true, shortcutOnly = false }: { onNavigate?: () => void; registerShortcut?: boolean; shortcutOnly?: boolean }) {
  const navigate = useNavigate();
  const [search] = useSearchParams();
  const remoteId = search.get('remoteId') || 'local';
  const locked = useRef(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState('');

  async function start() {
    if (locked.current) return;
    locked.current = true;
    setPending(true);
    setError('');
    try {
      const { directory } = await postJSON<{ directory: string }>('/api/sessions/one-off', { remoteId });
      navigate(newSessionPath({ directory, remoteId }));
      onNavigate?.();
    } catch (failure) {
      setError(failure instanceof Error ? failure.message : String(failure));
    } finally {
      locked.current = false;
      setPending(false);
    }
  }

  return <>
    {registerShortcut && <OneOffSessionShortcut start={start} />}
    {!shortcutOnly && <Button variant="ghost" className="main-nav-action" title="New session in a temporary folder (Option+O / Alt+O)"
      aria-label="New one-off session" aria-busy={pending} disabled={pending} onClick={() => { void start(); }}>
      <i className={`bi ${pending ? 'bi-hourglass-split' : 'bi-plus-square'}`} aria-hidden="true" />
      <span>{pending ? 'Creating workspace…' : 'One-off session'}</span>
    </Button>}
    {error && createPortal(<Modal label="Could not create one-off session" onClose={() => setError('')}>
      <ModalHeader title="Could not create one-off session" closeLabel="Close error" onClose={() => setError('')} />
      <p role="alert">{error}</p>
      <ModalFooter label="One-off session recovery">
        <Button onClick={() => setError('')}>Cancel</Button>
        <Button variant="accent" onClick={() => { void start(); }}>Retry</Button>
      </ModalFooter>
    </Modal>, document.body)}
  </>;
}
