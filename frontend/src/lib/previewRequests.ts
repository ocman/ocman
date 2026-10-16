import { postJSON } from './api';
import type { PreviewResult } from './previews';

interface PreviewRequest {
  text: string;
  checksSha?: string;
  refreshChecks?: boolean;
  refresh?: boolean;
  prStatus?: boolean;
  openGraph?: boolean;
  render?: boolean;
}
interface Result { previews: PreviewResult[]; error?: string }
interface Reader { resolve: (value: PreviewResult[]) => void; reject: (error: unknown) => void }
interface Entry {
  key: string;
  owner: string;
  body: PreviewRequest;
  readers: Set<Reader>;
  controller?: AbortController;
}

const entries = new Map<string, Entry>();
let pending: Entry[] = [];
let timer: ReturnType<typeof setTimeout> | undefined;

/** Coalesce conversation cards, including duplicate links, without sharing owners. */
export function requestPreview(body: PreviewRequest, owner: string, signal?: AbortSignal): Promise<PreviewResult[]> {
  if (signal?.aborted) return Promise.reject(signal.reason);
  const key = JSON.stringify([owner, body]);
  let entry = entries.get(key);
  if (!entry) {
    entry = { key, owner, body, readers: new Set() };
    entries.set(key, entry);
    pending.push(entry);
    if (body.render) timer ??= setTimeout(flush, 20);
    else {
      clearTimeout(timer);
      timer = undefined;
      queueMicrotask(flush);
    }
  }
  const current = entry;
  return new Promise((resolve, reject) => {
    const finish = () => signal?.removeEventListener('abort', abort);
    const reader: Reader = {
      resolve: (value) => { finish(); resolve(value); },
      reject: (error) => { finish(); reject(error); },
    };
    const abort = () => {
      current.readers.delete(reader);
      reader.reject(signal?.reason);
      if (!current.readers.size) {
        if (entries.get(key) === current) entries.delete(key);
        const controller = current.controller;
        if (controller && ![...entries.values()].some((e) => e.controller === controller && e.readers.size)) controller.abort();
        if (pending.length && pending.every((e) => !e.readers.size)) {
          clearTimeout(timer);
          timer = undefined;
          pending = [];
        }
      }
    };
    current.readers.add(reader);
    signal?.addEventListener('abort', abort, { once: true });
  });
}

function flush() {
  timer = undefined;
  const groups = new Map<string, Entry[]>();
  for (const entry of pending) {
    if (!entry.readers.size) continue;
    const group = groups.get(entry.owner) ?? [];
    group.push(entry);
    groups.set(entry.owner, group);
  }
  pending = [];
  for (const group of groups.values()) {
    // The server accepts at most 32 lookups and a 1 MiB JSON body.
    let batch: Entry[] = [];
    let bytes = 32;
    for (const entry of group) {
      const size = new TextEncoder().encode(JSON.stringify(entry.body)).length + 1;
      if (batch.length && (batch.length === 32 || bytes + size > 1_000_000)) {
        void send(batch);
        batch = [];
        bytes = 32;
      }
      batch.push(entry);
      bytes += size;
    }
    if (batch.length) void send(batch);
  }
}

async function send(batch: Entry[]) {
  const controller = new AbortController();
  for (const entry of batch) entry.controller = controller;
  const body = batch.length === 1 ? batch[0].body : { requests: batch.map((e) => e.body) };
  try {
    const response = await postJSON<Result & { results?: Result[] }>(`/api/previews/resolve?remoteId=${encodeURIComponent(batch[0].owner)}`, body, { signal: controller.signal });
    const results = batch.length === 1 ? [response] : response.results;
    if (!results || results.length !== batch.length) throw new Error('Invalid preview batch response');
    batch.forEach((entry, i) => {
      const result = results[i];
      for (const reader of entry.readers) {
        if (result.error || !Array.isArray(result.previews)) reader.reject(new Error(result.error || 'Invalid preview response'));
        else reader.resolve(result.previews);
      }
    });
  } catch (error) {
    for (const entry of batch) for (const reader of entry.readers) reader.reject(error);
  } finally {
    for (const entry of batch) {
      if (entries.get(entry.key) === entry) entries.delete(entry.key);
      entry.readers.clear();
    }
  }
}
