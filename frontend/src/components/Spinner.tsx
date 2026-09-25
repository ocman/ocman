import './Spinner.css';

/** Decorative indicator; the surrounding control or text supplies its label. */
export function Spinner({ className = '' }: { className?: string }) {
  return <span className={`oc-loading-spinner ${className}`} aria-hidden="true" />;
}
