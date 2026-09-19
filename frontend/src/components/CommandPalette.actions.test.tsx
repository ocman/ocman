// @vitest-environment jsdom
import { StrictMode } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import type { PluginAction, PluginActionRequest } from '../lib/plugins';
import { useUiStore } from '../lib/uiStore';
import { CommandPalette } from './CommandPalette';

const mocks = vi.hoisted(() => ({
  cachedSessions: [] as Session[],
  refreshCachedSessions: vi.fn(async () => []),
}));
vi.mock('../lib/apiStore', () => ({ useApiStore: (selector: (state: typeof mocks) => unknown) => selector(mocks) }));
vi.mock('../lib/useCapabilities', () => ({ useOpencodeLaunch: () => false }));
vi.mock('../lib/useTmux', () => ({ useTmux: () => ({ available: false }) }));

const contribution = (placement: PluginAction['action']['placement'] = 'global', pluginId = 'org.example.report'): PluginAction => ({
  pluginId, ownerId: 'local', scope: 'hub',
  action: { id: 'report', label: 'Create report', placement, confirmation: 'Create this report?' },
});
const fetchMock = vi.fn<typeof fetch>();
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
let invoke: (request: PluginActionRequest) => Response | Promise<Response>;
let list: (params: URLSearchParams) => Response | Promise<Response>;
let requests: PluginActionRequest[];

function Location() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="Current route">{location.pathname}</output><button onClick={() => navigate('/projects')}>Change context</button></>;
}
function renderPalette(path = '/sessions', strict = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const invalidate = vi.spyOn(client, 'invalidateQueries');
  const tree = <QueryClientProvider client={client}><MemoryRouter initialEntries={[path]}><CommandPalette /><Location /></MemoryRouter></QueryClientProvider>;
  render(strict ? <StrictMode>{tree}</StrictMode> : tree);
  return { client, invalidate };
}
async function selectAction(query = 'Create report') {
  fireEvent.change(screen.getByRole('combobox'), { target: { value: `>${query}` } });
  await screen.findByRole('option', { name: new RegExp(query) });
  fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
  return screen.findByRole('dialog', { name: query });
}

beforeEach(() => {
  vi.clearAllMocks();
  Element.prototype.scrollIntoView = vi.fn();
  useUiStore.getState().openCommandPalette();
  mocks.cachedSessions = [];
  requests = [];
  list = () => json([contribution()]);
  invoke = () => json({ results: [{ kind: 'notice', text: 'Report ready' }] });
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input), 'http://localhost');
    if (url.pathname === '/api/plugins/actions/invoke') {
      const request = JSON.parse(String(init?.body)) as PluginActionRequest;
      requests.push(request);
      return invoke(request);
    }
    if (url.pathname === '/api/plugins/actions') return list(url.searchParams);
    throw new Error(`Unexpected request: ${url}`);
  });
  vi.stubGlobal('fetch', fetchMock);
});
afterEach(() => vi.unstubAllGlobals());

