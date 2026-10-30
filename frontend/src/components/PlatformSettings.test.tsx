// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { PlatformSettings } from './PlatformSettings';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.useRealTimers());

it('shows adapter installation help when enabling Codex without installing anything automatically', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockResolvedValue({ ready: false, installable: true, checks: [] });
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  expect(screen.queryByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0')).toBeNull();
  fireEvent.click(codex);
  expect(await screen.findByRole('dialog', { name: 'Set up Codex' })).toBeVisible();
  expect(codex).not.toBeChecked();
  expect(screen.getByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0')).toBeVisible();
  expect(screen.getByText(/Node.js 22/)).toBeVisible();
  expect(screen.getByText(/PNPM_HOME/)).toBeVisible();
  expect(postJSON).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(codex).not.toBeChecked();
  expect(fetchJSON).toHaveBeenCalledTimes(3);
});

it('loads saved platforms and applies a desktop selection', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['opencode', 'claudecode'] });
  vi.mocked(postJSON).mockResolvedValue({ enabled: ['claudecode'] });
  render(<PlatformSettings />);
  const openCode = screen.getByRole('checkbox', { name: 'OpenCode' });
  const claude = screen.getByRole('checkbox', { name: 'Claude Code' });
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(openCode).toBeEnabled());
  expect(openCode).toBeChecked();
  expect(claude).toBeChecked();
  expect(codex).not.toBeChecked();
  fireEvent.click(openCode);
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('status')).toHaveTextContent('Platform changes applied');
  expect(postJSON).toHaveBeenCalledWith('/api/settings/platforms', { enabled: ['claudecode'] });
  fireEvent.click(openCode);
  expect(screen.queryByRole('status')).toBeNull();
  fireEvent.click(openCode);
  fireEvent.click(claude);
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  expect(screen.getByRole('alert')).toHaveTextContent('Enable at least one platform');
});

it('enables Codex immediately when dependencies are already available', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockResolvedValue({ ready: true });
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  fireEvent.click(codex);
  await waitFor(() => expect(codex).toBeChecked());
  expect(screen.queryByRole('dialog')).toBeNull();
  expect(postJSON).not.toHaveBeenCalled();
  fireEvent.click(codex);
  expect(codex).not.toBeChecked();
});

it('installs explicitly, checks again and revalidates on close before selecting Codex', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockResolvedValue({ ready: false, installable: true, checks: [{ id: 'adapter', label: 'Adapter', ok: false, detail: 'Missing', hint: 'Install it' }] });
  vi.mocked(postJSON).mockResolvedValue({ ready: true, installable: true, checks: [] });
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  fireEvent.click(codex);
  fireEvent.click(await screen.findByRole('button', { name: 'Install' }));
  expect(await screen.findByText('Dependencies are available. Close to enable Codex.')).toBeVisible();
  expect(postJSON).toHaveBeenCalledWith('/api/settings/platforms/codex/install', {});
  expect(codex).not.toBeChecked();
  vi.mocked(fetchJSON).mockResolvedValue({ ready: true, installable: true, checks: [{ id: 'adapter', label: 'Adapter', ok: true, detail: '/adapter' }] });
  fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
  expect(await screen.findByText('Adapter: Available')).toBeVisible();
  fireEvent.click(screen.getByRole('button', { name: 'Close Codex setup' }));
  await waitFor(() => expect(codex).toBeChecked());
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('fails closed on check and installation errors, including Escape dismissal', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockRejectedValue(new Error('check offline'));
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  fireEvent.click(codex);
  expect(await screen.findByRole('alert')).toHaveTextContent('check offline');
  expect(screen.getByRole('button', { name: 'Install' })).toBeDisabled();
  vi.mocked(fetchJSON).mockResolvedValue({ ready: false, installable: true, checks: [] });
  fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
  const install = screen.getByRole('button', { name: 'Install' });
  await waitFor(() => expect(install).toBeEnabled());
  vi.mocked(postJSON).mockRejectedValue(new Error('install failed'));
  fireEvent.click(install);
  expect(await screen.findByRole('alert')).toHaveTextContent('install failed');
  vi.mocked(fetchJSON).mockRejectedValue('offline');
  fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not check Codex dependencies');
  fireEvent.keyDown(document, { key: 'Escape' });
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(codex).not.toBeChecked();
});

