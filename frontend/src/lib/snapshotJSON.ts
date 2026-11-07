import { apiFetch, queryString, readJSON, throwForStatus } from './api.requests';
import { record, templatePath } from './perfRing';

// Only the session snapshot endpoints opt in. Never reuse data after an HTTP
// failure, and retain a bounded set of immutable server representations.
const snapshots = new Map<string, { etag: string; data: unknown }>();
const MAX_SNAPSHOTS = 64;
const sampledAt = new Map<string, number>();

// A shared snapshot may already be several seconds old. Charge that age against
// the next recovery interval instead of adding a second full polling window.
export function snapshotPollingDelay(path: string, params: Record<string, string | number | undefined>, budget: number): number {
  const sample = sampledAt.get(path + queryString(params));
  return sample === undefined ? budget : Math.max(250, budget - (Date.now() - sample));
}

export async function snapshotJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const cached = snapshots.get(url);
  const startedAt = performance.now();
  let status = 0;
  try {
    const response = await apiFetch(url, {
      signal, cache: 'no-store',
      ...(cached && { headers: { 'If-None-Match': cached.etag } }),
    });
    status = response.status;
    const age = Math.max(0, Number(response.headers.get('X-Ocman-Snapshot-Age-Ms') ?? 0));
    sampledAt.delete(url);
    sampledAt.set(url, Date.now() - (Number.isFinite(age) ? age : 0));
    while (sampledAt.size > MAX_SNAPSHOTS) sampledAt.delete(sampledAt.keys().next().value!);
    if (status === 304 && cached) return structuredClone(cached.data) as T;
    if (!response.ok) {
      snapshots.delete(url);
      sampledAt.delete(url);
      if (status === 401) clearSessionSnapshots();
      await throwForStatus(response);
    }
    const data = await readJSON<T>(response);
    const etag = response.headers.get('ETag');
    snapshots.delete(url);
    if (etag && Number(response.headers.get('Content-Length') ?? 0) <= 2 * 1024 * 1024) {
      snapshots.set(url, { etag, data: structuredClone(data) });
      while (snapshots.size > MAX_SNAPSHOTS) snapshots.delete(snapshots.keys().next().value!);
    }
    return data;
  } catch (error) {
    sampledAt.delete(url);
    throw error;
  } finally {
    record({ pathTemplate: templatePath(url), method: 'GET', status, durationMs: performance.now() - startedAt, startedAt });
  }
}

export function clearSessionSnapshots() { snapshots.clear(); sampledAt.clear(); }
