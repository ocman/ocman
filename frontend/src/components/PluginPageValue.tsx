import type { PageValue, PageDisplay } from '../lib/pluginPages';

export function PluginPageValue({ value, display }: { value: PageValue; display: PageDisplay }) {
  if (display === 'summary' && typeof value === 'object') return <span className="plugin-cell-summary">
    <span className="plugin-cell-label" title={value.label}>{value.label}</span>
    {value.description && <small title={value.description}>{value.description}</small>}
    {!!value.count && <span className="plugin-cell-count">{value.count} {value.count === 1 ? 'other' : 'others'}</span>}
  </span>;
  if (display === 'severity') return <span className={`plugin-severity plugin-severity-${value}`}><span aria-hidden="true" />{String(value)}</span>;
  if (display === 'boolean') return <>{value ? 'Yes' : 'No'}</>;
  if (display === 'status') return <>{String(value).replaceAll('_', ' ')}</>;
  return <>{String(value)}</>;
}
