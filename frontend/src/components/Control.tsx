import type { ButtonHTMLAttributes, InputHTMLAttributes, SelectHTMLAttributes } from 'react';
import './Control.css';

type Variant = 'accent' | 'muted' | 'default';
type Size = 'normal' | 'large';

function classes(...names: Array<string | undefined>) {
	return names.filter(Boolean).join(' ');
}

export function Button({ className, variant = 'default', size = 'normal', ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { variant?: Variant; size?: Size }) {
	return <button {...props} className={classes('oc-button', `oc-button--${variant}`, `oc-button--${size}`, className)} />;
}

export function SearchField({ className, ...props }: InputHTMLAttributes<HTMLInputElement>) {
	return <input {...props} type="search" className={classes('oc-field', 'oc-field--search', className)} />;
}

export function SelectField({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
	return <select {...props} className={classes('oc-field', className)} />;
}
