// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

it.each(['local', 'B'])('carries explicit %s ownership in project settings reads and writes', async (remoteId) => {
  const fetch = vi.fn().mockImplementation(async () => new Response('{}', { status: 200 }));
  vi.stubGlobal('fetch', fetch);
  await api.projectSettings('/repo', undefined, remoteId);
  const read = new URL(fetch.mock.calls[0][0], 'http://localhost');
  expect(read.searchParams.get('dir')).toBe('/repo');
  expect(read.searchParams.get('remoteId')).toBe(remoteId);
  await api.setProjectSettings('/repo', ['p/model'], true, remoteId);
  expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ directory: '/repo', models: ['p/model'], off: true, remoteId });
});
