import { SESSION_FILTER_FIELDS, type SessionListFilters } from '../lib/sessionListFilters';
import { CheckboxField } from './Control';
import './SessionFilterControls.css';

export function SessionFilterControls({ value, onChange }: {
  value: SessionListFilters;
  onChange: (key: keyof SessionListFilters, checked: boolean) => void;
}) {
  return <div className="session-filter-options">
    {SESSION_FILTER_FIELDS.map(([key, label]) => <label key={key}
      title={key === 'alwaysShowPrompts' ? 'Keep sessions awaiting permission or an answer visible regardless of filters' : undefined}>
      <CheckboxField checked={value[key]} onChange={event => onChange(key, event.target.checked)} />
      <span>{label}</span>
    </label>)}
  </div>;
}
