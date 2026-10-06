import { useEffect, useState } from 'react';
import { fetchJSON, postJSON } from '../lib/api';
import { clearSettingsCache, useSettingsRevision } from '../lib/projectSettingsCache';
import { useSettingSave } from '../lib/useSaveStatus';
import { SettingRow, SettingSelect } from './SettingRow';
import { InlineAlert } from './InlineAlert';

export function DefaultAgentSetting() {
  const [agent, setAgent] = useState('build');
  const [agents, setAgents] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState('');
  const [saveError, setSaveError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const revision = useSettingsRevision();
  const save = useSettingSave();
  useEffect(() => {
    const controller = new AbortController();
    fetchJSON<{ defaultAgent: string; agents?: string[] }>('/api/settings/default-agent', controller.signal).then((value) => {
      if (controller.signal.aborted) return;
      setError('');
      setAgent(value.defaultAgent);
      setAgents(value.agents ?? ['build', 'plan']);
      setLoaded(true);
    }).catch((error) => {
      if (!controller.signal.aborted) {
        setLoaded(false);
        setError(error instanceof Error ? error.message : String(error));
      }
    });
    return () => controller.abort();
  }, [revision, attempt]);
  return (
    <SettingRow setting="default-agent" detail={(error || saveError) && <>
      {error && <InlineAlert onRetry={() => setAttempt((value) => value + 1)}>{error}</InlineAlert>}
      {saveError && <InlineAlert>{saveError}</InlineAlert>}
    </>}>
      <SettingSelect value={agent} options={agents.map((value) => ({ value, label: value }))}
        ariaLabel="Default agent" placeholder="Choose an agent" searchLabel="Search agents"
        disabled={!loaded || save.state === 'saving'} save={save}
        onSave={async (defaultAgent) => {
          setSaveError('');
          try {
            const value = await postJSON<{ defaultAgent: string }>('/api/settings/default-agent', { defaultAgent });
            setAgent(value.defaultAgent);
            clearSettingsCache();
          } catch (error) {
            setSaveError(error instanceof Error ? error.message : String(error));
            throw error;
          }
        }} />
    </SettingRow>
  );
}
