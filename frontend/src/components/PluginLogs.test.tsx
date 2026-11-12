// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { plugins } from '../lib/plugins';
import { copyTextToClipboard } from '../lib/clipboard';
import { PluginLogs } from './PluginLogs';

vi.mock('../lib/plugins', () => ({ plugins: { stderr: vi.fn() } }));
vi.mock('../lib/clipboard', () => ({ copyTextToClipboard: vi.fn() }));
beforeEach(() => { vi.resetAllMocks(); });

it('reserves a logs panel while loading and copies bounded text after loading', async () => {
  let resolve!: (result: { stderr: string }) => void;
  vi.mocked(plugins.stderr).mockReturnValue(new Promise((done) => { resolve = done; }));
  vi.mocked(copyTextToClipboard).mockResolvedValue(true);
  renderWithQuery(<PluginLogs owner="remote" pluginId="example" />);
  expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('Loading recent logs…');
  expect(screen.getByRole('button', { name: 'Copy logs' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Refresh logs' })).toBeDisabled();
  const text = 'old'.repeat(50000) + '<script>recent</script>';
  await act(async () => resolve({ stderr: text }));
  await waitFor(() => expect(screen.getByLabelText('Recent stderr').textContent).toHaveLength(128 * 1024));
  expect(screen.getByLabelText('Recent stderr').querySelector('script')).toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Copy logs' }));
  await waitFor(() => expect(copyTextToClipboard).toHaveBeenCalledWith(text.slice(-128 * 1024)));
  expect(plugins.stderr).toHaveBeenCalledWith('remote', 'example');
});

it('keeps existing logs visible when refresh fails and offers retry', async () => {
  vi.mocked(plugins.stderr).mockResolvedValueOnce({ stderr: 'Last known output' }).mockRejectedValueOnce(new Error('private failure'));
  renderWithQuery(<PluginLogs owner="local" pluginId="example" />);
  await waitFor(() => expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('Last known output'));
  fireEvent.click(screen.getByRole('button', { name: 'Refresh logs' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Refresh logs to retry');
  expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('Last known output');
  expect(screen.queryByText('private failure')).not.toBeInTheDocument();
  vi.mocked(plugins.stderr).mockResolvedValue({ stderr: '' });
  fireEvent.click(screen.getByRole('button', { name: 'Refresh logs' }));
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('No recent stderr.');
});

it('distinguishes an initial failure from empty logs and never reuses another owner logs', async () => {
  vi.mocked(plugins.stderr).mockResolvedValueOnce({ stderr: 'Local output' }).mockRejectedValueOnce(new Error('offline'));
  const { rerender } = renderWithQuery(<PluginLogs owner="local" pluginId="example" />);
  await waitFor(() => expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('Local output'));
  rerender(<PluginLogs owner="remote" pluginId="example" />);
  expect(await screen.findByRole('alert')).toBeVisible();
  expect(screen.getByLabelText('Recent stderr')).toHaveTextContent('Logs are unavailable.');
  expect(screen.getByLabelText('Recent stderr')).not.toHaveTextContent('Local output');
});
