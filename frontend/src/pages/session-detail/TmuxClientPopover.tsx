import type React from 'react';
import type { TmuxState } from '../../lib/useTmux';
import { shortPath } from '../../lib/format';

export interface TmuxClientPopoverProps {
  pickerRef: React.RefObject<HTMLDivElement | null>;
  pos: { top: number; left: number };
  clients: TmuxState['clients'];
  onSelect: (tty: string) => void;
}

/** Floating list of attached tmux clients to switch to. */
export function TmuxClientPopover({ pickerRef, pos, clients, onSelect }: TmuxClientPopoverProps) {
  return (
    <div
      ref={pickerRef}
      className="tmux-client-popover"
      style={{ top: pos.top, left: pos.left }}
    >
      <div className="tmux-client-picker-header">
        <span>Select tmux client</span>
      </div>
      {clients.map((c) => (
        <div
          key={c.tty}
          className="tmux-client-picker-item"
          onClick={() => onSelect(c.tty)}
        >
          <span className="tmux-client-tty">{c.tty}</span>
          <span className="tmux-client-session">{shortPath(c.session)}</span>
          <span className="tmux-client-size">{c.width}&times;{c.height}</span>
        </div>
      ))}
    </div>
  );
}
