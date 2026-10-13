import { useState } from 'react';
import { createPortal } from 'react-dom';
import { api, type Session } from '../lib/api';
import { snoozeDeadline, SNOOZE_OPTIONS } from '../lib/snooze';
import { Button, TextField } from './Control';
import { IconButton } from './IconButton';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './DropdownMenu';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';

export function SnoozeButton({ session, disabled = false }: { session: Session; disabled?: boolean }) {
  const [custom, setCustom] = useState(false);
  const [value, setValue] = useState('');
  const [valid, setValid] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const until = new Date(value).getTime();
  async function snooze(deadline: number) {
    setBusy(true);
    setError('');
    try {
      await api.snoozeSession(session.platform, session.id, deadline);
      setCustom(false);
      // Owner-qualified SSE updates own archive state and active-session navigation.
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not snooze conversation');
      setCustom(true);
    } finally {
      setBusy(false);
    }
  }
  return <span onClick={(event) => event.stopPropagation()} onKeyDown={(event) => { if (event.key !== 'Escape') event.stopPropagation(); }}>
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <IconButton icon="bi-clock" label="Snooze conversation" variant="ghost" disabled={disabled || busy} />
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {SNOOZE_OPTIONS.map((label, index) => <DropdownMenuItem key={label} onSelect={() => { void snooze(snoozeDeadline(index)); }}>{label}</DropdownMenuItem>)}
        <DropdownMenuItem onSelect={() => { setError(''); setCustom(true); }}>Custom...</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    {custom && createPortal(<Modal label="Snooze conversation" onClose={() => setCustom(false)} canClose={!busy}>
      <ModalHeader title="Snooze conversation" closeLabel="Close snooze dialog" onClose={() => setCustom(false)} canClose={!busy} />
      <label>Wake up at<TextField type="datetime-local" aria-label="Wake up at" value={value} onChange={(event) => { setValue(event.target.value); setValid(new Date(event.target.value).getTime() > Date.now()); }} disabled={busy} /></label>
      {error && <p role="alert">{error}</p>}
      <ModalFooter label="Snooze actions">
        <Button onClick={() => setCustom(false)} disabled={busy}>Cancel</Button>
        <Button variant="accent" disabled={busy || !valid} onClick={() => { void snooze(until); }}>{busy ? 'Snoozing...' : 'Snooze'}</Button>
      </ModalFooter>
    </Modal>, document.body)}
  </span>;
}
