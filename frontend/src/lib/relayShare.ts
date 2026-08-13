import type { SharedConversation } from './api.types';

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

export async function decryptRelayChunk(
  keyText: string,
  id: string,
  chunk: RelayChunk,
): Promise<SharedConversation> {
  const key = await crypto.subtle.importKey('raw', bytes(decodeBase64URL(keyText)), 'AES-GCM', false, ['decrypt']);
  const plain = await crypto.subtle.decrypt(
    { name: 'AES-GCM', iv: bytes(nonce(chunk.seq)), additionalData: bytes(aad(id, chunk.seq)) },
    key,
    bytes(decodeBase64(chunk.data)),
  );
  return JSON.parse(new TextDecoder().decode(plain)) as SharedConversation;
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

export async function readRelayShare(
  id: string,
  key: string,
  from: number,
  signal?: AbortSignal,
  origin = '',
): Promise<{ chunks: SharedConversation[]; last: number }> {
  const response = await fetch(`${origin}/s/${encodeURIComponent(id)}?from=${from}`, { signal });
  if (!response.ok) throw new Error(`relay share: ${response.status}`);
  const body = (await response.json()) as RelayReadResponse;
  return {
    chunks: await Promise.all(body.chunks.map((chunk) => decryptRelayChunk(key, id, chunk))),
    last: body.last,
  };
}

export { POLL_MS as relayPollMs };

export function parseRelayShareURL(raw: string): { origin: string; id: string; key: string } {
  const url = new URL(raw);
  const match = url.pathname.match(/^\/v\/([^/]+)$/);
  const key = new URLSearchParams(url.hash.replace(/^#/, '')).get('k') ?? '';
  if (!match || !key) throw new Error('Not an ocman relay share URL');
  return { origin: url.origin, id: decodeURIComponent(match[1]), key };
}
