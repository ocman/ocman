import { useState } from 'react';
import { createPortal } from 'react-dom';
import { api, type Session } from '../lib/api';
import { snoozeDeadline, snoozeLabel } from '../lib/snooze';
import { useSnoozePreferences } from '../lib/snoozePreferences';
import { IconButton } from './IconButton';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from './DropdownMenu';
import { SnoozeDialog } from './SnoozeDialog';

export function SnoozeButton({ session, disabled = false, open, onOpenChange, onBusyChange }: {
  session: Session; disabled?: boolean; open?: boolean; onOpenChange?: (open: boolean) => void; onBusyChange?: (busy: boolean) => void;
}) {
  const [custom, setCustom] = useState(false);
  const choices = useSnoozePreferences((s) => s.choices);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  async function snooze(deadline: number) {
    setBusy(true);
    onBusyChange?.(true);
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
      onBusyChange?.(false);
    }
  }
  return <span onClick={(event) => event.stopPropagation()} onKeyDown={(event) => { if (event.key !== 'Escape') event.stopPropagation(); }}>
    <DropdownMenu open={open} onOpenChange={onOpenChange}>
      <DropdownMenuTrigger asChild>
        <IconButton icon="bi-clock" label="Snooze conversation" variant="ghost" disabled={disabled || busy} />
      </DropdownMenuTrigger>
      <DropdownMenuContent>
        {choices.map((choice, index) => <DropdownMenuItem key={index} onSelect={() => { void snooze(snoozeDeadline(choice)); }}>{snoozeLabel(choice)}</DropdownMenuItem>)}
        <DropdownMenuItem onSelect={() => { setError(''); setCustom(true); }}>Custom...</DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    {custom && createPortal(<SnoozeDialog busy={busy} error={error} onClose={() => setCustom(false)} onSnooze={(until) => { void snooze(until); }} />, document.body)}
  </span>;
}
