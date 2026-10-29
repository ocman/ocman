import { useBetaFeatures } from '../lib/betaFeatures';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingToggle } from './SettingRow';

export function BetaFeaturesSettings() {
  const enabled = useBetaFeatures((s) => s.factory);
  const setFactory = useBetaFeatures((s) => s.setFactory);
  const save = useSettingSave();
  return <SettingRow setting="factory-beta">
    <SettingToggle checked={enabled} onSave={setFactory} save={save} ariaLabel="Enable Factory beta" />
  </SettingRow>;
}
