import { createContext, useContext, type ReactNode } from 'react';

export interface ConfirmOptions {
  /** Names the item, e.g. "Delete routine Nightly build?". */
  title: string;
  /** Known consequences. */
  description?: ReactNode;
  /** Names the action, e.g. "Delete routine", never "OK". */
  confirmLabel: string;
  cancelLabel?: string;
  /** Destructive actions use danger styling and initially focus Cancel. */
  destructive?: boolean;
}

export type Confirm = (options: ConfirmOptions) => Promise<boolean>;

export const ConfirmContext = createContext<Confirm | null>(null);

/** The shared app confirmation dialog (ConfirmProvider); use instead of window.confirm. */
export function useConfirm(): Confirm {
  const confirm = useContext(ConfirmContext);
  if (!confirm) throw new Error('useConfirm requires a ConfirmProvider');
  return confirm;
}
