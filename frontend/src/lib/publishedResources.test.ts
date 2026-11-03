import { afterEach, expect, it, vi } from 'vitest';
import { artifactsApi } from './artifactsApi';
import { artifactPageURL, publishedResourceURL } from './publishedResources';

afterEach(() => vi.unstubAllGlobals());

it('keeps local paths and qualifies remote paths and pages', () => {
  expect(publishedResourceURL('/api/file/token')).toBe('/api/file/token');
  expect(publishedResourceURL('/api/file/token', 'box one')).toBe('/api/remote-resources/box%20one/api/file/token');
  expect(artifactPageURL('a b')).toBe('/artifacts/a%20b');
  expect(artifactPageURL('a', 'box one')).toBe('/artifacts/a?remoteId=box%20one');
});

it('reads remote artifact lists, metadata, stats and file URLs on the owner', async () => {
  const artifact = { id: 'a', platform: 'opencode', remoteId: 'local', items: [{ kind: 'file', url: '/api/artifacts/a/files/0' }, { kind: 'link', url: 'https://example.com' }] };
  const fetch = vi.fn(async (url: string) => new Response(JSON.stringify(url.includes('/stats') ? { count: 1 } : url.includes('?') ? { artifacts: [artifact], nextCursor: '' } : artifact)));
  vi.stubGlobal('fetch', fetch);
  const page = await artifactsApi.list({ remoteId: 'box', directory: '/repo', includeDescendants: true });
  const one = await artifactsApi.get('a', undefined, 'box');
  expect(fetch).toHaveBeenCalledWith('/api/remote-resources/box/api/artifacts?directory=%2Frepo&includeDescendants=1', undefined);
  expect(fetch).toHaveBeenCalledWith('/api/remote-resources/box/api/artifacts/a', undefined);
  expect(page.artifacts[0]).toEqual(one);
  expect(one.remoteId).toBe('box');
  expect(one.platform).toBe('r-box:opencode');
  expect(one.items[0].url).toBe('/api/remote-resources/box/api/artifacts/a/files/0');
  expect(one.items[1].url).toBe('https://example.com');
  await expect(artifactsApi.stats(undefined, 'box')).resolves.toEqual({ count: 1 });
});
