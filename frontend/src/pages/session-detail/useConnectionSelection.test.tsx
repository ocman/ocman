// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { queryClient } from '../../lib/queryClient';
import { useConnectionSelection } from './useConnectionSelection';

const { machinePlatforms } = vi.hoisted(() => ({ machinePlatforms: vi.fn() }));
vi.mock('../../lib/useCapabilities', () => ({ useMachinePlatforms: machinePlatforms }));
const connections = [
  { id: 'cnx_personal', kind: 'codex', name: 'Personal', enabled: true, status: 'ready' },
  { id: 'cnx_work', kind: 'codex', name: 'Work', enabled: true, status: 'ready' },
  { id: 'cnx_disabled', kind: 'codex', name: 'Disabled', enabled: false, status: 'ready' },
  { id: 'cnx_login', kind: 'codex', name: 'Login', enabled: true, status: 'sign-in-required' },
];
const inventory = { supported: true, connections };
const json = (body: unknown) => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
function Selection({ onSelect = vi.fn(), frozen = false }: { onSelect?: (id: string) => void; frozen?: boolean }) {
  const { selector, platformForModel } = useConnectionSelection('box', 'r-box:cnx_work', onSelect, frozen);
  return <>{selector}<output>{platformForModel('codex:openai/model')}</output></>;
}
beforeEach(() => { queryClient.clear(); machinePlatforms.mockReturnValue([{ id: 'r-box:cnx_work', kind: 'codex' }]); });
afterEach(() => { vi.unstubAllGlobals(); queryClient.clear(); });

describe('owner connection selection controls', () => {
  it('qualifies explicit choices for their owner, preserves model routing and disables unavailable accounts', async () => {
    vi.stubGlobal('fetch', vi.fn(async (url: string) => {
      expect(url).toBe('/api/platform-connections?remoteId=box');
      return json(inventory);
    }));
    const onSelect = vi.fn();
    const view = render(<Selection onSelect={onSelect} />);
    const selector = await screen.findByRole('combobox', { name: 'Platform connection' });
    expect(selector).toHaveValue('r-box:cnx_work');
    expect(screen.getByRole('status')).toHaveTextContent('r-box:cnx_work');
    expect(screen.getByRole('option', { name: 'Disabled (disabled)' })).toBeDisabled();
    expect(screen.getByRole('option', { name: 'Login (setup required)' })).toBeDisabled();
    await userEvent.setup().selectOptions(selector, 'r-box:cnx_personal');
    expect(onSelect).toHaveBeenCalledExactlyOnceWith('r-box:cnx_personal');
    view.rerender(<Selection onSelect={onSelect} frozen />);
    expect(selector).toBeDisabled();
  });

  it('offers retry on inventory failure without replacing the selected account with a sibling', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(new Response('Owner unavailable', { status: 503 })).mockResolvedValue(json(inventory));
    vi.stubGlobal('fetch', fetch);
    render(<Selection />);
    expect(await screen.findByText(/Could not load platform connections/)).toBeVisible();
    await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByRole('combobox', { name: 'Platform connection' })).toHaveValue('r-box:cnx_work');
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('keeps named controls hidden when the owner reports unsupported', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ supported: false, connections: [] })));
    render(<Selection />);
    expect(await screen.findByRole('status')).toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Platform connection' })).not.toBeInTheDocument();
  });
});
