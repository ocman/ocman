import { useCallback } from 'react';
import { useQuery } from '@tanstack/react-query';
import { fetchJSON } from '../../lib/api.requests';
import { queryClient } from '../../lib/queryClient';
import { useMachinePlatforms } from '../../lib/useCapabilities';
import type { ConnectionInventory } from '../../components/PlatformConnectionSettings';
import { InlineAlert } from '../../components/InlineAlert';
import { LoadingState } from '../../components/LoadingState';
import { PlatformLogo } from '../../components/PlatformBadge';
import type { ModelPickerContext } from '../../components/assistant/ModelPicker';
import { connectionForModel, ownerConnectionID } from './connectionSelection';
import { connectionAvailabilityLabel } from '../../lib/platformConnections';

export function useConnectionSelection(owner: string, selected: string | undefined, onSelect: (id: string, compatible: boolean) => void, frozen: boolean,
  catalog?: { ready: boolean; error: string; retry: () => void }) {
  const legacy = useMachinePlatforms(owner);
  const supported = legacy.some(entry => !!entry.kind);
  const inventory = useQuery({ queryKey: ['platform-connections', owner], queryFn: () => fetchJSON<ConnectionInventory>(`/api/platform-connections?remoteId=${encodeURIComponent(owner)}`), enabled: supported, retry: false }, queryClient);
  const connections = inventory.data?.supported ? inventory.data.connections : undefined;
  const platformForModel = useCallback((model: string) => connectionForModel(model, selected, owner, connections, legacy), [selected, owner, connections, legacy]);
  const tabs = connections ? {
    label: 'Model platforms', value: selected ?? '',
    items: connections.map(c => ({ value: ownerConnectionID(owner, c.id),
      label: <><PlatformLogo platform={c.kind} />{c.name}{!c.enabled || c.status !== 'ready' ? ` (${connectionAvailabilityLabel(c)})` : ''}</>,
      disabled: frozen || !c.enabled || c.status !== 'ready',
    })),
    onChange: (id: string) => {
      const previous = connections.find(c => ownerConnectionID(owner, c.id) === selected);
      const next = connections.find(c => ownerConnectionID(owner, c.id) === id);
      if (!frozen && next?.enabled && next.status === 'ready') onSelect(id, !!previous && previous.kind === next.kind);
    },
  } : undefined;
  const status = supported && inventory.error
    ? <InlineAlert onRetry={() => { void inventory.refetch(); }}>Could not load platform connections. Retry before selecting an account.</InlineAlert>
    : catalog?.error ? <InlineAlert onRetry={catalog.retry}>{catalog.error}</InlineAlert>
    : (supported && inventory.isPending) || (catalog && !catalog.ready) ? <LoadingState>Loading models...</LoadingState> : undefined;
  const modelPicker: ModelPickerContext | undefined = tabs || status ? { tabs, status } : undefined;
  return { platformForModel, modelPicker, named: !!connections };
}
