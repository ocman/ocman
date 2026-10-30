import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { Button } from './Control';
import { ToggleField } from './ToggleField';
import { SettingRow } from './SettingRow';
import { InlineAlert } from './InlineAlert';
import { PlatformSetupModal, codexSetupEndpoint } from './PlatformSetupModal';
import type { PlatformSetup } from './PlatformSetupModal';

const endpoint = '/api/settings/platforms';
const options = [
  { id: 'opencode', label: 'OpenCode' },
  { id: 'claudecode', label: 'Claude Code' },
  { id: 'codex', label: 'Codex' },
];
type Configuration = { enabled: string[]; reloadPending?: boolean; overridden?: boolean };

export function PlatformSettings() {
  const [enabled, setEnabled] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [pending, setPending] = useState(false);
  const [overridden, setOverridden] = useState(false);
  const [checking, setChecking] = useState(false);
  const [setup, setSetup] = useState<{ status: PlatformSetup | null; error: string } | null>(null);
  async function toggle(id: string, checked: boolean) {
    setSaved(false);
    if (id !== 'codex' || !checked) {
      setEnabled(values => checked ? [...values, id] : values.filter(value => value !== id));
      return;
    }
    setChecking(true);
    try {
      const status = await fetchJSON<PlatformSetup>(codexSetupEndpoint);
      if (status.ready === true) setEnabled(values => [...values, id]);
      else setSetup({ status, error: '' });
    } catch (err) {
      setSetup({ status: null, error: err instanceof Error ? err.message : 'Could not check Codex dependencies' });
    } finally { setChecking(false); }
  }
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
        if (!loaded || saving || checking || setup || overridden || enabled.length === 0) return;
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
        {options.map(({ id, label }) =>
          <div key={id}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ToggleField label={label} checked={enabled.includes(id)} disabled={!loaded || saving || checking || !!setup || overridden}
            onChange={(event) => {
              void toggle(id, event.target.checked);
            }} />{label}</div>
          </div>)}
        {checking && <span role="status">Checking Codex dependencies…</span>}
        <Button type="submit" disabled={!loaded || saving || checking || !!setup || overridden || enabled.length === 0}>{saving ? 'Saving…' : 'Save'}</Button>
        {loaded && enabled.length === 0 && <InlineAlert>Enable at least one platform.</InlineAlert>}
        {(saved || pending) && <span role="status">{pending ? 'Saved. Applying after active turns, prompts and queued messages finish.' : 'Platform changes applied.'}</span>}
        {overridden && <InlineAlert>Remove the explicit -platforms flag to configure platforms here.</InlineAlert>}
      </form>
    </SettingRow>
    {error && <InlineAlert>{error}</InlineAlert>}
    {setup && <PlatformSetupModal initial={setup.status} initialError={setup.error} onClose={ready => {
      setSetup(null);
      setEnabled(values => ready ? [...values.filter(value => value !== 'codex'), 'codex'] : values.filter(value => value !== 'codex'));
    }} />}
  </>;
}
