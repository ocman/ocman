import { useEffect, useState } from 'react';
import { api } from '../lib/api';
import { useSettingSave } from '../lib/useSaveStatus';
import { useMachinePlatforms } from '../lib/useCapabilities';
import { SettingRow, SettingSelect } from './SettingRow';

/** The platform a new conversation starts on; shown when there is a choice. */
export function DefaultPlatformSetting() {
  const options = useMachinePlatforms('local');
  const [platform, setPlatform] = useState('');
  const save = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api.getDefaultPlatform(ctrl.signal).then((value) => setPlatform(value.platform))
      .catch(() => { /* Keep disabled until the saved preference is known. */ });
    return () => ctrl.abort();
  }, []);
  if (options.length < 2) return null;
  return (
    <SettingRow setting="default-platform">
      <SettingSelect
        ariaLabel="Default platform for new conversations"
        placeholder="Platform"
        searchLabel="Search platforms"
        value={platform}
        options={options.map((p) => ({ value: p.id, label: p.displayName }))}
        disabled={!platform || save.state === 'saving'}
        save={save}
        onSave={async (next) => {
          const result = await api.setDefaultPlatform(next);
          setPlatform(result.platform);
        }}
      />
    </SettingRow>
  );
}
