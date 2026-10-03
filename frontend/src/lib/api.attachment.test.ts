// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

it('addresses an upload by compound platform and preserves its file bytes', async () => {
  const saved = { path: '/box-cache/note.txt', name: 'note.txt', mime: 'text/plain', size: 4 };
  const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(saved), { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  const file = new File(['note'], 'note.txt', { type: 'text/plain' });
  expect(await api.uploadComposerAttachment('same-id', file, 'r-box:opencode')).toEqual(saved);
  const [url, options] = fetch.mock.calls[0];
  expect(url).toBe('/api/session/same-id/attachment?platform=r-box%3Aopencode');
  expect(options.method).toBe('POST');
  expect(options.body.get('file')).toEqual(file);
});

it('retains the legacy inferred-owner route when platform is omitted', async () => {
  const fetch = vi.fn().mockResolvedValue(new Response('{}', { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  await api.uploadComposerAttachment('s1', new File(['note'], 'note.txt'));
  expect(fetch.mock.calls[0][0]).toBe('/api/session/s1/attachment');
});
