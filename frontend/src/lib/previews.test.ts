import { afterEach, describe, expect, it, vi } from 'vitest';
import { resolvePreviews } from './previews';

describe('resolvePreviews', () => {
  afterEach(() => { vi.restoreAllMocks(); });

  it('posts the text owner-qualified and returns normalized previews', async () => {
    const preview = { provider: 'mock', kind: 'issue', id: 'ABC-42', state: 'connect' };
    const fetchMock = vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify({ previews: [preview] }), { status: 200, headers: { 'Content-Type': 'application/json' } }),
    );
    await expect(resolvePreviews('see ABC-42', 'r 1')).resolves.toEqual([preview]);
    const [url, init] = fetchMock.mock.calls[0];
    expect(String(url)).toBe('/api/previews/resolve?remoteId=r%201');
    expect(JSON.parse(String(init?.body))).toEqual({ text: 'see ABC-42' });
  });
});
