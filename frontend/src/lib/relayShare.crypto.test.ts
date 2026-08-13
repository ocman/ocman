// @vitest-environment jsdom
import { describe, expect, it, vi, afterEach } from 'vitest';
import { decryptRelayChunk, parseRelayShareURL, readRelayShare } from './relayShare';
import type { SharedConversation } from './api.types';

// The viewer decrypts with WebCrypto, so these tests seal real chunks
// the same way the Go writer does (AES-256-GCM, sequence-derived nonce,
// share id + sequence as AAD) and decrypt them through the real code.
// A hand-rolled fake would only prove the fake agrees with itself.
const rawKey = new Uint8Array(32).map((_, i) => i);
const keyText = btoa(String.fromCharCode(...rawKey)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');

function nonce(seq: number): Uint8Array {
  const out = new Uint8Array(12);
  new DataView(out.buffer).setBigUint64(4, BigInt(seq), false);
  return out;
}

function aad(id: string, seq: number): Uint8Array {
  const idBytes = new TextEncoder().encode(id);
  const out = new Uint8Array(idBytes.length + 8);
  out.set(idBytes);
  new DataView(out.buffer).setBigUint64(idBytes.length, BigInt(seq), false);
  return out;
}

// WebCrypto wants an ArrayBuffer, not a view over one.
function buf(view: Uint8Array): ArrayBuffer {
  return view.buffer.slice(view.byteOffset, view.byteOffset + view.byteLength) as ArrayBuffer;
}

async function seal(id: string, seq: number, payload: unknown): Promise<string> {
  const key = await crypto.subtle.importKey('raw', buf(rawKey), 'AES-GCM', false, ['encrypt']);
  const sealed = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv: buf(nonce(seq)), additionalData: buf(aad(id, seq)) },
    key,
    new TextEncoder().encode(JSON.stringify(payload)),
  );
  return btoa(String.fromCharCode(...new Uint8Array(sealed))).replace(/=+$/, '');
}

const conversation: SharedConversation = {
  session: { id: 's1', title: 'Sealed' } as never,
  messages: [{ id: 'm1', sessionId: 's1', timeCreated: 1, data: { role: 'user' } }],
  parts: [{ id: 'p1', messageId: 'm1', sessionId: 's1', timeCreated: 1, data: { type: 'text', text: 'hi' } }],
  readOnly: true,
};

afterEach(() => vi.unstubAllGlobals());

describe('decryptRelayChunk', () => {
  it('decrypts a chunk sealed for the same share and sequence', async () => {
    const data = await seal('share-1', 0, conversation);
    const got = await decryptRelayChunk(keyText, 'share-1', { seq: 0, data });
    expect(got.session?.title).toBe('Sealed');
    expect(got.messages[0].id).toBe('m1');
  });

  it('decrypts a chunk at a non-zero sequence', async () => {
    const data = await seal('share-1', 7, conversation);
    const got = await decryptRelayChunk(keyText, 'share-1', { seq: 7, data });
    expect(got.parts[0].id).toBe('p1');
  });

  // The share id and sequence are authenticated, so a relay cannot
  // renumber a chunk or move it between shares undetected. These two
  // cases are the reason that binding exists.
  it('rejects a chunk replayed at a different sequence', async () => {
    const data = await seal('share-1', 1, conversation);
    await expect(decryptRelayChunk(keyText, 'share-1', { seq: 2, data })).rejects.toThrow();
  });

  it('rejects a chunk transplanted from another share', async () => {
    const data = await seal('share-1', 0, conversation);
    await expect(decryptRelayChunk(keyText, 'share-2', { seq: 0, data })).rejects.toThrow();
  });

  it('rejects a chunk under the wrong key', async () => {
    const data = await seal('share-1', 0, conversation);
    const otherKey = btoa(String.fromCharCode(...new Uint8Array(32).fill(9)))
      .replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
    await expect(decryptRelayChunk(otherKey, 'share-1', { seq: 0, data })).rejects.toThrow();
  });
});

describe('readRelayShare', () => {
  it('fetches from a sequence and returns the decrypted chunks', async () => {
    const fetchMock = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ chunks: [{ seq: 3, data: await seal('share-1', 3, conversation) }], last: 3 }),
    });
    vi.stubGlobal('fetch', fetchMock);

    const got = await readRelayShare('share-1', keyText, 3, undefined, 'https://relay.test');

    expect(fetchMock.mock.calls[0][0]).toBe('https://relay.test/s/share-1?from=3');
    expect(got.last).toBe(3);
    expect(got.chunks[0].messages[0].id).toBe('m1');
  });

  it('encodes the share id into the URL', async () => {
    const fetchMock = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ chunks: [], last: -1 }) });
    vi.stubGlobal('fetch', fetchMock);

    await readRelayShare('a/b?c', keyText, 0);

    expect(fetchMock.mock.calls[0][0]).toBe('/s/a%2Fb%3Fc?from=0');
  });

  it('surfaces the status when the relay refuses the read', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({ ok: false, status: 404 }));
    await expect(readRelayShare('share-1', keyText, 0)).rejects.toThrow('relay share: 404');
  });
});

describe('parseRelayShareURL', () => {
  it('splits a share URL into origin, id and key', () => {
    expect(parseRelayShareURL('https://relay.test/v/share-1#k=abc')).toEqual({
      origin: 'https://relay.test',
      id: 'share-1',
      key: 'abc',
    });
  });

  it('decodes a percent-encoded id', () => {
    expect(parseRelayShareURL('https://relay.test/v/a%2Fb#k=abc').id).toBe('a/b');
  });

  it.each([
    ['https://relay.test/v/share-1', 'no key in the fragment'],
    ['https://relay.test/share-1#k=abc', 'not a viewer path'],
    ['https://relay.test/v/a/b#k=abc', 'nested viewer path'],
  ])('rejects %s (%s)', (url) => {
    expect(() => parseRelayShareURL(url)).toThrow('Not an ocman relay share URL');
  });
});
