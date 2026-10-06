import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api';
import { clearSettingsCache, useSettingsRevision } from '../lib/projectSettingsCache';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingText } from './SettingRow';

export function DefaultAgentSetting() {
  const [agent, setAgent] = useState('build');
  const [loaded, setLoaded] = useState(false);
  const revision = useSettingsRevision();
  const save = useSettingSave();
  useEffect(() => {
    const controller = new AbortController();
    fetchJSON<{ defaultAgent: string }>('/api/settings/default-agent', controller.signal).then((value) => {
      if (controller.signal.aborted) return;
      setAgent(value.defaultAgent);
      setLoaded(true);
    }).catch(() => { /* Keep disabled until the saved setting is known. */ });
    return () => controller.abort();
  }, [revision]);
  return (
    <SettingRow setting="default-agent">
      <SettingText value={agent} ariaLabel="Default agent" disabled={!loaded || save.state === 'saving'} save={save}
        onSave={async (defaultAgent) => {
          const value = await postJSON<{ defaultAgent: string }>('/api/settings/default-agent', { defaultAgent });
          setAgent(value.defaultAgent);
          clearSettingsCache();
        }} />
    </SettingRow>
  );
}
