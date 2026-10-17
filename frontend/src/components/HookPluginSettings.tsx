import { useHookPlugin } from '../lib/useHookPlugin';
import { SubmitButton } from './Control';
import { SettingRow } from './SettingRow';
import { SettingsSection } from './SettingsSection';

/** Settings button installing or removing the hook plugin through the Claude CLI. */
export function HookPluginSettings() {
  const { status, error, set } = useHookPlugin();
  if (!status?.available) return null;
  return (
    <SettingsSection title="Hooks">
    <SettingRow
      label="Live status for terminal sessions"
      desc="Installs ocman's Claude Code plugin at user scope so sessions started in a terminal report busy and idle here."
      detail={error ? <span role="alert">{error}</span> : undefined}
    >
      <SubmitButton size="small" variant={status.installed ? 'danger' : 'accent'} onClick={() => set(!status.installed)}
        pendingLabel={status.installed ? 'Removing…' : 'Installing…'}>
        {status.installed ? 'Uninstall' : 'Install'}
      </SubmitButton>
    </SettingRow>
    </SettingsSection>
  );
}
