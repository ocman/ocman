import { useEffect, useState } from 'react';
import { loadLinkPreviewRules, saveLinkPreviewRules } from '../lib/linkPreviewRules';
import type { LinkPreviewRule } from '../lib/linkPreviewRules';
import { Button, TextField } from './Control';
import { SettingDescription } from './SettingRow';

export function LinkPreviewSettings() {
  const [rules, setRules] = useState<LinkPreviewRule[]>([]);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [loaded, setLoaded] = useState(false);

  useEffect(() => {
    let active = true;
    loadLinkPreviewRules().then((loaded) => {
      if (active) { setRules(loaded); setLoaded(true); }
    }).catch((err: unknown) => {
      if (active) setError(String(err));
    });
    return () => { active = false; };
  }, []);

  const update = (index: number, key: keyof LinkPreviewRule, value: string) => {
    setRules((current) => current.map((rule, i) => i === index ? { ...rule, [key]: value } : rule));
  };

  const save = async () => {
    setSaving(true);
    setError('');
    try {
      await saveLinkPreviewRules(rules);
    } catch (err) {
      setError(String(err));
    } finally {
      setSaving(false);
    }
  };

  return <div>
    <SettingDescription>Match text in a conversation and show a link card below it. Use <code>$&amp;</code> for the whole match or <code>$1</code> for the first capture group. For example, <code>ABC-\d+</code> with <code>https://tracker.example.com/issues/$&amp;</code>.</SettingDescription>
    {rules.map((rule, index) => <div className="settings-prompt-template" key={index}>
      <label>Regular expression {index + 1}
        <TextField type="text" value={rule.pattern} onChange={(e) => update(index, 'pattern', e.target.value)} placeholder="ABC-\d+" spellCheck={false} />
      </label>
      <label>Link replacement {index + 1}
        <TextField type="url" value={rule.replacement} onChange={(e) => update(index, 'replacement', e.target.value)} placeholder="https://tracker.example.com/issues/$&" spellCheck={false} />
      </label>
      <Button type="button" variant="muted" onClick={() => setRules((current) => current.filter((_, i) => i !== index))}>Remove rule {index + 1}</Button>
    </div>)}
    <Button type="button" disabled={!loaded || rules.length >= 20} onClick={() => setRules((current) => [...current, { pattern: '', replacement: '' }])}>Add rule</Button>
    <Button type="button" variant="accent" disabled={!loaded || saving} onClick={() => void save()}>Save rules</Button>
    {error && <p role="alert">{error}</p>}
  </div>;
}
