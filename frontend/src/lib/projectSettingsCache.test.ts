import { beforeEach, expect, it, vi } from 'vitest';
import { fetchJSON } from './api';
import { clearSettingsCache, loadProjectSettings } from './projectSettingsCache';

vi.mock('./api', () => ({ fetchJSON: vi.fn() }));
const settings = { models: [], off: false, defaultAgent: 'build' };
beforeEach(() => {
  clearSettingsCache();
  vi.mocked(fetchJSON).mockReset().mockResolvedValue(settings);
});

it('shares concurrent and later reads across sibling worktrees, scoped by owner and project', async () => {
  const first = loadProjectSettings('/src/repo', 'box');
  expect(loadProjectSettings('/src/.worktrees/repo/one', 'box')).toBe(first);
  await first;
  await loadProjectSettings('/src/.worktrees/repo/two', 'box');
  expect(fetchJSON).toHaveBeenCalledTimes(1);
  await loadProjectSettings('/src/repo', 'local');
  await loadProjectSettings('/src/other', 'box');
  expect(fetchJSON).toHaveBeenCalledTimes(3);
});

it('clears every project and prevents an old in-flight response from restoring stale settings', async () => {
  let finish!: (value: typeof settings) => void;
  vi.mocked(fetchJSON).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
  const old = loadProjectSettings('/repo');
  await loadProjectSettings('/other');
  clearSettingsCache();
  vi.mocked(fetchJSON).mockResolvedValue({ ...settings, defaultAgent: 'plan' });
  finish(settings);
  expect(await old).toMatchObject({ defaultAgent: 'plan' });
  await loadProjectSettings('/other');
  expect(fetchJSON).toHaveBeenCalledTimes(4);
});

it('retries failed reads instead of caching failures', async () => {
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('offline'));
  await expect(loadProjectSettings('/repo')).rejects.toThrow('offline');
  expect(await loadProjectSettings('/repo')).toEqual(settings);
  expect(fetchJSON).toHaveBeenCalledTimes(2);
});