describe('plugin command contributions', () => {
  it('discovers, filters, confirms with the same operation, and renders text, links and owner-routed downloads', async () => {
    invoke = (request) => request.confirmationToken
      ? json({ results: [
        { kind: 'notice', text: '<b>Report ready</b>' },
        { kind: 'link', label: 'View report', url: 'https://example.org/report' },
        { kind: 'artifact', label: 'report.txt', handle: 'handle&one' },
      ] })
      : json({ confirmation: { text: 'Create this report?', token: 'confirmed', expiresAt: Date.now() + 300_000 } }, 409);
    renderPalette('/sessions', true);
    expect(await screen.findByRole('option', { name: /Create report/ })).toBeTruthy();
    const dialog = await selectAction();
    const confirm = await within(dialog).findByRole('button', { name: 'Confirm' });
    expect(requests).toHaveLength(1);
    expect(within(dialog).getByText('Create this report?')).toBeTruthy();
    expect(document.activeElement?.closest('[role="dialog"]')).toBe(dialog);
    fireEvent.click(confirm);
    expect(await within(dialog).findByText('<b>Report ready</b>')).toBeTruthy();
    expect(dialog.querySelector('b')).toBeNull();
    expect(requests).toHaveLength(2);
    expect(requests[1]).toEqual({ ...requests[0], confirmationToken: 'confirmed' });
    expect(requests[0]).toMatchObject({ ownerId: 'local', surface: 'command-palette', context: { ownerId: 'local', route: 'sessions' } });
    const link = within(dialog).getByRole('link', { name: 'View report' });
    expect(link.getAttribute('href')).toBe('https://example.org/report');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    expect(within(dialog).getByRole('link', { name: 'report.txt' }).getAttribute('href')).toBe('/api/plugins/actions/artifact?ownerId=local&handle=handle%26one');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it('cancels confirmation without invoking the plugin', async () => {
    invoke = () => json({ confirmation: { text: 'Proceed?', token: 'token', expiresAt: Date.now() + 300_000 } }, 409);
    renderPalette();
    const dialog = await selectAction();
    fireEvent.click(await within(dialog).findByRole('button', { name: 'Cancel' }));
    expect(requests).toHaveLength(1);
    expect(screen.queryByRole('dialog')).toBeNull();
  });

  it.each([
    [403, 'denied'], [503, 'unavailable'], [504, 'timed out'], [500, 'failed'],
  ])('handles HTTP %s without retrying', async (status, text) => {
    invoke = () => json({ error: { category: 'internal' } }, status);
    renderPalette();
    await selectAction();
    expect((await screen.findByRole('alert')).textContent).toContain(text);
    expect(requests).toHaveLength(1);
  });

  it('handles an expired or conflicting confirmation', async () => {
    invoke = () => json({ error: { category: 'conflict' } }, 409);
    renderPalette();
    await selectAction();
    expect((await screen.findByRole('alert')).textContent).toContain('not be repeated safely');
  });

  it('handles a browser timeout without retrying', async () => {
    invoke = () => Promise.reject(new DOMException('timeout', 'TimeoutError'));
    renderPalette();
    await selectAction();
    expect((await screen.findByRole('alert')).textContent).toContain('timed out');
    expect(requests).toHaveLength(1);
  });

  it('applies refresh hints and core navigation', async () => {
    invoke = () => json({ results: [
      { kind: 'refresh', target: 'actions' }, { kind: 'refresh', target: 'sessions' },
      { kind: 'refresh', target: 'projects' }, { kind: 'navigation', target: 'settings' },
    ] });
    const { invalidate } = renderPalette();
    await selectAction();
    await screen.findByText('Action completed.');
    await waitFor(() => expect(screen.getByLabelText('Current route').textContent).toBe('/settings'));
    for (const key of ['plugin-actions', 'sessions', 'projects']) expect(invalidate).toHaveBeenCalledWith({ queryKey: [key] });
    expect(mocks.refreshCachedSessions).toHaveBeenCalledTimes(1);
  });

  it.each(['project', 'session'])('resolves an uncached current %s independently of recent sessions', async (route) => {
    const current = { id: 'old-session', projectId: 'old-project', directory: '/old', remoteId: 'owner-2' };
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), 'http://localhost');
      if (url.pathname === '/api/session/old-session') return json({ session: current, messages: [], parts: [] });
      if (url.pathname === '/api/sessions' && url.searchParams.get('dir') === '/old') return json([
        { ...current, id: 'pinned-other', projectId: 'other-project', directory: '/other', pinned: true }, current,
      ]);
      if (url.pathname === '/api/plugins/actions') {
        const placement = url.searchParams.get('placement');
        if (placement === 'global') return json([]);
        expect(url.searchParams.get('projectId')).toBe('old-project');
        expect(url.searchParams.get('ownerId')).toBe('owner-2');
        return json([{ ...contribution(), action: { id: placement, label: `${placement} old action`, placement } }]);
      }
      throw new Error(`Unexpected request: ${url}`);
    });
    renderPalette(route === 'session' ? '/session/old-session' : '/project/%2Fold?remoteId=owner-2');
    expect(await screen.findByRole('option', { name: new RegExp(`${route} old action`) })).toBeTruthy();
  });

  it('keeps focus in the dialog and waits for the outcome after confirmation', async () => {
    let resolve!: (response: Response) => void;
    invoke = (request) => request.confirmationToken
      ? new Promise<Response>((done) => { resolve = done; })
      : json({ confirmation: { text: 'Proceed?', token: 'token', expiresAt: Date.now() + 300_000 } }, 409);
    renderPalette();
    const dialog = await selectAction();
    const confirm = await within(dialog).findByRole('button', { name: 'Confirm' });
    confirm.focus();
    fireEvent.click(confirm);
    expect(dialog.contains(document.activeElement)).toBe(true);
    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByRole('dialog')).toBe(dialog);
    expect(within(dialog).queryByRole('button', { name: 'Close' })).toBeNull();
    await act(async () => resolve(json({ results: [{ kind: 'notice', text: 'Finished' }] })));
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeTruthy();
  });

  it.each(['project', 'session'])('merges global and %s actions using opaque context identities', async (route) => {
    mocks.cachedSessions = [{ id: 'ses-1', projectId: 'proj-1', directory: '/private/work', remoteId: 'owner-1' } as Session];
    const placements: string[] = [];
    list = (params) => {
      placements.push(params.get('placement')!);
      expect(params.get('projectId')).toBe('proj-1');
      expect(params.get('ownerId')).toBe('owner-1');
      expect(params.get('route')).toBe(route);
      return json([{ ...contribution(params.get('placement') as PluginAction['action']['placement']), ownerId: 'owner-1', action: { id: `${params.get('placement')}-report`, placement: params.get('placement'), label: `${params.get('placement')} report` } }]);
    };
    renderPalette(route === 'session' ? '/session/ses-1' : '/project/%2Fprivate%2Fwork?remoteId=owner-1');
    await screen.findByRole('option', { name: new RegExp(`${route} report`) });
    expect(placements.sort()).toEqual(route === 'session' ? ['global', 'project', 'session'] : ['global', 'project']);
    fireEvent.click(screen.getByRole('option', { name: new RegExp(`${route} report`) }));
    await screen.findByText('Report ready');
    expect(requests[0].context.projectId).toBe('proj-1');
    expect(requests[0].ownerId).toBe('owner-1');
    expect(JSON.stringify(requests[0])).not.toContain('/private/work');
  });

  it('keeps identically labelled contributions discoverable', async () => {
    list = () => json([contribution(), contribution('global', 'org.example.other')]);
    renderPalette();
    expect(await screen.findAllByRole('option', { name: /Create report/ })).toHaveLength(2);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'Create report' } });
    expect(screen.getAllByRole('option', { name: /Create report/ })).toHaveLength(2);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '>Create report' } });
    expect(screen.getAllByRole('option', { name: /Create report/ })).toHaveLength(2);
  });

  it('leaves core commands usable when discovery is unavailable', async () => {
    list = () => json({ error: { category: 'unavailable' } }, 503);
    renderPalette();
    expect(await screen.findByText('Some plugin actions are unavailable.')).toBeTruthy();
    expect(screen.getByRole('option', { name: /sessions.*Go to Sessions/ })).toBeTruthy();
  });

  it('ignores discovery from a previous context and rechecks on reopen', async () => {
    let resolve!: (response: Response) => void;
    list = (params) => params.get('route') === 'sessions'
      ? new Promise<Response>((done) => { resolve = done; }) : json([]);
    renderPalette();
    await waitFor(() => expect(resolve).toBeTypeOf('function'));
    fireEvent.click(screen.getByRole('button', { name: 'Change context' }));
    await act(async () => resolve(json([contribution()])));
    expect(screen.queryByRole('option', { name: /Create report/ })).toBeNull();
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Escape' });
    list = () => json([contribution()]);
    act(() => useUiStore.getState().openCommandPalette());
    expect(await screen.findByRole('option', { name: /Create report/ })).toBeTruthy();
  });
});
