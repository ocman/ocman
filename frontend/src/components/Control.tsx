import type { ButtonHTMLAttributes, ComponentPropsWithRef, HTMLAttributes, InputHTMLAttributes, SelectHTMLAttributes } from 'react';
import './Control.css';

type Variant = 'accent' | 'muted' | 'default' | 'link' | 'ghost';
type Size = 'compact' | 'small' | 'normal' | 'large';

function classes(...names: Array<string | undefined>) {
	return names.filter(Boolean).join(' ');
}

export function Button({ className, variant = 'default', size = 'normal', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size }) {
	return <button {...props} className={classes('oc-button', `oc-button--${variant}`, `oc-button--${size}`, className)} />;
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

export function TextareaField({ className, ...props }: ComponentPropsWithRef<'textarea'>) {
	return <textarea {...props} className={classes('oc-field', 'oc-field--textarea', className)} />;
}
