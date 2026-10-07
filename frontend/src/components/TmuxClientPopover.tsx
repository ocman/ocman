import type { CSSProperties, RefObject } from 'react';
import type { TmuxClient } from '../lib/api';
import { shortPath } from '../lib/format';
import { Button } from './Control';
import styles from './TmuxClientPopover.module.css';

export interface TmuxClientPopoverProps {
  pickerRef: RefObject<HTMLDivElement | null>;
  pos: { top: number; left: number };
  clients: TmuxClient[];
  onSelect: (tty: string) => void;
}

/** Floating list of attached tmux clients to switch to. */
export function TmuxClientPopover({ pickerRef, pos, clients, onSelect }: TmuxClientPopoverProps) {
  return (
    <div
      ref={pickerRef}
      role="group"
      aria-label="Select tmux client"
      className={styles.root}
      style={{ top: pos.top, '--tmux-anchor-x': `${pos.left}px` } as CSSProperties}
    >
      <div className={styles.heading}>Select tmux client</div>
      <div className={styles.clients}>
        {clients.map((c) => (
          <Button
            key={c.tty}
            type="button"
            className={styles.client}
            onClick={() => onSelect(c.tty)}
          >
            <span className={styles.tty}>{c.tty}</span>
            <span className={styles.session}>{shortPath(c.session)}</span>
            <span className={styles.size}>{c.width}&times;{c.height}</span>
          </Button>
        ))}
      </div>
    </div>
  );
}
