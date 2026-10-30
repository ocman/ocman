// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { PlatformSettings } from './PlatformSettings';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => vi.clearAllMocks());
afterEach(() => vi.useRealTimers());

it('shows adapter installation help when enabling Codex without installing anything automatically', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ enabled: ['opencode'] });
  render(<PlatformSettings />);
  const codex = screen.getByRole('checkbox', { name: 'Codex' });
  await waitFor(() => expect(codex).toBeEnabled());
  expect(screen.queryByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0')).toBeNull();
  fireEvent.click(codex);
  expect(screen.getByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0')).toBeVisible();
  expect(screen.getByText(/Node.js 22/)).toBeVisible();
  expect(screen.getByText(/PNPM_HOME/)).toBeVisible();
  expect(postJSON).not.toHaveBeenCalled();
  fireEvent.click(codex);
  expect(screen.queryByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0')).toBeNull();
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
