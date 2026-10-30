import { SettingRow, SettingToggle } from './SettingRow';
import { InlineAlert } from './InlineAlert';
import { useSettingSave } from '../lib/useSaveStatus';
import { useBadgeSettings, useSaveBadgeSettings, type BadgeSettings } from '../lib/badgeSettings';
import { useBetaFeatures } from '../lib/betaFeatures';

const sources: { key: keyof BadgeSettings; label: string; desc: string }[] = [
  { key: 'sessions', label: 'Unread sessions and input requests', desc: 'Includes permission and choice prompts. Routine sessions are excluded.' },
  { key: 'primary', label: 'Primary Inbox unread', desc: 'Unread Primary messages, excluding permission requests.' },
  { key: 'factory', label: 'Factory Inbox unread', desc: 'Unread Factory messages.' },
  { key: 'routines', label: 'Routines Inbox unread', desc: 'Unread Routines messages. Off by default.' },
];

function BadgeSource({ source, settings, disabled, onSave }: { source: typeof sources[number]; settings: BadgeSettings; disabled: boolean; onSave: (settings: Partial<BadgeSettings>) => Promise<unknown> }) {
  const save = useSettingSave();
  return <SettingRow label={source.label} desc={source.desc}>
    <SettingToggle ariaLabel={source.label} checked={settings[source.key]} disabled={disabled} save={save}
      onSave={(next) => onSave({ [source.key]: next })} />
  </SettingRow>;
}

export function AppBadgeSettings() {
  const factory = useBetaFeatures((s) => s.factory);
  const query = useBadgeSettings();
  const update = useSaveBadgeSettings();
  return <SettingRow setting="app-icon-badge" block>
    {query.isError && <InlineAlert onRetry={() => { void query.refetch(); }} retrying={query.isFetching}>Could not load badge settings.</InlineAlert>}
    {query.data && sources.filter((source) => factory || source.key !== 'factory').map((source) => <BadgeSource key={source.key} source={source} settings={query.data} disabled={query.isFetching || update.isPending} onSave={update.mutateAsync} />)}
  </SettingRow>;
}
