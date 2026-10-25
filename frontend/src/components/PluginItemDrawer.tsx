import { useQuery } from '@tanstack/react-query';
import { callPluginPage, type PluginPage } from '../lib/pluginPages';
import { Drawer } from './Drawer';
import { Button } from './Control';
import { PluginPageValue } from './PluginPageValue';

export function PluginItemDrawer({ page, projectId, itemId, title, onClose }: {
  page: PluginPage; projectId: string; itemId: string; title: string; onClose: () => void;
}) {
  const query = useQuery({
    queryKey: ['plugin-item-detail', page.ownerId, page.pluginId, page.page.id, projectId, itemId],
    queryFn: ({ signal }) => callPluginPage(page, projectId, 'read', undefined, signal, { itemId }),
    retry: false, gcTime: 0,
  });
  const detail = query.data?.detail;
  return <Drawer title={detail?.title || title} onClose={onClose} closeLabel="Close item details">
    {query.isPending ? <p role="status">Loading item details…</p>
      : query.isError || !detail ? <><p role="alert">Could not load item details. Check that the item still exists and its owner is available.</p>
        <Button onClick={() => void query.refetch()}>Retry</Button></>
        : <dl className="plugin-item-properties">{detail.properties.map((property, i) => <div key={i}>
          <dt>{property.label}</dt><dd><PluginPageValue value={property.value} display={property.display} /></dd>
        </div>)}</dl>}
  </Drawer>;
}
