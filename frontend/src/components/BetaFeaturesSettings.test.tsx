// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { BetaFeaturesSettings } from './BetaFeaturesSettings';
import { BetaFeaturesSync } from './BetaFeaturesSync';
import { useBetaFeatures } from '../lib/betaFeatures';
import { fetchJSON, postJSON } from '../lib/api.requests';

vi.mock('../lib/api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => {
  vi.resetAllMocks();
  useBetaFeatures.setState({ factory: false, loaded: false, error: false });
});
afterEach(() => { cleanup(); vi.useRealTimers(); });

it('disables the toggle until the instance setting loads', () => {
  render(<BetaFeaturesSettings />);
  expect(screen.getByRole('checkbox', { name: 'Enable Factory beta' })).toBeDisabled();
});

it('saves the toggle to the backend', async () => {
  useBetaFeatures.setState({ loaded: true });
  vi.mocked(postJSON).mockResolvedValue({ factory: true });
  render(<BetaFeaturesSettings />);
  fireEvent.click(screen.getByRole('checkbox', { name: 'Enable Factory beta' }));
  await waitFor(() => expect(postJSON).toHaveBeenCalledWith('/api/settings/beta-features', { factory: true }));
  expect(screen.getByRole('checkbox', { name: 'Enable Factory beta' })).toBeChecked();
});

it('offers retry after a failed load', async () => {
  useBetaFeatures.setState({ error: true });
  vi.mocked(fetchJSON).mockResolvedValue({ factory: false });
  render(<BetaFeaturesSettings />);
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.getByRole('checkbox')).toBeEnabled());
  expect(screen.queryByText('Could not load beta features.')).not.toBeInTheDocument();
});

it('syncs on mount, focus and periodic refresh and cleans up', async () => {
  vi.useFakeTimers();
  vi.mocked(fetchJSON).mockResolvedValue({ factory: true });
  const view = render(<BetaFeaturesSync />);
  await act(async () => {});
  expect(useBetaFeatures.getState().factory).toBe(true);
  vi.mocked(fetchJSON).mockResolvedValue({ factory: false });
  await act(async () => { window.dispatchEvent(new Event('focus')); });
  expect(useBetaFeatures.getState().factory).toBe(false);
  await act(async () => { vi.advanceTimersByTime(10_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(3);
  view.unmount();
  await act(async () => { window.dispatchEvent(new Event('focus')); vi.advanceTimersByTime(10_000); });
  expect(fetchJSON).toHaveBeenCalledTimes(3);
});
