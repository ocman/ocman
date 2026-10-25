import { Button, CheckboxField, SelectField, TextareaField, TextField } from './Control';
import { blankPageValues, pageRowName, type PageField, type PageValues, type PageIssue } from '../lib/pluginPages';
import { PluginProjectPicker } from './PluginProjectPicker';

function numericValue(value: PageValues[string]) {
  return typeof value === 'number' ? value : '';
}

export function PluginPageFields({ fields, values, onChange, prefix = '', ownerId = 'local', path = '', issues = [] }: {
  fields: PageField[] | null; values: PageValues; onChange: (value: PageValues) => void; prefix?: string; ownerId?: string; path?: string; issues?: PageIssue[];
}) {
  return <>{(fields ?? []).map((field) => {
    const label = `${prefix}${field.label}`;
    const value = values[field.key];
    const fieldPath = `${path}/${field.key}`;
    const errors = issues.filter((issue) => issue.path === fieldPath);
    const feedback = <>{field.help && <small>{field.help}</small>}{errors.map((issue, i) => <small role="alert" key={i}>{issue.message}</small>)}</>;
    const update = (next: PageValues[string]) => onChange({ ...values, [field.key]: next });
    if (field.type === 'list') {
      const rows = Array.isArray(value) ? value : [];
      return <fieldset key={field.key} className="plugin-page-list"><legend>{field.label}</legend>
        {feedback}
        {rows.map((row, index) => <details key={index} open className="plugin-page-item">
          <summary>{pageRowName(field.fields, row) || `${field.label} ${index + 1}`}</summary>
          <div className="plugin-page-item-fields"><PluginPageFields fields={field.fields ?? []} values={row} prefix={`${label} ${index + 1}: `} ownerId={ownerId} path={`${fieldPath}/${index}`} issues={issues}
            onChange={(next) => update(rows.map((item, i) => i === index ? next : item))} />
          </div><Button variant="danger" onClick={() => update(rows.filter((_, i) => i !== index))}>Remove {field.label} {index + 1}</Button>
        </details>)}
        <Button disabled={field.maxItems !== undefined && rows.length >= field.maxItems} onClick={() => update([...rows, blankPageValues(field.fields ?? [])])}>Add {field.label}</Button>
      </fieldset>;
    }
    const text = typeof value === 'string' ? value : '';
    if (field.type === 'number' || field.type === 'integer') return <label key={field.key} className="plugin-page-field"><span>{field.label}</span>
      <TextField aria-label={label} aria-invalid={errors.length > 0} type="number" value={numericValue(value)} min={field.minimum} max={field.maximum} step={field.type === 'integer' ? 1 : 'any'} required={field.required}
        onChange={(e) => {
          if (e.target.value === '') { const next = { ...values }; delete next[field.key]; onChange(next); }
          else if (Number.isFinite(e.target.valueAsNumber)) update(e.target.valueAsNumber);
        }} />{feedback}
    </label>;
    if (field.type === 'projects') return <div key={field.key} className="plugin-page-field"><span>{field.label}</span>
      <PluginProjectPicker ownerId={ownerId} label={label} value={text} onChange={update} />
      {feedback}
    </div>;
    if (field.type === 'select') {
      const source = fields?.find((f) => f.key === field.optionsFrom);
      const rows = values[field.optionsFrom ?? ''];
      const options = field.options ?? (Array.isArray(rows) ? [...new Set(rows.map((row) => String(row[source?.fields?.[0]?.key ?? ''] ?? '')).filter(Boolean))].map((value) => ({ value, label: value })) : []);
      return <label key={field.key} className="plugin-page-field"><span>{field.label}</span>
        <SelectField aria-label={label} aria-invalid={errors.length > 0} required={field.required} value={text} onChange={(e) => update(e.target.value)}>
          <option value="">Choose an option</option>
          {text && !options.some((o) => o.value === text) && <option value={text}>{text} · no matching target</option>}
          {options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}
        </SelectField>{feedback}
      </label>;
    }
    return <label key={field.key} className="plugin-page-field"><span>{field.label}</span>
      {field.type === 'boolean'
        ? <CheckboxField aria-label={label} checked={value === true} onChange={(e) => update(e.target.checked)} />
        : field.type === 'multiline'
          ? <TextareaField aria-label={label} aria-invalid={errors.length > 0} required={field.required} value={text} onChange={(e) => update(e.target.value)} />
          : <TextField aria-label={label} aria-invalid={errors.length > 0} required={field.required} type={field.type === 'secret' ? 'password' : 'text'} autoComplete="off" value={text} onChange={(e) => update(e.target.value)} />}
      {feedback}
    </label>;
  })}</>;
}
