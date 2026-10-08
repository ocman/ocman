import type { ComponentProps } from 'react';
import { createPortal } from 'react-dom';
import * as Toast from '@radix-ui/react-toast';
import { ButtonGroup } from './Control';
import { IconButton } from './IconButton';
import styles from './PromptToast.module.css';

export function PromptToast(props: Omit<ComponentProps<typeof Toast.Root>, 'className'>) {
  return <Toast.Root {...props} className={styles.toast} />;
}

export function PromptToastHeading(props: Omit<ComponentProps<typeof Toast.Title>, 'className'>) {
  return <Toast.Title {...props} className={styles.heading} />;
}

export function PromptToastBody({ truncate = false, ...props }: Omit<ComponentProps<typeof Toast.Description>, 'className'> & { truncate?: boolean }) {
  return <Toast.Description {...props} className={`${styles.body}${truncate ? ` ${styles.truncate}` : ''}`} />;
}

export function PromptToastActions(props: Omit<ComponentProps<typeof ButtonGroup>, 'className'>) {
  return <ButtonGroup {...props} className={styles.actions} />;
}

export function PromptToastClose({ label = 'Dismiss' }: { label?: string }) {
  return <Toast.Close asChild><IconButton label={label} icon="bi-x-lg" variant="ghost" className={styles.close} /></Toast.Close>;
}

export function PromptToastViewport() {
  return createPortal(<Toast.Viewport className={styles.viewport} data-prompt-toast-viewport="" />, document.body);
}
