// @vitest-environment jsdom
import contract from '../../../sdk/plugin-ui/index.d.ts?raw';
import packageText from '../../../sdk/plugin-ui/package.json?raw';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { expect, it, vi } from 'vitest';
import * as PluginUI from './index';
import { PluginHostContext, type PluginHostValue } from './context';

vi.mock('../lib/queries', () => ({ useProjects: () => ({ data: [{ directory: '/srv/alpha' }, { directory: '/srv/remote', remoteId: 'machine' }] }) }));
vi.mock('../lib/useCapabilities', () => ({ useCapabilities: () => ({ platforms: [] }) }));

const packageJSON = JSON.parse(packageText) as { version: string };

it('exposes exactly the published contract at runtime', () => {
  const declared = [...contract.matchAll(/^export declare (?:function|const) (\w+)/gm)].map((m) => m[1]).sort();
  expect(Object.keys(PluginUI).sort()).toEqual(declared);
  expect(PluginUI.PLUGIN_UI_VERSION).toBe(packageJSON.version);
});

const host: PluginHostValue = { pluginId: 'org.example.catalog', ownerId: 'local', basePath: '/plugins/org.example.catalog', runAction: () => Promise.resolve() };

function mount(children: ReactNode) {
  return render(<QueryClientProvider client={new QueryClient()}><MemoryRouter>
    <PluginHostContext.Provider value={host}>{children}</PluginHostContext.Provider>
  </MemoryRouter></QueryClientProvider>);
}

it('renders layout primitives with host structure and styling hooks', () => {
  const { container } = mount(<PluginUI.Page title="Catalog" description="All items" actions={<PluginUI.Button>New</PluginUI.Button>}>
    <PluginUI.Section title="Details" description="Selected item">
      <PluginUI.Properties items={[{ label: 'Owner', value: 'Team A' }, { label: 'Level', value: <PluginUI.SeverityPill severity="high" /> }]} />
      <PluginUI.CodeBlock language="json">{'{"a":1}'}</PluginUI.CodeBlock>
      <PluginUI.Pill tone="success">Active</PluginUI.Pill>
      <PluginUI.Pill>Draft</PluginUI.Pill>
      <PluginUI.SeverityPill severity={'bogus' as never} />
      <PluginUI.StatusBadge status="busy" />
    </PluginUI.Section>
  </PluginUI.Page>);
  expect(screen.getByRole('heading', { level: 1, name: 'Catalog' }).closest('.plugin-page')).not.toBeNull();
  expect(screen.getByRole('heading', { level: 2, name: 'Details' }).closest('.plugin-page-heading')).not.toBeNull();
  expect(screen.getByRole('rowheader', { name: 'Owner' })).toBeInTheDocument();
  expect(container.querySelector('.plugin-severity-high')).toHaveTextContent('high');
  expect(container.querySelector('.plugin-severity-unknown')).not.toBeNull();
  expect(container.querySelector('pre.oc-plugin-code')).toHaveAttribute('data-language', 'json');
  expect(screen.getByText('Active')).toHaveClass('oc-plugin-pill--success');
  expect(screen.getByText('Draft')).toHaveClass('oc-plugin-pill--neutral');
  expect(screen.getByText('Busy')).toBeInTheDocument();
});

it('wraps host controls, tables, states, tabs and overlays', async () => {
  const onClose = vi.fn();
  const submit = vi.fn(() => Promise.resolve());
  mount(<>
    <PluginUI.ButtonGroup label="Controls">
      <PluginUI.SubmitButton onClick={submit}>Save</PluginUI.SubmitButton>
      <PluginUI.RouteButton to="items">Items</PluginUI.RouteButton>
    </PluginUI.ButtonGroup>
    <PluginUI.TextField aria-label="Name" />
    <PluginUI.SearchField aria-label="Search" />
    <PluginUI.SelectField aria-label="Kind"><option>One</option></PluginUI.SelectField>
    <PluginUI.CheckboxField aria-label="Enabled" />
    <PluginUI.TextareaField aria-label="Notes" />
    <PluginUI.DataTable framed aria-label="Items"><tbody><tr><td>Row</td></tr></tbody></PluginUI.DataTable>
    <PluginUI.EmptyState>Nothing yet</PluginUI.EmptyState>
    <PluginUI.LoadingState>Loading items</PluginUI.LoadingState>
    <PluginUI.ErrorState title="Failed" description="Retry later" />
    <PluginUI.Tabs defaultValue="a"><PluginUI.TabsList aria-label="Views"><PluginUI.TabsTrigger value="a">A</PluginUI.TabsTrigger></PluginUI.TabsList>
      <PluginUI.TabsContent value="a">Tab A</PluginUI.TabsContent></PluginUI.Tabs>
    <PluginUI.Drawer title="Item" onClose={onClose}>Drawer body</PluginUI.Drawer>
  </>);
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(submit).toHaveBeenCalledOnce();
  expect(screen.getByRole('link', { name: 'Items' })).toHaveAttribute('href', '/items');
  for (const name of ['Name', 'Search', 'Kind', 'Enabled', 'Notes']) expect(screen.getByLabelText(name)).toBeInTheDocument();
  expect(screen.getByRole('table', { name: 'Items' }).closest('.oc-data-table-frame')).not.toBeNull();
  expect(screen.getByText('Nothing yet')).toHaveClass('oc-empty');
  expect(screen.getByRole('status')).toHaveTextContent('Loading items');
  expect(screen.getByRole('alert')).toHaveTextContent('Failed');
  expect(screen.getByText('Tab A')).toBeInTheDocument();
  expect(screen.getByText('Drawer body')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Close Item' }));
  expect(onClose).toHaveBeenCalled();
});

it('scopes project and model pickers to the plugin owner', () => {
  const onChange = vi.fn();
  mount(<>
    <PluginUI.ProjectPicker label="Projects" value={'/srv/alpha'} onChange={onChange} />
    <PluginUI.ModelPicker label="Model" value="" onChange={onChange} />
  </>);
  expect(screen.getByLabelText('Projects selected projects')).toHaveTextContent('alpha');
  expect(screen.queryByText('Unavailable on this owner')).toBeNull();
  expect(screen.getByText(/No models available on this owner/)).toBeInTheDocument();
});

it('refuses hooks outside a mounted plugin', () => {
  vi.spyOn(console, 'error').mockImplementation(() => {});
  function Orphan() { PluginUI.usePluginContext(); return null; }
  expect(() => render(<Orphan />)).toThrow(/only available inside a mounted plugin UI/);
});
