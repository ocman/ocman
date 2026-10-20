import { Button, CheckboxField, SelectField, TextareaField, TextField } from './Control';
import { blankPageValues, type PageField, type PageValues } from '../lib/pluginPages';
import { PluginProjectPicker } from './PluginProjectPicker';

export function PluginPageFields({ fields, values, onChange, prefix = '', ownerId = 'local' }: {
  fields: PageField[] | null; values: PageValues; onChange: (value: PageValues) => void; prefix?: string; ownerId?: string;
}) {
  return <>{(fields ?? []).map((field) => {
    const label = `${prefix}${field.label}`;
    const value = values[field.key];
    const update = (next: PageValues[string]) => onChange({ ...values, [field.key]: next });
    if (field.type === 'list') {
      const rows = Array.isArray(value) ? value : [];
      return <fieldset key={field.key} className="plugin-page-list"><legend>{field.label}</legend>
        {field.help && <p className="plugin-page-help">{field.help}</p>}
        {rows.map((row, index) => <details key={index} open className="plugin-page-item">
          <summary>{String(row[field.fields?.[0]?.key ?? ''] || `${field.label} ${index + 1}`)}</summary>
          <div className="plugin-page-item-fields"><PluginPageFields fields={field.fields ?? []} values={row} prefix={`${label} ${index + 1}: `} ownerId={ownerId}
            onChange={(next) => update(rows.map((item, i) => i === index ? next : item))} />
          </div><Button variant="danger" onClick={() => update(rows.filter((_, i) => i !== index))}>Remove {field.label} {index + 1}</Button>
        </details>)}
        <Button onClick={() => update([...rows, blankPageValues(field.fields ?? [])])}>Add {field.label}</Button>
      </fieldset>;
    }
    const text = typeof value === 'string' ? value : '';
    if (field.type === 'projects') return <div key={field.key} className="plugin-page-field"><span>{field.label}</span>
      <PluginProjectPicker ownerId={ownerId} label={label} value={text} onChange={update} />
      {field.help && <small>{field.help}</small>}
    </div>;
    if (field.type === 'select') {
      const source = fields?.find((f) => f.key === field.optionsFrom);
      const rows = values[field.optionsFrom ?? ''];
      const options = Array.isArray(rows) ? rows.map((row) => String(row[source?.fields?.[0]?.key ?? ''] ?? '')).filter(Boolean) : [];
      return <label key={field.key} className="plugin-page-field"><span>{field.label}</span>
        <SelectField aria-label={label} value={text} onChange={(e) => update(e.target.value)}>
          <option value="">Choose a target</option>
          {text && !options.includes(text) && <option value={text}>{text} · no matching target</option>}
          {[...new Set(options)].map((option) => <option key={option} value={option}>{option}</option>)}
        </SelectField>{field.help && <small>{field.help}</small>}
      </label>;
    }
    return <label key={field.key} className="plugin-page-field"><span>{field.label}</span>
      {field.type === 'boolean'
        ? <CheckboxField aria-label={label} checked={value === true} onChange={(e) => update(e.target.checked)} />
        : field.type === 'multiline'
          ? <TextareaField aria-label={label} value={text} onChange={(e) => update(e.target.value)} />
          : <TextField aria-label={label} type={field.type === 'secret' ? 'password' : 'text'} autoComplete="off" value={text} onChange={(e) => update(e.target.value)} />}
      {field.help && <small>{field.help}</small>}
    </label>;
  })}</>;
}
