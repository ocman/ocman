// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { fetchJSON, postJSON } from '../lib/api';
import { clearSettingsCache } from '../lib/projectSettingsCache';
import { DefaultAgentSetting } from './DefaultAgentSetting';

vi.mock('../lib/api', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
vi.mock('../lib/projectSettingsCache', () => ({ clearSettingsCache: vi.fn(), useSettingsRevision: () => 0 }));
beforeEach(() => {
  vi.mocked(fetchJSON).mockReset().mockResolvedValue({ defaultAgent: 'build' });
  vi.mocked(postJSON).mockReset().mockResolvedValue({ defaultAgent: 'plan' });
  vi.mocked(clearSettingsCache).mockClear();
});

it('loads the saved agent and clears all cached settings after saving', async () => {
  render(<DefaultAgentSetting />);
  const input = screen.getByRole('textbox', { name: 'Default agent' });
  await waitFor(() => expect(input).not.toBeDisabled());
  expect(input).toHaveValue('build');
  fireEvent.change(input, { target: { value: 'plan' } });
  fireEvent.blur(input);
  await waitFor(() => expect(postJSON).toHaveBeenCalledWith('/api/settings/default-agent', { defaultAgent: 'plan' }));
  await waitFor(() => expect(clearSettingsCache).toHaveBeenCalledOnce());
  expect(screen.getByRole('textbox', { name: 'Default agent' })).toHaveValue('plan');
});

it('does not invalidate caches after a rejected save', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('invalid agent'));
  render(<DefaultAgentSetting />);
  const input = screen.getByRole('textbox', { name: 'Default agent' });
  await waitFor(() => expect(input).not.toBeDisabled());
  fireEvent.change(input, { target: { value: '' } });
  fireEvent.blur(input);
  await screen.findByTitle('Save failed');
  expect(clearSettingsCache).not.toHaveBeenCalled();
});

it('shows a failed load and lets the user retry before editing', async () => {
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('offline'));
  render(<DefaultAgentSetting />);
  expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  expect(screen.getByRole('textbox', { name: 'Default agent' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByRole('textbox', { name: 'Default agent' })).toBeEnabled());
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});
