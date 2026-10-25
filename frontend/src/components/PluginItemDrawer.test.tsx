// @vitest-environment jsdom
import { render, screen, within, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { beforeEach, expect, it, vi } from 'vitest';
import { PluginPageContent } from './PluginPageContent';
import type { PageDocument } from '../lib/pluginPages';

const mocks = vi.hoisted(() => ({ call: vi.fn() }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(), callPluginPage: mocks.call }));

beforeEach(() => { mocks.call.mockReset(); });
function Location() { const location = useLocation(); return <output aria-label="Route">{location.pathname}{location.search}</output>; }
function mount({ summary = true, disabled = false, configuration = false } = {}) {
  const document = { revision: 'r1', fields: [], values: {}, tree: { available: false }, collection: {
    title: 'Findings', detailPresentation: 'drawer', columns: [{ key: 'name', label: 'Name', display: summary ? 'summary' : 'text' }],
    rows: [{ id: 'f1', ...(configuration ? { projectId: 'config-project' } : {}), values: { name: summary ? { label: 'Missing authorization', description: 'auth.go:12' } : 'Missing authorization' } }], nextCursor: 'next',
  } } as PageDocument;
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
    <MemoryRouter initialEntries={['/plugins/example/runs?ownerId=remote']}><Location />
      <PluginPageContent page={{ pluginId: 'example', ownerId: 'remote', page: { id: 'runs', label: 'Runs' } }} projectId="p1" document={document} disabled={disabled} cursor="page2" onCursor={() => {}} />
    </MemoryRouter>
  </QueryClientProvider>);
}

it('opens finding details without replacing the list or changing its route, then restores focus', async () => {
  mocks.call.mockResolvedValue({ detail: { title: 'Missing authorization', properties: [
    { label: 'Severity', display: 'severity', value: 'high' },
    { label: 'Illustrative example', display: 'text', value: 'A user requests another account\'s record.' },
    { label: 'Potential fixes', display: 'text', value: 'Enforce object ownership on every request.' },
  ] } });
  mount();
  expect(mocks.call).not.toHaveBeenCalled();
  const opener = screen.getByRole('button', { name: 'View Missing authorization' });
  await userEvent.click(opener);
  const drawer = await screen.findByRole('dialog', { name: 'Missing authorization' });
  expect(await within(drawer).findByText('Enforce object ownership on every request.')).toBeInTheDocument();
  expect(within(drawer).getByText('high')).toHaveClass('plugin-severity-high');
  expect(screen.getByRole('table', { hidden: true })).toBeInTheDocument();
  expect(screen.getByLabelText('Route')).toHaveTextContent('/plugins/example/runs?ownerId=remote');
  expect(mocks.call).toHaveBeenCalledWith(expect.objectContaining({ ownerId: 'remote', pluginId: 'example' }), 'p1', 'read', undefined, expect.any(AbortSignal), { itemId: 'f1' });
  await userEvent.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(opener).toHaveFocus();
  expect(screen.getByRole('button', { name: 'First page' })).toBeInTheDocument();
});

it('shows a safe error and can retry a failed detail read', async () => {
  mocks.call.mockRejectedValueOnce(new Error('private owner diagnostics'));
  mount();
  await userEvent.click(screen.getByRole('button', { name: 'View Missing authorization' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load item details.');
  expect(screen.queryByText('private owner diagnostics')).not.toBeInTheDocument();
  mocks.call.mockResolvedValue({ detail: { title: 'Missing authorization', properties: [] } });
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  await userEvent.click(screen.getByRole('button', { name: 'Close item details' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('aborts an outstanding read when the drawer closes', async () => {
  let signal: AbortSignal | undefined;
  mocks.call.mockImplementation((_page, _project, _method, _document, requestSignal) => { signal = requestSignal; return new Promise(() => {}); });
  mount();
  await userEvent.click(screen.getByRole('button', { name: 'View Missing authorization' }));
  expect(await within(screen.getByRole('dialog')).findByRole('status')).toHaveTextContent('Loading item details');
  await userEvent.click(screen.getByRole('button', { name: 'Close item details' }));
  expect(signal?.aborted).toBe(true);
});

it('supports ordinary text collections and missing detail responses', async () => {
  mocks.call.mockResolvedValue({});
  mount({ summary: false });
  await userEvent.click(screen.getByRole('button', { name: 'View' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not load item details.');
});

it('does not open a drawer for a disabled collection', () => {
  mount({ disabled: true });
  expect(screen.queryByRole('button', { name: 'View Missing authorization' })).not.toBeInTheDocument();
  expect(mocks.call).not.toHaveBeenCalled();
});

it('keeps project configuration rows as navigation links', () => {
  mount({ configuration: true });
  expect(screen.getByRole('link', { name: 'View f1' })).toHaveAttribute('href', '/plugins/example/runs?ownerId=remote&projectId=config-project');
  expect(mocks.call).not.toHaveBeenCalled();
});
