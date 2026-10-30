// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { useBetaFeatures } from './betaFeatures';
import { fetchJSON, postJSON } from './api.requests';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));

beforeEach(() => {
  vi.resetAllMocks();
  useBetaFeatures.setState({ factory: false, loaded: false, error: false });
  localStorage.clear();
});

it('defaults off and ignores old browser-local opt-ins', async () => {
  localStorage.setItem('ocman-beta-features', JSON.stringify({ state: { factory: true } }));
  vi.mocked(fetchJSON).mockResolvedValue({ factory: false });
  await useBetaFeatures.getState().load();
  expect(useBetaFeatures.getState()).toMatchObject({ factory: false, loaded: true, error: false });
});

it.each([true, false])('loads the instance-wide flag %s', async (factory) => {
  vi.mocked(fetchJSON).mockResolvedValue({ factory });
  await useBetaFeatures.getState().load();
  expect(fetchJSON).toHaveBeenCalledWith('/api/settings/beta-features');
  expect(useBetaFeatures.getState().factory).toBe(factory);
});

it('only updates after a successful save', async () => {
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('offline'));
  await expect(useBetaFeatures.getState().setFactory(true)).rejects.toThrow('offline');
  expect(useBetaFeatures.getState().factory).toBe(false);
  vi.mocked(postJSON).mockResolvedValue({ factory: true });
  await useBetaFeatures.getState().setFactory(true);
  expect(postJSON).toHaveBeenCalledWith('/api/settings/beta-features', { factory: true });
  expect(useBetaFeatures.getState().factory).toBe(true);
});

it('fails closed and supports retrying failed loads', async () => {
  useBetaFeatures.setState({ factory: true });
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ factory: true });
  await useBetaFeatures.getState().load();
  expect(useBetaFeatures.getState()).toMatchObject({ factory: false, error: true });
  await useBetaFeatures.getState().load();
  expect(useBetaFeatures.getState()).toMatchObject({ factory: true, error: false });
});

it('does not let a stale load overwrite a saved opt-in', async () => {
  let finish!: (value: { factory: boolean }) => void;
  vi.mocked(fetchJSON).mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  const loading = useBetaFeatures.getState().load();
  vi.mocked(postJSON).mockResolvedValue({ factory: true });
  await useBetaFeatures.getState().setFactory(true);
  finish({ factory: false });
  await loading;
  expect(useBetaFeatures.getState().factory).toBe(true);
});
