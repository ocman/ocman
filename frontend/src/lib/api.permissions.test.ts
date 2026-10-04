import { afterEach, describe, expect, it, vi } from 'vitest';
import { api } from './api';

afterEach(() => vi.unstubAllGlobals());

describe('api.refreshPermissions', () => {
  it('requests the authoritative list pinned to the owning platform', async () => {
    const fetchMock = vi.fn<(url: string) => Promise<Response>>(async () => new Response('[]', { status: 200 }));
    vi.stubGlobal('fetch', fetchMock);
    await api.refreshPermissions('s/1', 'r-box:opencode');
    expect(fetchMock.mock.calls[0][0]).toBe('/api/session/s%2F1/permissions?refresh=1&platform=r-box%3Aopencode');
  });
});
