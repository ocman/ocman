import { useQuery, useQueryClient } from '@tanstack/react-query';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { useSettingSave } from '../lib/useSaveStatus';
import { InlineAlert } from './InlineAlert';
import { SettingDescription, SettingRow, SettingToggle } from './SettingRow';

interface UsageCollectionView {
  enabled: boolean;
  relayUrl: string;
  version: string;
}

const path = '/api/settings/usage-collection';
const queryKey = ['usage-collection'];

export function UsageCollectionSettings() {
  const client = useQueryClient();
  const query = useQuery({ queryKey, queryFn: ({ signal }) => fetchJSON<UsageCollectionView>(path, signal) });
  const save = useSettingSave();
  const persist = async (enabled: boolean) => {
    client.setQueryData(queryKey, await postJSON<UsageCollectionView>(path, { enabled }));
  };
  return <>
    {query.isError && <InlineAlert onRetry={() => { void query.refetch(); }}>Could not load usage collection settings.</InlineAlert>}
    <SettingRow setting="usage-collection" detail={query.data && (query.data.relayUrl
      ? <>Collector: <code>{query.data.relayUrl}</code>. Reported version: <code>{query.data.version}</code>.</>
      : 'No relay configured. Usage reports are not sent.')}>
      <SettingToggle ariaLabel="Send usage reports" checked={query.data?.enabled ?? false}
        disabled={!query.data || query.isError || save.state === 'saving'} save={save} onSave={persist} />
    </SettingRow>
    <SettingDescription>
      Reports are pseudonymous, not anonymous. The relay forwards usage events and separate
      source-IP security events to its OpenTelemetry backend without storing them locally.
      The backend operator controls access and retention. Opting out stops usage reporting, not security logging
      for other relay requests or separately configured OpenTelemetry diagnostics.
    </SettingDescription>
  </>;
}
