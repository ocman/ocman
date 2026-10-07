// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { PrepareSessionResponse } from './api';
import { cacheNewSessionCatalog, getNewSessionCatalog } from './newSessionCatalogCache';

const storageKey = 'ocman.newSessionCatalogs.v1';
const catalog: PrepareSessionResponse = {
  platform: 'opencode', agents: [], commands: [], models: { models: [], hasProviders: false }, liveConnection: false,
};
beforeEach(() => localStorage.removeItem(storageKey));
afterEach(() => vi.restoreAllMocks());

it('persists catalogs by target and keeps the last 20 refreshed targets', () => {
  expect(getNewSessionCatalog('missing')).toBeUndefined();
  for (let i = 0; i < 20; i++) cacheNewSessionCatalog(String(i), catalog);
  cacheNewSessionCatalog('0', { ...catalog, defaultAgent: 'plan' });
  cacheNewSessionCatalog('20', catalog);
  expect(getNewSessionCatalog('1')).toBeUndefined();
  expect(getNewSessionCatalog('0')?.defaultAgent).toBe('plan');
  expect(getNewSessionCatalog('20')).toEqual(catalog);
});

it('recovers from malformed or unavailable storage', () => {
  localStorage.setItem(storageKey, 'invalid');
  expect(getNewSessionCatalog('target')).toBeUndefined();
  cacheNewSessionCatalog('target', catalog);
  expect(getNewSessionCatalog('target')).toEqual(catalog);
  vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('disabled'); });
  vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('quota'); });
  expect(getNewSessionCatalog('target')).toBeUndefined();
  expect(() => cacheNewSessionCatalog('target', catalog)).not.toThrow();
});

it.each([{}, null, { ...catalog, models: {} }, { ...catalog, agents: {} },
  { ...catalog, commands: null }, { ...catalog, agents: [null] },
  { ...catalog, commands: [{}] }, { ...catalog, models: { models: [{}] } },
])('treats structurally invalid persisted catalogs as cache misses: %j', (invalid) => {
  localStorage.setItem(storageKey, JSON.stringify([['target', invalid]]));
  expect(getNewSessionCatalog('target')).toBeUndefined();
});
