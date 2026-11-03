import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { checkPlatformSetup, platformSettingsEndpoint as endpoint } from '../lib/platformSetup';
import type { PlatformConfiguration as Configuration, PlatformOption, PlatformSetup } from '../lib/platformSetup';
import { Button } from './Control';
import { ToggleField } from './ToggleField';
import { SettingRow } from './SettingRow';
import { InlineAlert } from './InlineAlert';
import { PlatformSetupModal } from './PlatformSetupModal';

export function PlatformSettings() {
  const [enabled, setEnabled] = useState<string[]>([]);
  const [options, setOptions] = useState<PlatformOption[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [saved, setSaved] = useState(false);
  const [pending, setPending] = useState(false);
  const [overridden, setOverridden] = useState(false);
  const [checking, setChecking] = useState<PlatformOption | null>(null);
  const [setup, setSetup] = useState<{ platform: PlatformOption; status: PlatformSetup | null; error: string } | null>(null);
  async function toggle(platform: PlatformOption, checked: boolean) {
    const { id } = platform;
    setSaved(false);
    if (!platform.setup || !checked) {
      setEnabled(values => checked ? [...values, id] : values.filter(value => value !== id));
      return;
    }
    setChecking(platform);
    try {
      const status = await checkPlatformSetup(id);
      if (status.ready === true) setEnabled(values => [...values, id]);
      else setSetup({ platform, status, error: '' });
    } catch (err) {
      setSetup({ platform, status: null, error: err instanceof Error ? err.message : `Could not check ${platform.label} dependencies` });
    } finally { setChecking(null); }
  }
  useEffect(() => {
    fetchJSON<Configuration>(endpoint).then((value) => {
      setEnabled(value.enabled);
      setOptions(value.platforms ?? []);
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
        {options.map((platform) =>
          <div key={platform.id}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <ToggleField label={platform.label} checked={enabled.includes(platform.id)} disabled={!loaded || saving || !!checking || !!setup || overridden}
            onChange={(event) => {
              void toggle(platform, event.target.checked);
            }} />{platform.label}</div>
          </div>)}
        {checking && <span role="status">Checking {checking.label} dependencies…</span>}
        <Button type="submit" disabled={!loaded || saving || !!checking || !!setup || overridden || enabled.length === 0}>{saving ? 'Saving…' : 'Save'}</Button>
        {loaded && enabled.length === 0 && <InlineAlert>Enable at least one platform.</InlineAlert>}
        {(saved || pending) && <span role="status">{pending ? 'Saved. Applying after active turns, prompts and queued messages finish.' : 'Platform changes applied.'}</span>}
        {overridden && <InlineAlert>Remove the explicit -platforms flag to configure platforms here.</InlineAlert>}
      </form>
    </SettingRow>
    {error && <InlineAlert>{error}</InlineAlert>}
    {setup && <PlatformSetupModal platform={setup.platform} initial={setup.status} initialError={setup.error} onClose={ready => {
      const { id } = setup.platform;
      setSetup(null);
      setEnabled(values => ready ? [...values.filter(value => value !== id), id] : values.filter(value => value !== id));
    }} />}
  </>;
}
