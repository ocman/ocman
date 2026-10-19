import { Button, CheckboxField, TextareaField, TextField } from './Control';
import { blankPageValues, type PageField, type PageValues } from '../lib/pluginPages';

export function PluginPageFields({ fields, values, onChange, prefix = '' }: {
  fields: PageField[] | null; values: PageValues; onChange: (value: PageValues) => void; prefix?: string;
}) {
  return <>{(fields ?? []).map((field) => {
    const label = `${prefix}${field.label}`;
    const value = values[field.key];
    const update = (next: PageValues[string]) => onChange({ ...values, [field.key]: next });
    if (field.type === 'list') {
      const rows = Array.isArray(value) ? value : [];
      return <fieldset key={field.key} className="plugin-page-list"><legend>{field.label}</legend>
        {rows.map((row, index) => <fieldset key={index}><legend>{field.label} {index + 1}</legend>
          <PluginPageFields fields={field.fields ?? []} values={row} prefix={`${label} ${index + 1}: `}
            onChange={(next) => update(rows.map((item, i) => i === index ? next : item))} />
          <Button variant="danger" onClick={() => update(rows.filter((_, i) => i !== index))}>Remove {field.label} {index + 1}</Button>
        </fieldset>)}
        <Button onClick={() => update([...rows, blankPageValues(field.fields ?? [])])}>Add {field.label}</Button>
      </fieldset>;
    }
    const text = typeof value === 'string' ? value : '';
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
