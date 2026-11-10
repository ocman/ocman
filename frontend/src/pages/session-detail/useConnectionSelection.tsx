import { useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from '../../lib/api.requests';
import { queryClient } from '../../lib/queryClient';
import { useMachinePlatforms } from '../../lib/useCapabilities';
import type { ConnectionInventory } from '../../components/PlatformConnectionSettings';
import { SelectField } from '../../components/Control';
import { InlineAlert } from '../../components/InlineAlert';
import { connectionForModel, ownerConnectionID } from './connectionSelection';
import { connectionAvailabilityLabel } from '../../lib/platformConnections';

export function useConnectionSelection(owner: string, selected: string | undefined, onSelect: (id: string, compatible: boolean) => void, frozen: boolean) {
  const legacy = useMachinePlatforms(owner);
  const supported = legacy.some(entry => !!entry.kind);
  const inventory = useQuery({ queryKey: ['platform-connections', owner], queryFn: () => fetchJSON<ConnectionInventory>(`/api/platform-connections?remoteId=${encodeURIComponent(owner)}`), enabled: supported, retry: false }, queryClient);
  const connections = inventory.data?.supported ? inventory.data.connections : undefined;
  const platformForModel = useCallback((model: string) => connectionForModel(model, selected, owner, connections, legacy), [selected, owner, connections, legacy]);
  const selector = connections ? <label>Platform connection
    <SelectField aria-label="Platform connection" value={selected ?? ''} disabled={frozen} onChange={event => {
      const previous = connections.find(c => ownerConnectionID(owner, c.id) === selected);
      const next = connections.find(c => ownerConnectionID(owner, c.id) === event.target.value);
      onSelect(event.target.value, !!previous && !!next && previous.kind === next.kind);
    }}>
      <option value="" disabled>Choose a connection</option>
      {connections.map(c => <option key={c.id} value={ownerConnectionID(owner, c.id)} disabled={!c.enabled || c.status !== 'ready'}>{c.name}{!c.enabled || c.status !== 'ready' ? ` (${connectionAvailabilityLabel(c)})` : ''}</option>)}
    </SelectField>
  </label> : supported && inventory.error ? <InlineAlert onRetry={() => { void inventory.refetch(); }}>Could not load platform connections. Retry before selecting an account.</InlineAlert> : null;
  return { platformForModel, selector, named: !!connections };
}
