import { useState } from 'react';
import { Route, Routes, useParams } from 'react-router-dom';
import {
  Button, ButtonGroup, CodeBlock, DataTable, EmptyState, ErrorState, LoadingState, Page, Pill, Properties, RouteButton,
  SearchField, Section, SeverityPill, usePluginAction, usePluginCall, usePluginMutation, type PillTone, type Severity,
} from '@ocman/plugin-ui';
import './catalog.css';

type Item = { id: string; name: string; category: string; status: 'draft' | 'active' | 'retired'; priority: Severity; owner: string; notes: string };

const tones: Record<Item['status'], PillTone> = { draft: 'warning', active: 'success', retired: 'neutral' };

function StatusPill({ status }: { status: Item['status'] }) {
  return <Pill tone={tones[status]}>{status}</Pill>;
}

function ItemList() {
  const [query, setQuery] = useState('');
  const items = usePluginCall<{ items: Item[] }>('items.list', { query }, { refetchInterval: 30_000 });
  const reset = usePluginAction('reset');
  return <Page title="Catalog" description="A synthetic inventory rendered by the plugin's own React UI."
    actions={<Button variant="accent" disabled={reset.pending} onClick={() => void reset.run()}>Restore sample catalog</Button>}>
    <Section title="Items" actions={<SearchField aria-label="Filter items" placeholder="Filter by name, category or owner" value={query} onChange={(e) => setQuery(e.target.value)} />}>
      {items.isPending ? <LoadingState>Loading items…</LoadingState>
        : items.isError ? <ErrorState inline title="Could not load items" description="Check that the plugin is enabled." />
          : items.data.items.length === 0 ? <EmptyState>No items match this filter.</EmptyState>
            : <DataTable framed aria-label="Catalog items" className="catalog-table">
              <thead><tr><th scope="col">Name</th><th scope="col">Category</th><th scope="col">Owner</th><th scope="col">Priority</th><th scope="col">Status</th></tr></thead>
              <tbody>{items.data.items.map((item) => <tr key={item.id}>
                <td><RouteButton variant="link" to={`items/${item.id}`}>{item.name}</RouteButton></td>
                <td>{item.category}</td><td>{item.owner}</td>
                <td><SeverityPill severity={item.priority} /></td><td><StatusPill status={item.status} /></td>
              </tr>)}</tbody>
            </DataTable>}
    </Section>
  </Page>;
}

function ItemDetail() {
  const { id = '' } = useParams();
  const item = usePluginCall<Item>('items.get', { id });
  const setStatus = usePluginMutation<Item, { id: string; status: Item['status'] }>('items.setStatus');
  const back = <RouteButton to="..">Back to items</RouteButton>;
  if (item.isPending) return <LoadingState>Loading item…</LoadingState>;
  if (item.isError) return <Page title="Item unavailable" actions={back}><ErrorState inline title="Item not found" description="It may have been removed." /></Page>;
  const current = item.data;
  return <Page title={current.name} description={current.category} actions={back}>
    <Section title="Details">
      <Properties items={[
        { label: 'Owner', value: current.owner },
        { label: 'Priority', value: <SeverityPill severity={current.priority} /> },
        { label: 'Status', value: <StatusPill status={current.status} /> },
        { label: 'Notes', value: current.notes },
      ]} />
    </Section>
    <Section title="Change status" description="Calls a plugin-defined method; the list refreshes afterwards.">
      <ButtonGroup label="Item status">
        {(['draft', 'active', 'retired'] as const).map((status) => <Button key={status} disabled={setStatus.isPending || current.status === status}
          onClick={() => setStatus.mutate({ id: current.id, status })}>Mark {status}</Button>)}
      </ButtonGroup>
      {setStatus.isError && <p role="alert">Could not change the status.</p>}
    </Section>
    <Section title="Raw record"><CodeBlock language="json">{JSON.stringify(current, null, 2)}</CodeBlock></Section>
  </Page>;
}

export default function Index() {
  return <Routes>
    <Route index element={<ItemList />} />
    <Route path="items/:id" element={<ItemDetail />} />
    <Route path="*" element={<EmptyState>Unknown catalog page.</EmptyState>} />
  </Routes>;
}
