// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { fetchJSON, postJSON } from '../lib/api';
import { clearSettingsCache } from '../lib/projectSettingsCache';
import { DefaultAgentSetting } from './DefaultAgentSetting';

vi.mock('../lib/api', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
vi.mock('../lib/projectSettingsCache', () => ({ clearSettingsCache: vi.fn(), useSettingsRevision: () => 0 }));
beforeEach(() => {
  vi.mocked(fetchJSON).mockReset().mockResolvedValue({ defaultAgent: 'build', agents: ['build', 'plan', 'custom-agent'] });
  vi.mocked(postJSON).mockReset().mockResolvedValue({ defaultAgent: 'plan' });
  vi.mocked(clearSettingsCache).mockClear();
});

it('loads the saved agent and clears all cached settings after saving', async () => {
  render(<DefaultAgentSetting />);
  const input = screen.getByRole('combobox', { name: 'Default agent' });
  await waitFor(() => expect(input).not.toBeDisabled());
  expect(input).toHaveTextContent('build');
  fireEvent.click(input);
  expect(screen.getByRole('option', { name: 'custom-agent' })).toBeInTheDocument();
  fireEvent.click(screen.getByRole('option', { name: 'plan' }));
  await waitFor(() => expect(postJSON).toHaveBeenCalledWith('/api/settings/default-agent', { defaultAgent: 'plan' }));
  await waitFor(() => expect(clearSettingsCache).toHaveBeenCalledOnce());
  expect(screen.getByRole('combobox', { name: 'Default agent' })).toHaveTextContent('plan');
});

it('does not invalidate caches after a rejected save', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('invalid agent'));
  render(<DefaultAgentSetting />);
  const input = screen.getByRole('combobox', { name: 'Default agent' });
  await waitFor(() => expect(input).not.toBeDisabled());
  fireEvent.click(input);
  fireEvent.click(screen.getByRole('option', { name: 'plan' }));
  await screen.findByTitle('Save failed');
  expect(screen.getByRole('alert')).toHaveTextContent('invalid agent');
  expect(screen.queryByRole('button', { name: 'Retry' })).not.toBeInTheDocument();
  expect(clearSettingsCache).not.toHaveBeenCalled();
  await waitFor(() => expect(input).toBeEnabled());
  fireEvent.click(input);
  fireEvent.click(screen.getByRole('option', { name: 'plan' }));
  await waitFor(() => expect(clearSettingsCache).toHaveBeenCalledOnce());
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('shows a failed load and lets the user retry before editing', async () => {
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('offline'));
  render(<DefaultAgentSetting />);
  expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  expect(screen.getByRole('combobox', { name: 'Default agent' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByRole('combobox', { name: 'Default agent' })).toBeEnabled());
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('preserves an unavailable saved agent and offers built-ins with an older backend', async () => {
  vi.mocked(fetchJSON).mockResolvedValueOnce({ defaultAgent: 'retired-agent' });
  render(<DefaultAgentSetting />);
  const input = screen.getByRole('combobox', { name: 'Default agent' });
  await waitFor(() => expect(input).toBeEnabled());
  expect(input).toHaveTextContent('retired-agent');
  fireEvent.click(input);
  expect(screen.getByRole('option', { name: 'retired-agent' })).toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'build' })).toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'plan' })).toBeInTheDocument();
});
