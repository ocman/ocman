// @vitest-environment jsdom
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { queryClient } from '../../lib/queryClient';
import { ModelPicker } from '../../components/assistant/ModelPicker';
import { useConnectionSelection } from './useConnectionSelection';

const { machinePlatforms } = vi.hoisted(() => ({ machinePlatforms: vi.fn() }));
vi.mock('../../lib/useCapabilities', () => ({ useMachinePlatforms: machinePlatforms }));
const connections = [
  { id: 'cnx_personal', kind: 'codex', name: 'Personal', enabled: true, status: 'ready' },
  { id: 'cnx_work', kind: 'codex', name: 'Work', enabled: true, status: 'ready' },
  { id: 'cnx_disabled', kind: 'codex', name: 'Disabled', enabled: false, status: 'ready' },
  { id: 'cnx_login', kind: 'codex', name: 'Login', enabled: true, status: 'sign-in-required' },
  { id: 'cnx_error', kind: 'codex', name: 'Broken', enabled: true, status: 'error' },
  { id: 'cnx_disabled_error', kind: 'codex', name: 'Disabled Broken', enabled: false, status: 'error' },
  { id: 'cnx_claude', kind: 'claudecode', name: 'Claude', enabled: true, status: 'ready' },
];
const inventory = { supported: true, connections };
const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
function Selection({ onSelect = vi.fn(), onModel = vi.fn(), frozen = false, catalog }: {
  onSelect?: (id: string, compatible: boolean) => void; onModel?: (model: string) => void; frozen?: boolean;
  catalog?: { ready: boolean; error: string; retry: () => void };
}) {
  const { platformForModel, modelPicker } = useConnectionSelection('box', 'r-box:cnx_work', onSelect, frozen, catalog);
  return <><output data-testid="route">{platformForModel('codex:openai/model')}</output>
    <ModelPicker open models={[]} modelEntries={[{ platform: 'codex', provider: 'openai', model: 'model' }]}
      context={modelPicker} onSelect={onModel} onClose={vi.fn()} /></>;
}
beforeEach(() => {
  queryClient.clear();
  machinePlatforms.mockReturnValue([{ id: 'r-box:cnx_work', kind: 'codex' }]);
  Element.prototype.scrollIntoView = vi.fn();
});
afterEach(() => { vi.unstubAllGlobals(); queryClient.clear(); });

describe('named platforms in the model picker', () => {
  it('uses named tabs instead of a connection dropdown and disables unavailable accounts', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      expect(url).toBe('/api/platform-connections?remoteId=box');
      return json(inventory);
    }));
    const onSelect = vi.fn();
    const onModel = vi.fn();
    const view = render(<Selection onSelect={onSelect} onModel={onModel} />);
    expect(await screen.findByRole('tab', { name: 'Work' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('combobox', { name: 'Platform connection' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab', { name: 'All' })).not.toBeInTheDocument();
    expect(screen.getByTestId('route')).toHaveTextContent('r-box:cnx_work');
    for (const label of ['Disabled (Disabled)', 'Login (Sign-in required)', 'Broken (Check failed)', 'Disabled Broken (Disabled · Check failed)']) {
      expect(screen.getByRole('tab', { name: label })).toBeDisabled();
    }
    const user = userEvent.setup();
    await user.click(screen.getByRole('tab', { name: 'Personal' }));
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('r-box:cnx_personal', true);
    await user.click(screen.getByRole('tab', { name: 'Claude' }));
    expect(onSelect).toHaveBeenLastCalledWith('r-box:cnx_claude', false);
    await user.click(screen.getByRole('option'));
    expect(onModel).toHaveBeenCalledWith('codex:openai/model');
    view.rerender(<Selection onSelect={onSelect} frozen />);
    expect(screen.getByRole('tab', { name: 'Personal' })).toBeDisabled();
  });

  it('offers retry on inventory failure without replacing the selected account with a sibling', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response('Owner unavailable', { status: 503 })).mockResolvedValue(json(inventory));
    vi.stubGlobal('fetch', fetch);
    render(<Selection />);
    expect(await screen.findByText(/Could not load platform connections/)).toBeVisible();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('tab', { name: 'Work' })).toHaveAttribute('aria-selected', 'true');
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps legacy model tabs when the owner reports unsupported', async () => {
    const fetch = vi.fn(async () => json({ supported: false, connections: [] }));
    vi.stubGlobal('fetch', fetch);
    render(<Selection />);
    await waitFor(() => expect(queryClient.getQueryData(['platform-connections', 'box'])).toEqual({ supported: false, connections: [] }));
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
  });

  it('keeps the account tabs available during catalog failure and retry without selectable stale models', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json(inventory)));
    const retry = vi.fn();
    const view = render(<Selection catalog={{ ready: false, error: 'Account catalog unavailable', retry }} />);
    expect(await screen.findByRole('tab', { name: 'Personal' })).toBeEnabled();
    expect(screen.getByRole('alert')).toHaveTextContent('Account catalog unavailable');
    expect(screen.queryByRole('option')).not.toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }));
    expect(retry).toHaveBeenCalledOnce();
    view.rerender(<Selection catalog={{ ready: false, error: '', retry }} />);
    expect(screen.getByText('Loading models...')).toBeVisible();
    expect(screen.queryByText('No models found')).not.toBeInTheDocument();
    view.rerender(<Selection catalog={{ ready: true, error: '', retry }} />);
    expect(screen.getByRole('option')).toBeVisible();
  });
});
