import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { Button } from './Control';
import { ToggleField } from './ToggleField';
import { SettingRow } from './SettingRow';
import { InlineAlert } from './InlineAlert';

const endpoint = '/api/settings/platforms';
const options = [['opencode', 'OpenCode'], ['claudecode', 'Claude Code'], ['codex', 'Codex']] as const;
type Configuration = { enabled: string[]; reloadPending?: boolean; overridden?: boolean };

export function PlatformSettings() {
  const [enabled, setEnabled] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [pending, setPending] = useState(false);
  const [overridden, setOverridden] = useState(false);
  useEffect(() => {
    fetchJSON<Configuration>(endpoint).then((value) => {
      setEnabled(value.enabled);
      setPending(value.reloadPending ?? false);
      setOverridden(value.overridden ?? false);
      setLoaded(true);
    }, (error: unknown) => setError(error instanceof Error ? error.message : 'Failed to load platforms'));
  }, []);
  useEffect(() => {
    if (!pending) return;
    const timer = setInterval(() => {
      void fetchJSON<Configuration>(endpoint).then((value) => setPending(value.reloadPending ?? false), () => {});
    }, 1000);
    return () => clearInterval(timer);
  }, [pending]);
  return <>
    <SettingRow setting="enabled-platforms" block>
      <form style={{ display: 'flex', flexDirection: 'column', gap: 12 }} onSubmit={(event) => {
        event.preventDefault();
        if (!loaded || saving || overridden || enabled.length === 0) return;
        setSaving(true);
        setError('');
        setSaved(false);
        void postJSON<Configuration>(endpoint, { enabled }).then((value) => {
          setSaved(true);
          setPending(value.reloadPending ?? false);
        }, (error: unknown) => {
          setError(error instanceof Error ? error.message : 'Failed to save platforms');
        }).finally(() => setSaving(false));
      }}>
        {options.map(([id, label]) =>
          <div key={id} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ToggleField label={label} checked={enabled.includes(id)} disabled={!loaded || saving || overridden}
            onChange={(event) => {
              setEnabled(event.target.checked ? [...enabled, id] : enabled.filter((value) => value !== id));
              setSaved(false);
            }} />{label}</div>)}
        <Button type="submit" disabled={!loaded || saving || overridden || enabled.length === 0}>{saving ? 'Saving…' : 'Save'}</Button>
        {loaded && enabled.length === 0 && <InlineAlert>Enable at least one platform.</InlineAlert>}
        {(saved || pending) && <span role="status">{pending ? 'Saved. Applying after active turns, prompts and queued messages finish.' : 'Platform changes applied.'}</span>}
        {overridden && <InlineAlert>Remove the explicit -platforms flag to configure platforms here.</InlineAlert>}
      </form>
    </SettingRow>
    {error && <InlineAlert>{error}</InlineAlert>}
  </>;
}
