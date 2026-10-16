import { afterEach, expect, it, vi } from 'vitest';
import { resolvePreviews } from './previews';
import { requestPreview } from './previewRequests';

afterEach(() => vi.restoreAllMocks());

it('bundles simultaneous conversation lookups and deduplicates identical text', async () => {
  const first = { provider: 'github', kind: 'pr', id: 'a/r#1', state: 'ok' };
  const second = { ...first, id: 'a/r#2' };
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => new Response(JSON.stringify({
    results: [{ previews: [first] }, { previews: [second] }],
  }), { status: 200 }));
  const results = await Promise.all([
    resolvePreviews('https://github.com/a/r/pull/1'),
    resolvePreviews('https://github.com/a/r/pull/2'),
    resolvePreviews('https://github.com/a/r/pull/1'),
  ]);
  expect(fetch).toHaveBeenCalledTimes(1);
  expect(JSON.parse(String(fetch.mock.calls[0][1]?.body))).toEqual({ requests: [
    { text: 'https://github.com/a/r/pull/1', render: true },
    { text: 'https://github.com/a/r/pull/2', render: true },
  ] });
  expect(results).toEqual([[first], [second], [first]]);
});

const response = (body: unknown) => new Response(JSON.stringify(body), { status: 200 });

it('keeps owners separate and splits oversized batches', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async (_url, init) => {
    const body = JSON.parse(String(init?.body));
    return response(body.requests ? { results: body.requests.map(() => ({ previews: [] })) } : { previews: [] });
  });
  await Promise.all([
    ...Array.from({ length: 33 }, (_, i) => requestPreview({ text: `${i}` }, 'local')),
    requestPreview({ text: '0' }, 'remote-1'),
  ]);
  expect(fetch).toHaveBeenCalledTimes(3);
  expect(JSON.parse(String(fetch.mock.calls[0][1]?.body)).requests).toHaveLength(32);
  expect(fetch.mock.calls.map(([url]) => url)).toContain('/api/previews/resolve?remoteId=remote-1');
});

it('splits batches by encoded body size', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(async () => response({ previews: [] }));
  await Promise.all([requestPreview({ text: 'é'.repeat(260_000) }, 'local'), requestPreview({ text: 'ü'.repeat(260_000) }, 'local')]);
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('isolates a failed lookup from the rest of a batch', async () => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(response({ results: [{ error: 'invalid target' }, { previews: [] }] }));
  const results = await Promise.allSettled([requestPreview({ text: 'bad' }, 'local'), requestPreview({ text: 'good' }, 'local')]);
  expect(results[0]).toMatchObject({ status: 'rejected', reason: new Error('invalid target') });
  expect(results[1]).toEqual({ status: 'fulfilled', value: [] });
});

it.each([{}, { results: [] }])('rejects malformed batch responses %j', async (body) => {
  vi.spyOn(globalThis, 'fetch').mockResolvedValue(response(body));
  const results = await Promise.allSettled([requestPreview({ text: 'a' }, 'local'), requestPreview({ text: 'b' }, 'local')]);
  expect(results.every((r) => r.status === 'rejected')).toBe(true);
});

it('rejects transport failures and permits a later retry', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockRejectedValueOnce(new Error('offline')).mockResolvedValue(response({ previews: [] }));
  await expect(requestPreview({ text: 'a' }, 'local')).rejects.toThrow('offline');
  await expect(requestPreview({ text: 'a' }, 'local')).resolves.toEqual([]);
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('drops cancelled lookups before dispatch', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch');
  const controller = new AbortController();
  const promise = requestPreview({ text: 'a', render: true }, 'local', controller.signal);
  controller.abort();
  await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  await expect(requestPreview({ text: 'a' }, 'local', controller.signal)).rejects.toMatchObject({ name: 'AbortError' });
  await new Promise((resolve) => setTimeout(resolve, 25));
  expect(fetch).not.toHaveBeenCalled();
});

it('keeps a shared in-flight batch alive until every reader leaves', async () => {
  let finish!: (value: Response) => void;
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
  const a = new AbortController(), b = new AbortController();
  const first = requestPreview({ text: 'a' }, 'local', a.signal);
  const duplicate = requestPreview({ text: 'a' }, 'local', b.signal);
  const other = requestPreview({ text: 'b' }, 'local');
  await Promise.resolve();
  a.abort();
  await expect(first).rejects.toMatchObject({ name: 'AbortError' });
  expect((fetch.mock.calls[0][1]?.signal as AbortSignal).aborted).toBe(false);
  finish(response({ results: [{ previews: [] }, { previews: [] }] }));
  await expect(duplicate).resolves.toEqual([]);
  await expect(other).resolves.toEqual([]);
});

it('aborts the transport after the last in-flight reader leaves', async () => {
  const fetch = vi.spyOn(globalThis, 'fetch').mockImplementation((_url, init) => new Promise((_resolve, reject) => {
    init?.signal?.addEventListener('abort', () => reject(init.signal?.reason));
  }));
  const controller = new AbortController();
  const promise = requestPreview({ text: 'a' }, 'local', controller.signal);
  await Promise.resolve();
  controller.abort();
  await expect(promise).rejects.toMatchObject({ name: 'AbortError' });
  expect(fetch.mock.calls[0][1]?.signal?.aborted).toBe(true);
});
