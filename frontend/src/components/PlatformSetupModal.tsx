import { useRef, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { Button } from './Control';
import { CopyButton } from './CopyButton';
import { InlineAlert } from './InlineAlert';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';

export const codexSetupEndpoint = '/api/settings/platforms/codex/setup';
const installCommand = 'pnpm add -g @agentclientprotocol/codex-acp@2.5.0';
export type PlatformSetup = {
  ready: boolean;
  installable: boolean;
  checks: { id: string; label: string; ok: boolean; detail: string; hint?: string }[];
};

export function PlatformSetupModal({ initial, initialError, onClose }: {
  initial: PlatformSetup | null;
  initialError: string;
  onClose: (ready: boolean) => void;
}) {
  const [status, setStatus] = useState(initial);
  const [error, setError] = useState(initialError);
  const [busy, setBusy] = useState('');
  const active = useRef(false);
  async function run(action: 'check' | 'install' | 'close') {
    if (active.current) return;
    active.current = true;
    setBusy(action);
    setError('');
    try {
      const value = action === 'install'
        ? await postJSON<PlatformSetup>('/api/settings/platforms/codex/install', {})
        : await fetchJSON<PlatformSetup>(codexSetupEndpoint);
      setStatus(value);
      if (action === 'close') onClose(value.ready === true);
    } catch (err) {
      setStatus(null);
      setError(err instanceof Error ? err.message : 'Could not check Codex dependencies');
      if (action === 'close') onClose(false);
    } finally {
      active.current = false;
      setBusy('');
    }
  }
  const close = () => { void run('close'); };
  return <Modal label="Set up Codex" onClose={close} canClose={!busy}>
    <ModalHeader title="Set up Codex" onClose={close} closeLabel="Close Codex setup" canClose={!busy} />
    <div className="oc-modal-body">
      <p>Codex requires the Codex CLI, Node.js 22 or newer and the Codex ACP adapter on the machine running Ocman.</p>
      <p>Install runs this command as Ocman&apos;s user on that machine. It does not install Node.js, pnpm or the Codex CLI.</p>
      <p><code style={{ overflowWrap: 'anywhere' }}>{installCommand}</code> <CopyButton text={installCommand} label="Copy adapter install command" size="small" /></p>
      <p>If pnpm has no global bin directory, run <code>pnpm setup</code> and reopen your terminal. Ensure PNPM_HOME is on Ocman&apos;s PATH. Restart Ocman if its PATH changed.</p>
      <p>Sign in separately with <code>codex login</code>. This check verifies dependencies, not your login.</p>
      {status && <ul>{status.checks.map(check => <li key={check.id}>
        {check.label}: {check.ok ? 'Available' : check.detail}{!check.ok && check.hint && <p>{check.hint}</p>}
      </li>)}</ul>}
      {status?.ready && <p role="status">Dependencies are available. Close to enable Codex.</p>}
      {error && <InlineAlert>{error}</InlineAlert>}
    </div>
    <ModalFooter label="Codex setup actions">
      <Button disabled={!!busy || !status?.installable || status.ready} onClick={() => { void run('install'); }}>{busy === 'install' ? 'Installing…' : 'Install'}</Button>
      <Button disabled={!!busy} onClick={() => { void run('check'); }}>{busy === 'check' ? 'Checking…' : 'Check again'}</Button>
      <Button disabled={!!busy} onClick={close}>{busy === 'close' ? 'Checking…' : 'Close'}</Button>
    </ModalFooter>
  </Modal>;
}
