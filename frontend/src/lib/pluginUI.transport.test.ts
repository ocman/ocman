import { afterEach, expect, it, vi } from 'vitest';
import { callPluginUI } from './pluginUI';
import { PluginCallError } from './pluginCallError';
import { registerAuthErrorHandler } from './api.requests';

afterEach(() => vi.unstubAllGlobals());

it.each([
  [503, 'unavailable', '2', 2000],
  [429, null, null, null],
  [403, 'permission_denied', '-1', null],
  [400, 'invalid_argument', 'nonsense', null],
  [500, 'internal', '0', 0],
])('preserves HTTP %i metadata without exposing diagnostics', async (status, category, retryAfter, retryAfterMs) => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ error: { category, message: 'private diagnostic' } }), {
    status, headers: retryAfter === null ? {} : { 'Retry-After': retryAfter },
  })));
  await expect(callPluginUI('org.example.catalog', 'items.list', {})).rejects.toMatchObject({
    name: 'PluginCallError', status, category, retryAfterMs, message: `Plugin request failed (HTTP ${status})`,
  });
});

it('retains status on malformed error bodies and ignores unknown categories', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('private proxy error', { status: 503 }))
    .mockResolvedValueOnce(new Response('{"error":{"category":"private diagnostic"}}', { status: 500 }));
  vi.stubGlobal('fetch', fetch);
  for (const status of [503, 500]) {
    await expect(callPluginUI('org.example.index', 'documents.list', null)).rejects.toMatchObject({ status, category: null });
  }
});

it('passes cancellation through and never retries transport failures', async () => {
  const controller = new AbortController();
  controller.abort();
  const error = new DOMException('Aborted', 'AbortError');
  const fetch = vi.fn().mockRejectedValue(error);
  vi.stubGlobal('fetch', fetch);
  await expect(callPluginUI('org.example.catalog', 'items.list', undefined, controller.signal)).rejects.toBe(error);
  expect(fetch).toHaveBeenCalledOnce();
  expect(fetch.mock.calls[0][1]).toMatchObject({ signal: controller.signal });
});

it.each([
  ['Fri, 06 Nov 2026 12:00:02 GMT', 2000],
  ['Fri, 06 Nov 2026 11:00:00 GMT', 0],
  ['Fri, not a date', null],
  ['9999999999999999999999', null],
  ['1.5', null],
  ['', null],
])('parses Retry-After %s', async (header, expected) => {
  const now = vi.spyOn(Date, 'now').mockReturnValue(Date.parse('2026-11-06T12:00:00Z'));
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('null', { status: 503, headers: { 'Retry-After': header } })));
  try {
    await expect(callPluginUI('org.example.index', 'documents.list', null)).rejects.toMatchObject({ retryAfterMs: expected });
  } finally { now.mockRestore(); }
});

it('keeps successful calls and authentication handling on the shared transport', async () => {
  const fetch = vi.fn()
    .mockResolvedValueOnce(new Response('{"items":[]}'))
    .mockResolvedValueOnce(new Response('unauthorized', { status: 401 }));
  vi.stubGlobal('fetch', fetch);
  await expect(callPluginUI('org.example.catalog', 'items.list', { limit: 2 })).resolves.toEqual({ items: [] });
  expect(fetch.mock.calls[0][1]).toMatchObject({ method: 'POST', body: '{"pluginId":"org.example.catalog","method":"items.list","params":{"limit":2}}' });
  const onAuthError = vi.fn();
  const previous = registerAuthErrorHandler(onAuthError);
  try {
    await expect(callPluginUI('org.example.catalog', 'items.save', {})).rejects.toMatchObject({ name: 'AuthError' });
    expect(onAuthError).toHaveBeenCalledOnce();
  } finally { registerAuthErrorHandler(previous); }
});

it('exports an Error subclass matching the public contract', () => {
  expect(new PluginCallError(503, 'unavailable', null)).toBeInstanceOf(Error);
});

it('preserves cancellation while reading an error response body', async () => {
  const error = new DOMException('Aborted', 'AbortError');
  const response = new Response('', { status: 503 });
  vi.spyOn(response, 'json').mockRejectedValue(error);
  vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response));
  await expect(callPluginUI('org.example.catalog', 'items.list', {})).rejects.toBe(error);
});
