import type { SharedConversation } from './api.types';
import { assembleArtifactShare, type ArtifactShare, type ArtifactShareManifest } from './artifactShare';

interface RelayChunk {
  seq: number;
  data: string;
}

interface RelayReadResponse {
  chunks: RelayChunk[];
  last: number;
}

const POLL_MS = 3000;

function decodeBase64URL(value: string): Uint8Array {
  const padded = value.replace(/-/g, '+').replace(/_/g, '/').padEnd(Math.ceil(value.length / 4) * 4, '=');
  return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0));
}

function bytes(value: Uint8Array): ArrayBuffer {
  return value.buffer.slice(value.byteOffset, value.byteOffset + value.byteLength) as ArrayBuffer;
}

function decodeBase64(value: string): Uint8Array {
  const padded = value.padEnd(Math.ceil(value.length / 4) * 4, '=');
  return Uint8Array.from(atob(padded), (c) => c.charCodeAt(0));
}

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

export function relayKeyFromFragment(hash = window.location.hash): string {
  return new URLSearchParams(hash.replace(/^#/, '')).get('k') ?? '';
}

/** Decrypts one relay chunk to its raw plaintext bytes. */
export async function decryptRelayBytes(keyText: string, id: string, chunk: RelayChunk): Promise<Uint8Array> {
  const key = await crypto.subtle.importKey('raw', bytes(decodeBase64URL(keyText)), 'AES-GCM', false, ['decrypt']);
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: bytes(nonce(chunk.seq)), additionalData: bytes(aad(id, chunk.seq)) },
    key,
    bytes(decodeBase64(chunk.data)),
  );
  return new Uint8Array(plain);
}

const parseJSON = (plain: Uint8Array): unknown => JSON.parse(new TextDecoder().decode(plain));

export async function decryptRelayChunk(
  keyText: string,
  id: string,
  chunk: RelayChunk,
): Promise<SharedConversation> {
  return parseJSON(await decryptRelayBytes(keyText, id, chunk)) as SharedConversation;
}

export function mergeRelayChunks(
  current: SharedConversation | null,
  chunks: SharedConversation[],
): SharedConversation {
  let session = current?.session ?? null;
  const messages = new Map(current?.messages.map((row) => [row.id, row]) ?? []);
  const parts = new Map(current?.parts.map((row) => [row.id, row]) ?? []);
  for (const chunk of chunks) {
    if (chunk.session) session = chunk.session;
    for (const row of chunk.messages ?? []) messages.set(row.id, row);
    for (const row of chunk.parts ?? []) parts.set(row.id, row);
  }
  return {
    session,
    messages: [...messages.values()].sort((a, b) => (a.timeCreated ?? 0) - (b.timeCreated ?? 0)),
    parts: [...parts.values()].sort((a, b) => (a.timeCreated ?? 0) - (b.timeCreated ?? 0)),
    readOnly: true,
  };
}

/**
 * Reads a relay share from `from`. A share whose chunk 0 is an artifact
 * manifest returns `artifact` (files decrypted as bytes, never parsed) and
 * no conversation chunks; anything else is a conversation.
 */
export async function readRelayShare(
  id: string,
  key: string,
  from: number,
  signal?: AbortSignal,
  origin = '',
): Promise<{ chunks: SharedConversation[]; last: number; artifact?: ArtifactShare }> {
  const response = await fetch(`${origin}/s/${encodeURIComponent(id)}?from=${from}`, { signal });
  if (!response.ok) throw new Error(`relay share: ${response.status}`);
  const body = (await response.json()) as RelayReadResponse;
  const plain = await Promise.all(body.chunks.map((chunk) => decryptRelayBytes(key, id, chunk)));
  const head = body.chunks[0]?.seq === 0 ? (parseJSON(plain[0]) as { kind?: unknown }) : null;
  if (head?.kind === 'artifact') {
    const bySeq = new Map(body.chunks.map((chunk, i) => [chunk.seq, plain[i]]));
    return { chunks: [], last: body.last, artifact: assembleArtifactShare(head as ArtifactShareManifest, bySeq) };
  }
  return { chunks: plain.map((p) => parseJSON(p) as SharedConversation), last: body.last };
}

export { POLL_MS as relayPollMs };

export function parseRelayShareURL(raw: string): { origin: string; id: string; key: string } {
  const url = new URL(raw);
  const match = url.pathname.match(/^\/v\/([^/]+)$/);
  const key = new URLSearchParams(url.hash.replace(/^#/, '')).get('k') ?? '';
  if (!match || !key) throw new Error('Not an ocman relay share URL');
  return { origin: url.origin, id: decodeURIComponent(match[1]), key };
}
