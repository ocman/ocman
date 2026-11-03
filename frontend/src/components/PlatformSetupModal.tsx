import { useRef, useState } from 'react';
import { checkPlatformSetup, installPlatformSetup } from '../lib/platformSetup';
import type { PlatformOption, PlatformSetup } from '../lib/platformSetup';
import { Button } from './Control';
import { CopyButton } from './CopyButton';
import { InlineAlert } from './InlineAlert';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';

export function PlatformSetupModal({ platform, initial, initialError, onClose }: {
  platform: PlatformOption;
  initial: PlatformSetup | null;
  initialError: string;
  onClose: (ready: boolean) => void;
}) {
  const [status, setStatus] = useState(initial);
  const [error, setError] = useState(initialError);
  const [busy, setBusy] = useState('');
  const active = useRef(false);
  const { id, label } = platform;
  async function run(action: 'check' | 'install' | 'close') {
    if (active.current) return;
    active.current = true;
    setBusy(action);
    setError('');
    try {
      const value = action === 'install'
        ? await installPlatformSetup(id)
        : await checkPlatformSetup(id);
      setStatus(value);
      if (action === 'close') onClose(value.ready === true);
    } catch (err) {
      setStatus(null);
      setError(err instanceof Error ? err.message : `Could not check ${label} dependencies`);
      if (action === 'close') onClose(false);
    } finally {
      active.current = false;
      setBusy('');
    }
  }
  const close = () => { void run('close'); };
  const command = status?.command;
  return <Modal label={`Set up ${label}`} onClose={close} canClose={!busy}>
    <ModalHeader title={`Set up ${label}`} onClose={close} closeLabel={`Close ${label} setup`} canClose={!busy} />
    <div className="oc-modal-body">
      {status?.notes?.map((note) => <p key={note}>{note}</p>)}
      {command && <p><code style={{ overflowWrap: 'anywhere' }}>{command}</code> <CopyButton text={command} label="Copy install command" size="small" /></p>}
      {status && <ul>{status.checks.map(check => <li key={check.id}>
        {check.label}: {check.ok ? 'Available' : check.detail}{!check.ok && check.hint && <p>{check.hint}</p>}
      </li>)}</ul>}
      {status?.ready && <p role="status">Dependencies are available. Close to enable {label}.</p>}
      {error && <InlineAlert>{error}</InlineAlert>}
    </div>
    <ModalFooter label={`${label} setup actions`}>
      <Button disabled={!!busy || !status?.installable || status.ready} onClick={() => { void run('install'); }}>{busy === 'install' ? 'Installing…' : 'Install'}</Button>
      <Button disabled={!!busy} onClick={() => { void run('check'); }}>{busy === 'check' ? 'Checking…' : 'Check again'}</Button>
      <Button disabled={!!busy} onClick={close}>{busy === 'close' ? 'Checking…' : 'Close'}</Button>
    </ModalFooter>
  </Modal>;
}
