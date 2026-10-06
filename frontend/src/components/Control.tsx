import { useRef, useState } from 'react';
import type { AnchorHTMLAttributes, ComponentPropsWithRef, HTMLAttributes, InputHTMLAttributes, MouseEvent, ReactNode, SelectHTMLAttributes } from 'react';
import './Control.css';

type Variant = 'accent' | 'muted' | 'default' | 'link' | 'ghost' | 'danger';
type Size = 'compact' | 'small' | 'normal' | 'large';

function classes(...names: Array<string | undefined>) {
	return names.filter(Boolean).join(' ');
}

export function Button({ className, variant = 'default', size = 'normal', ...props }: ComponentPropsWithRef<'button'> & { variant?: Variant; size?: Size }) {
	return <button {...props} className={classes('oc-button', `oc-button--${variant}`, `oc-button--${size}`, className)} />;
}

type SubmitButtonProps = Omit<ComponentPropsWithRef<'button'>, 'onClick'> & {
	variant?: Variant;
	size?: Size;
	/** The action. The button stays busy until the returned promise settles. */
	onClick: (event: MouseEvent<HTMLButtonElement>) => unknown;
	/** Extra busy state owned by the caller, e.g. a mutation's isPending. */
	pending?: boolean;
	/** Label shown while busy; defaults to the normal label. */
	pendingLabel?: ReactNode;
};

/**
 * A Button for actions that take time. While `onClick` runs it is disabled
 * and shows the aria-busy spinner, and repeat clicks are ignored, so code
 * after the `await` (closing a modal, navigating) only runs once the work is
 * done. Show errors from inside `onClick`; an unhandled rejection is only logged.
 */
export function SubmitButton({ onClick, pending = false, pendingLabel, disabled, children, type = 'button', ...props }: SubmitButtonProps) {
	const [running, setRunning] = useState(false);
	const lock = useRef(false);
	const busy = running || pending;
	const run = async (event: MouseEvent<HTMLButtonElement>) => {
		if (lock.current || pending) return;
		lock.current = true;
		setRunning(true);
		try {
			await onClick(event);
		} finally {
			lock.current = false;
			setRunning(false);
		}
	};
	return <Button {...props} type={type} aria-busy={busy} disabled={disabled || busy} onClick={(event) => { run(event).catch((err: unknown) => console.error('Button action failed', err)); }}>{busy && pendingLabel ? pendingLabel : children}</Button>;
}

/** A link styled as a Button, for navigation and downloads. */
export function AnchorButton({ className, variant = 'default', size = 'normal', ...props }: AnchorHTMLAttributes<HTMLAnchorElement> & { variant?: Variant; size?: Size }) {
	return <a {...props} className={classes('oc-button', `oc-button--${variant}`, `oc-button--${size}`, className)} />;
}

export function ButtonGroup({ label, joined = false, className, ...props }: Omit<HTMLAttributes<HTMLDivElement>, 'role' | 'aria-label'> & { label: string; joined?: boolean }) {
	return <div {...props} role="group" aria-label={label} className={classes('oc-button-group', joined ? 'oc-button-group--joined' : undefined, className)} />;
}

export function SearchField({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
	return <input {...props} type="search" className={classes('oc-field', 'oc-field--search', className)} />;
}

export function SelectField({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
	return <select {...props} className={classes('oc-field', className)} />;
}

export function TextField({ className, ...props }: ComponentPropsWithRef<'input'>) {
	return <input {...props} className={classes('oc-field', className)} />;
}

export function CheckboxField({ className, ...props }: Omit<ComponentPropsWithRef<'input'>, 'type'>) {
	return <input {...props} type="checkbox" className={classes('oc-checkbox', className)} />;
}

export function TextareaField({ className, ...props }: ComponentPropsWithRef<'textarea'>) {
	return <textarea {...props} className={classes('oc-field', 'oc-field--textarea', className)} />;
}
