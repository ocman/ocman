import { afterEach, describe, expect, it, vi } from 'vitest';
import { assembleArtifactShare, type ArtifactShareManifest } from './artifactShare';
import { decryptRelayBytes, readRelayShare } from './relayShare';
// Sealed by internal/server/artifact_share_test.go (TestArtifactShareFixture).
import fixture from './artifactShare.fixture.json';

afterEach(() => vi.unstubAllGlobals());

const stubRelay = (chunks = fixture.chunks) =>
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ chunks, last: chunks.length - 1 }))));

describe('artifact relay shares', () => {
  it('decrypts file chunks sealed by Go as raw bytes', async () => {
    const png = fixture.chunks.find((c) => c.seq > 0)!;
    const plain = await decryptRelayBytes(fixture.key, fixture.id, png);
    expect(plain).toBeInstanceOf(Uint8Array);
  });

  it('branches on kind and assembles each file into a Blob', async () => {
    stubRelay();
    const got = await readRelayShare(fixture.id, fixture.key, 0);
    expect(got.chunks).toEqual([]);
    const a = got.artifact!;
    expect(a.title).toBe('Fixture');
    expect(a.description).toBe('Sealed in Go');
    expect(a.links).toEqual([{ url: 'https://example.com/pr/1', label: 'PR' }]);
    expect(a.files.map((f) => [f.name, f.size])).toEqual([['notes.md', 25], ['pixel.png', 27], ['empty.txt', 0]]);
    expect(await a.files[0].blob.text()).toBe('# Notes\n\nhello **world**\n');
    expect(a.files[0].blob.type).toContain('text/markdown');
    const png = new Uint8Array(await a.files[1].blob.arrayBuffer());
    expect(Array.from(png.slice(0, 4))).toEqual([0x89, 0x50, 0x4e, 0x47]);
  });

  it('rejects a share with a missing file chunk', async () => {
    stubRelay(fixture.chunks.slice(0, -1));
    await expect(readRelayShare(fixture.id, fixture.key, 0)).rejects.toThrow(/missing chunk/);
  });

  it('rejects a file whose bytes disagree with the manifest size', () => {
    const m: ArtifactShareManifest = { kind: 'artifact', title: 't', links: [], files: [{ name: 'f', mime: 'text/plain', size: 5, firstSeq: 1, chunks: 1 }] };
    expect(() => assembleArtifactShare(m, new Map([[1, new Uint8Array(3)]]))).toThrow(/3 bytes, expected 5/);
  });
});
