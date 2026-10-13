// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { acquirePRStatus } from './prStatusCache';
import { clearPRChecksCache } from './prChecksCache';
import { previewAuthChanged } from './previews';

const key = ['local', 'github.com/o/r', '42', 'sha', 'today', 'open'].join('\0');
const done = { mergeable: true, approved: true };
beforeEach(() => clearPRChecksCache());

it('keeps a shared request alive until its last reader leaves', () => {
  const load = vi.fn().mockImplementation(() => new Promise(() => {}));
  const first = acquirePRStatus(key, load);
  const second = acquirePRStatus(key, load);
  expect(load).toHaveBeenCalledOnce();
  first.release();
  expect(load.mock.calls[0][0].aborted).toBe(false);
  second.release();
  expect(load.mock.calls[0][0].aborted).toBe(true);
  acquirePRStatus(key, load).release();
  expect(load).toHaveBeenCalledTimes(2);
});

it('retries failures and unknown mergeability instead of caching them', async () => {
  const load = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ mergeable: null, approved: true }).mockResolvedValue(done);
  const failed = acquirePRStatus(key, load);
  await expect(failed.promise).rejects.toThrow('offline');
  failed.release();
  const pending = acquirePRStatus(key, load);
  await pending.promise;
  pending.release();
  const settled = acquirePRStatus(key, load);
  await settled.promise;
  settled.release();
  await acquirePRStatus(key, load).promise;
  expect(load).toHaveBeenCalledTimes(3);
});

it('caches reviews-only results when mergeability is already known or irrelevant', async () => {
  const load = vi.fn().mockResolvedValue({ mergeable: null, approved: false });
  const first = acquirePRStatus(key, load, false, true);
  await first.promise;
  first.release();
  await acquirePRStatus(key, load, false, true).promise;
  expect(load).toHaveBeenCalledOnce();
});

it('invalidates only matching repositories and clears data after credential changes', async () => {
  const load = vi.fn().mockResolvedValue(done);
  await acquirePRStatus(key, load).promise;
  clearPRChecksCache(['github.com/other/repo']);
  await acquirePRStatus(key, load).promise;
  expect(load).toHaveBeenCalledOnce();
  clearPRChecksCache(['github.com/o/r']);
  await acquirePRStatus(key, load).promise;
  expect(load).toHaveBeenCalledTimes(2);
  previewAuthChanged();
  await acquirePRStatus(key, load).promise;
  expect(load).toHaveBeenCalledTimes(3);
});

it('forces a settled refresh but still shares an in-flight refresh', async () => {
  const load = vi.fn().mockResolvedValue(done);
  await acquirePRStatus(key, load).promise;
  const refresh = acquirePRStatus(key, load, true);
  const shared = acquirePRStatus(key, load, true);
  await Promise.all([refresh.promise, shared.promise]);
  expect(load).toHaveBeenCalledTimes(2);
});
