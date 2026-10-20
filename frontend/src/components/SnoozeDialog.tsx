import { useCallback, useState, type SubmitEvent } from 'react';
import { Button, TextField } from './Control';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import { ModalFooter } from './ModalFooter';
import styles from './SnoozeDialog.module.css';

export function SnoozeDialog({ busy, error, onClose, onSnooze }: {
  busy: boolean; error: string; onClose: () => void; onSnooze: (until: number) => void;
}) {
  const [date, setDate] = useState('');
  const [time, setTime] = useState('08:00');
  const [now, setNow] = useState(Date.now);
  const until = new Date(`${date}T${time}`).getTime();
  const valid = Number.isFinite(until) && until > now;
  const submit = useCallback((event: SubmitEvent<HTMLFormElement>) => {
    event.preventDefault();
    const current = Date.now();
    setNow(current);
    if (!busy && until > current) onSnooze(until);
  }, [busy, until, onSnooze]);
  return <Modal label="Snooze conversation" onClose={onClose} canClose={!busy}>
    <ModalHeader title="Snooze conversation" closeLabel="Close snooze dialog" onClose={onClose} canClose={!busy} />
    <form className={styles.form} onSubmit={submit}>
      <p className={styles.description}>Hide this conversation until a time that works for you.</p>
      <div className={styles.fields}>
        <label className={styles.field}>Date<TextField type="date" aria-label="Wake-up date" data-autofocus value={date} onChange={(event) => { setDate(event.target.value); setNow(Date.now()); }} disabled={busy} required /></label>
        <label className={styles.field}>Time<TextField type="time" aria-label="Wake-up time" value={time} onChange={(event) => { setTime(event.target.value); setNow(Date.now()); }} disabled={busy} required /></label>
      </div>
      <p className={styles.description} role="status">{valid ? `Wakes ${new Date(until).toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' })}` : date ? 'Choose a date and time in the future.' : 'Times use your local timezone.'}</p>
      {error && <p className={styles.error} role="alert">{error}</p>}
      <ModalFooter label="Snooze actions">
        <Button type="button" onClick={onClose} disabled={busy}>Cancel</Button>
        <Button type="submit" variant="accent" disabled={busy || !valid}>{busy ? 'Snoozing...' : 'Snooze'}</Button>
      </ModalFooter>
    </form>
  </Modal>;
}
