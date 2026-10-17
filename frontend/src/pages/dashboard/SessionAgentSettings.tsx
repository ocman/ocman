import { useEffect, useState } from 'react';
import { api, type ModelFallthroughSettings } from '../../lib/api';
import { SettingRow, SettingToggle, SettingNumber } from '../../components/SettingRow';
import { useSettingSave } from '../../lib/useSaveStatus';

export function WorktreePermissionSettings() {
  const [inheritPerms, setInheritPerms] = useState(true);
  const save = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api.getWorktreeInheritPermissions(ctrl.signal).then(({ enabled }) => setInheritPerms(enabled))
      .catch(() => { /* best-effort; keep default on */ });
    return () => ctrl.abort();
  }, []);
  return <SettingRow setting="worktree-inherit-permissions">
    <SettingToggle
      testId="worktree-inherit-toggle"
      ariaLabel="Worktree sessions inherit parent permissions"
      checked={inheritPerms}
      save={save}
      onSave={async (want) => {
        setInheritPerms(want);
        try {
          await api.setWorktreeInheritPermissions(want);
        } catch (err) {
          setInheritPerms(!want);
          throw err;
        }
      }}
    />
  </SettingRow>;
}

export function ModelFallbackSettings() {
  const [times, setTimes] = useState<ModelFallthroughSettings | null>(null);
  const patienceSave = useSettingSave();
  const fallbackSave = useSettingSave();
  useEffect(() => {
    const ctrl = new AbortController();
    api.getModelFallthroughSettings(ctrl.signal).then(setTimes).catch(() => { /* rows stay hidden */ });
    return () => ctrl.abort();
  }, []);
  if (!times) return null;
  const save = async (next: ModelFallthroughSettings) => {
    const previous = times;
    setTimes(next);
    try {
      await api.setModelFallthroughSettings(next);
    } catch (err) {
      setTimes(previous);
      throw err;
    }
  };
  return <>
    <SettingRow setting="model-fallthrough-patience">
      <SettingNumber ariaLabel="Model fallthrough patience in minutes" unit="min" min={1} max={1440}
        value={times.patienceMinutes} save={patienceSave}
        onSave={(patienceMinutes) => save({ ...times, patienceMinutes })} />
    </SettingRow>
    <SettingRow setting="model-fallthrough-cooldown">
      <SettingNumber ariaLabel="Model fallthrough cooldown in minutes" unit="min" min={1} max={1440}
        value={times.fallbackMinutes} save={fallbackSave}
        onSave={(fallbackMinutes) => save({ ...times, fallbackMinutes })} />
    </SettingRow>
  </>;
}
