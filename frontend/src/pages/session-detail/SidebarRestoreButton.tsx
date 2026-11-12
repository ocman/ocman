import { useState } from 'react';
import { api, type Session } from '../../lib/api';
import { IconButton } from '../../components/IconButton';

export function SidebarRestoreButton({ session, disabled, onBusyChange }: {
  session: Session; disabled: boolean; onBusyChange: (busy: boolean) => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const label = session.snoozedUntil ? 'Unsnooze session' : 'Unarchive session';
  async function restore() {
    setBusy(true);
    onBusyChange(true);
    setError('');
    try {
      // Unarchive also cancels snooze. Owner-qualified SSE owns the state patch.
      await api.archiveSession(session.platform, session.id, session.timeUpdated, false);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not restore session');
    } finally {
      setBusy(false);
      onBusyChange(false);
    }
  }
  return <span onClick={event => event.stopPropagation()} onKeyDown={event => event.stopPropagation()}>
    <IconButton icon={session.snoozedUntil ? 'bi-sun' : 'bi-arrow-up-square'} label={label}
      variant="ghost" disabled={disabled || busy} onClick={() => { void restore(); }} />
    {error && <span role="alert">{error}</span>}
  </span>;
}
