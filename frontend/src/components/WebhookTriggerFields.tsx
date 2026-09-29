import type { FilterRow, Op, Trigger } from '../lib/webhookFilters';
import { Button, SelectField, TextField } from './Control';
import { IconButton } from './IconButton';

const OPS: { value: Op; label: string }[] = [
  { value: 'equals', label: 'equals' },
  { value: 'oneOf', label: 'is one of' },
  { value: 'exists', label: 'exists' },
  { value: 'missing', label: 'is missing' },
];

function FilterList({ title, keyLabel, placeholder, rows, onChange, disabled }: { title: string; keyLabel: string; placeholder: string; rows: FilterRow[]; onChange: (rows: FilterRow[]) => void; disabled?: boolean }) {
  const set = (i: number, patch: Partial<FilterRow>) => onChange(rows.map((row, j) => (j === i ? { ...row, ...patch } : row)));
  return (
    <fieldset className="webhook-filter" aria-label={title}>
      <legend>{title}</legend>
      {rows.map((row, i) => (
        <div key={i} className="webhook-filter-row">
          <TextField aria-label={`${title} ${i + 1} ${keyLabel}`} placeholder={placeholder} value={row.key} disabled={disabled} onChange={(e) => set(i, { key: e.target.value })} />
          <SelectField aria-label={`${title} ${i + 1} operator`} value={row.op} disabled={disabled} onChange={(e) => set(i, { op: e.target.value as Op })}>
            {OPS.map((op) => <option key={op.value} value={op.value}>{op.label}</option>)}
          </SelectField>
          <TextField aria-label={`${title} ${i + 1} value`} placeholder={row.op === 'oneOf' ? 'a, b, c' : ''} value={row.value} disabled={disabled || row.op === 'exists' || row.op === 'missing'} onChange={(e) => set(i, { value: e.target.value })} />
          <IconButton icon="bi-x" label={`Remove ${title.toLowerCase()} ${i + 1}`} variant="ghost" disabled={disabled} onClick={() => onChange(rows.filter((_, j) => j !== i))} />
        </div>
      ))}
      <Button type="button" size="small" variant="ghost" disabled={disabled} onClick={() => onChange([...rows, { key: '', op: 'equals', value: '' }])}><i className="bi bi-plus" aria-hidden="true" />Add {keyLabel}</Button>
    </fieldset>
  );
}

/** Routine-form filter fields shown when a webhook inbox triggers the routine. */
export function WebhookTriggerFields({ trigger, onChange, disabled }: { trigger: Trigger; onChange: (trigger: Trigger) => void; disabled?: boolean }) {
  return (
    <>
      <p className="webhook-filter-hint">A delivery runs the routine only when every condition matches. With no conditions, every delivery runs it.</p>
      <FilterList title="Header conditions" keyLabel="header" placeholder="X-Forgejo-Event" rows={trigger.headers} onChange={(headers) => onChange({ ...trigger, headers })} disabled={disabled} />
      <FilterList title="Body conditions" keyLabel="JSON pointer" placeholder="/action" rows={trigger.fields} onChange={(fields) => onChange({ ...trigger, fields })} disabled={disabled} />
    </>
  );
}
