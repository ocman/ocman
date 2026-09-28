import type { ButtonHTMLAttributes } from 'react';
import './ArchiveButton.css';

export function ArchiveIcon({ size = 12 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 3.5h12v2H2zm1 3h10v6H3zm3 2.5h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

type Props = Omit<ButtonHTMLAttributes<HTMLButtonElement>, 'children'> & {
  /** Accessible name; doubles as the tooltip. */
  label?: string;
  iconSize?: number;
};

/** The one archive button: bordered square, accent fill on hover. */
export function ArchiveButton({ label = 'Archive', iconSize, className, ...rest }: Props) {
  return (
    <button type="button" className={`session-archive-btn${className ? ` ${className}` : ''}`} title={label} aria-label={label} {...rest}>
      <ArchiveIcon size={iconSize} />
    </button>
  );
}
