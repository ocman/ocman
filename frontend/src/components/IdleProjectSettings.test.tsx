// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { fetchJSON, postJSON } from '../lib/api.requests';
import { IdleProjectSettings } from './IdleProjectSettings';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => vi.clearAllMocks());

it('loads and saves a duration and Never', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ minutes: 0 });
  vi.mocked(postJSON).mockResolvedValue({ minutes: 30 });
  render(<IdleProjectSettings />);
  const input = screen.getByRole('spinbutton', { name: 'Idle project timeout in minutes' });
  await waitFor(() => expect(input).toBeEnabled());
  expect(input).toHaveValue(0);
  fireEvent.change(input, { target: { value: '30' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('status')).toHaveTextContent('Saved');
  expect(postJSON).toHaveBeenLastCalledWith('/api/settings/idle-project-timeout', { minutes: 30 });
  fireEvent.change(input, { target: { value: '0' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  await waitFor(() => expect(postJSON).toHaveBeenLastCalledWith('/api/settings/idle-project-timeout', { minutes: 0 }));
});

it('rejects invalid durations and reports failures', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ minutes: 15 });
  vi.mocked(postJSON).mockRejectedValue(new Error('offline'));
  render(<IdleProjectSettings />);
  const input = screen.getByRole('spinbutton');
  await waitFor(() => expect(input).toBeEnabled());
  fireEvent.change(input, { target: { value: '-1' } });
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  fireEvent.change(input, { target: { value: '43201' } });
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
  fireEvent.change(input, { target: { value: '30' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('offline');
  await waitFor(() => expect(input).toBeEnabled());
  expect(input).toHaveValue(30);
});

it('keeps the form disabled on a load failure', async () => {
  vi.mocked(fetchJSON).mockRejectedValue(new Error('load failed'));
  render(<IdleProjectSettings />);
  expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
  expect(screen.getByRole('button', { name: 'Save' })).toBeDisabled();
});
