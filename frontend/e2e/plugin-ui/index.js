// A synthetic plugin UI bundle, written as the output @ocman/plugin-vite
// produces: shared imports point at the host's /plugin-shared/ modules.
import { useState } from '/plugin-shared/react.js';
import { jsx, jsxs, Fragment } from '/plugin-shared/react-jsx-runtime.js';
import { Route, Routes, useParams } from '/plugin-shared/react-router-dom.js';
import {
  Button, ButtonGroup, DataTable, InlineAlert, Page, Pill, Properties, RouteButton, Section, SeverityPill, usePluginAction, usePluginCall, usePluginContext,
  useSessionDraft,
} from '/plugin-shared/ocman-plugin-ui.js';

// Two unrelated workflows handed to the user as editable conversation drafts.
const workflows = [
  { label: 'Draft maintenance task', directory: '/home/user/projects/myapp', title: 'Update deprecated API calls',
    prompt: 'Replace calls to the deprecated client.fetchAll() with client.list() and keep the tests passing.' },
  { label: 'Draft documentation update', directory: '/home/user/projects/myapp', title: 'Refresh the setup guide',
    prompt: 'Update docs/setup.md for the new install command.' },
  { label: 'Draft in unknown project', directory: '/nowhere', title: 'Unavailable' },
];

function Handoff() {
  const draft = useSessionDraft();
  return jsxs(Section, { title: 'Hand off work', children: [
    draft.error ? jsx(InlineAlert, { children: draft.error }) : null,
    jsx(ButtonGroup, { label: 'Draft workflows', children: workflows.map(({ label, ...options }) =>
      jsx(Button, { disabled: draft.pending, onClick: () => void draft.open(options).catch(() => {}), children: label }, label)) }),
  ] });
}

function List() {
  const [clicks, setClicks] = useState(0);
  const items = usePluginCall('items.list', {});
  const reset = usePluginAction('reset');
  const context = usePluginContext();
  return jsxs(Page, { title: 'Catalog', description: `Mounted at ${context.basePath}`,
    actions: jsx(Button, { variant: 'accent', onClick: () => { setClicks(clicks + 1); void reset.run(); }, children: `Restore (${clicks})` }),
    children: [jsx(Section, { title: 'Items', children: jsx(DataTable, { framed: true, 'aria-label': 'Catalog items', children: jsx('tbody', {
      children: (items.data?.items ?? []).map((item) => jsxs('tr', { children: [
        jsx('td', { children: jsx(RouteButton, { variant: 'link', to: `items/${item.id}`, children: item.name }) }),
        jsx('td', { children: jsx(SeverityPill, { severity: item.priority }) }),
        jsx('td', { children: jsx(Pill, { tone: 'success', children: item.status }) }),
      ] }, item.id)),
    }) }) }), jsx(Handoff, {})] });
}

function Detail() {
  const { id } = useParams();
  const item = usePluginCall('items.get', { id });
  return jsx(Page, { title: item.data?.name ?? 'Loading', actions: jsx(RouteButton, { to: '..', children: 'Back to items' }),
    children: item.data ? jsx(Properties, { items: [{ label: 'Owner', value: item.data.owner }] }) : jsx(Fragment, {}) });
}

export default function Index() {
  return jsxs(Routes, { children: [jsx(Route, { index: true, element: jsx(List, {}) }), jsx(Route, { path: 'items/:id', element: jsx(Detail, {}) })] });
}
