import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { Button, TextField } from './Control';
import { SettingRow } from './SettingRow';
import { InlineAlert } from './InlineAlert';

const endpoint = '/api/settings/idle-project-timeout';

export function IdleProjectSettings() {
  const [minutes, setMinutes] = useState('0');
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  useEffect(() => {
    fetchJSON<{ minutes: number }>(endpoint).then((value) => {
      setMinutes(String(value.minutes));
      setLoaded(true);
    }, (error: unknown) => setError(error instanceof Error ? error.message : 'Failed to load timeout'));
  }, []);
  const valid = /^\d+$/.test(minutes) && Number(minutes) <= 43200;
  return <>
    <SettingRow setting="idle-project-timeout">
      <form onSubmit={(event) => {
        event.preventDefault();
        if (!valid || saving) return;
        setSaving(true);
        setError('');
        setSaved(false);
        void postJSON(endpoint, { minutes: Number(minutes) }).then(() => setSaved(true), (error: unknown) => {
          setError(error instanceof Error ? error.message : 'Failed to save timeout');
        }).finally(() => setSaving(false));
      }}>
        <TextField type="number" min={0} max={43200} step={1} aria-label="Idle project timeout in minutes"
          disabled={!loaded || saving} value={minutes} onChange={(event) => { setMinutes(event.target.value); setSaved(false); }} />
        <Button type="submit" disabled={!loaded || saving || !valid}>{saving ? 'Saving…' : 'Save'}</Button>
        {saved && <span role="status">Saved</span>}
      </form>
    </SettingRow>
    {error && <InlineAlert>{error}</InlineAlert>}
  </>;
}