it('does not trust an earlier successful check when dependencies disappear before close', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockRejectedValueOnce('offline')
    .mockResolvedValueOnce({ ready: true, installable: false, checks: [] })
    .mockResolvedValue({ ready: false, installable: false, checks: [] });
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  fireEvent.click(codex);
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not check Codex dependencies');
  fireEvent.click(screen.getByRole('button', { name: 'Check again' }));
  await screen.findByText('Dependencies are available. Close to enable Codex.');
  fireEvent.click(screen.getByRole('button', { name: 'Close' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(codex).not.toBeChecked();
});

it('blocks saving, closing and duplicate installs while an installation is pending', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ enabled: ['opencode'] }).mockResolvedValue({ ready: false, installable: true, checks: [] });
  let finish!: (value: unknown) => void;
  vi.mocked(postJSON).mockReturnValue(new Promise(resolve => { finish = resolve; }));
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  fireEvent.click(codex);
  const install = await screen.findByRole('button', { name: 'Install' });
  fireEvent.click(install);
  fireEvent.click(install);
  expect(screen.getByRole('button', { name: 'Close' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Save', hidden: true })).toBeDisabled();
  fireEvent.keyDown(document, { key: 'Escape' });
  expect(screen.getByRole('dialog')).toBeVisible();
  expect(postJSON).toHaveBeenCalledTimes(1);
  await act(async () => { finish({ ready: false, installable: true, checks: [] }); });
  expect(screen.getByRole('button', { name: 'Close' })).toBeEnabled();
});

it('waits for backend reload and tolerates its brief reconnect gap', async () => {
  vi.useFakeTimers();
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['opencode', 'claudecode'] });
  vi.mocked(postJSON).mockResolvedValue({ enabled: ['claudecode'], reloadPending: true });
  render(<PlatformSettings />);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  fireEvent.click(screen.getByRole('checkbox', { name: 'OpenCode' }));
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByRole('status')).toHaveTextContent('Applying after active turns');
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('reconnecting'));
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByRole('status')).toHaveTextContent('Applying after active turns');
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['claudecode'], reloadPending: false });
  await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
  expect(screen.getByRole('status')).toHaveTextContent('Platform changes applied');
});

it('explains an explicit CLI override and disables the toggles', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['opencode'], overridden: true });
  render(<PlatformSettings />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Remove the explicit -platforms flag');
  expect(screen.getByRole('checkbox', { name: 'OpenCode' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});

it.each([new Error('load failed'), 'offline'])('reports load failures without enabling controls: %s', async (error) => {
  vi.mocked(fetchJSON).mockRejectedValue(error);
  render(<PlatformSettings />);
  expect(await screen.findByRole('alert')).toHaveTextContent(error instanceof Error ? error.message : 'Failed to load platforms');
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});

it.each([new Error('save failed'), 'offline'])('preserves the selection when saving fails: %s', async (error) => {
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['opencode', 'claudecode'] });
  vi.mocked(postJSON).mockRejectedValue(error);
  render(<PlatformSettings />);
  const openCode = screen.getByRole('checkbox', { name: 'OpenCode' });
  await waitFor(() => expect(openCode).toBeEnabled());
  fireEvent.click(openCode);
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toHaveTextContent(error instanceof Error ? error.message : 'Failed to save platforms');
  await waitFor(() => expect(openCode).toBeEnabled());
  expect(openCode).not.toBeChecked();
  expect(screen.queryByRole('status')).toBeNull();
});
