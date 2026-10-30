import { useBetaFeatures } from '../lib/betaFeatures';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingToggle } from './SettingRow';
import { InlineAlert } from './InlineAlert';

export function BetaFeaturesSettings() {
  const enabled = useBetaFeatures((s) => s.factory);
  const setFactory = useBetaFeatures((s) => s.setFactory);
  const { loaded, error, load } = useBetaFeatures();
  const save = useSettingSave();
  return <SettingRow setting="factory-beta">
    {error && <InlineAlert onRetry={() => { void load(); }}>Could not load beta features.</InlineAlert>}
    <SettingToggle checked={enabled} disabled={!loaded || error || save.state === 'saving'} onSave={setFactory} save={save} ariaLabel="Enable Factory beta" />
  </SettingRow>;
}
