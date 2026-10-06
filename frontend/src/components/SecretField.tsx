import { useId, useState, type ComponentPropsWithRef } from 'react';
import { TextField } from './Control';
import { IconButton } from './IconButton';
import styles from './SecretField.module.css';

type Props = Omit<ComponentPropsWithRef<'input'>, 'type'> & {
  allowReveal?: boolean;
  /** Blank keeps the stored secret; the owner omits that value from its save payload. */
  protect?: boolean;
  /** Explicit reset, separate from empty input. The owner stages clearing until save. */
  onReset?: () => void;
  resetPending?: boolean;
  wrapperClassName?: string;
};

export function SecretField({ disabled, allowReveal = true, protect, onReset, resetPending, wrapperClassName, className, placeholder, autoComplete = 'off', ...props }: Props) {
  const [shown, setShown] = useState(false);
  const statusId = useId();
  const label = props['aria-label'] || 'secret';
  const describedBy = [props['aria-describedby'], resetPending ? statusId : undefined].filter(Boolean).join(' ') || undefined;
  return (
    <span className={[styles.root, wrapperClassName].filter(Boolean).join(' ')}>
      <span className={styles.control}>
        <TextField {...props} className={[styles.input, className].filter(Boolean).join(' ')} disabled={disabled} type={allowReveal && shown ? 'text' : 'password'} autoComplete={autoComplete}
          placeholder={resetPending ? 'Will be cleared' : placeholder ?? (protect ? 'Leave blank to keep' : undefined)} aria-describedby={describedBy} data-actions={Number(allowReveal) + Number(Boolean(onReset))} />
        <span className={styles.actions}>
          {allowReveal && <IconButton icon={shown ? 'bi-eye-slash' : 'bi-eye'} label={`${shown ? 'Hide' : 'Show'} ${label}`} variant="ghost" disabled={disabled} aria-pressed={shown} onClick={() => setShown(!shown)} />}
          {onReset && <IconButton icon="bi-trash" label={`${resetPending ? 'Undo reset' : 'Reset'} ${label}`} variant={resetPending ? 'danger' : 'ghost'} disabled={disabled} aria-pressed={Boolean(resetPending)} onClick={() => { setShown(false); onReset(); }} />}
        </span>
      </span>
      {resetPending && <span className={styles.status} id={statusId} role="status">Will be cleared when saved.</span>}
    </span>
  );
}
