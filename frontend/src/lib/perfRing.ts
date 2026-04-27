/**
 * perfRing — a tiny in-memory ring buffer of recent API call timings,
 * exposed to operators via `window.__ocmanPerf`.
 *
 * Why this exists: the backend timing middleware tells us how slow
 * each handler is server-side. perfRing tells us what the *client*
 * sees, including network and serialization overhead. Pairing the
 * two answers "is the slowness on the wire or on the box?".
 *
 * Storage: capped to PERF_RING_CAPACITY entries; new entries evict
 * the oldest. No persistence — diagnostics only.
 *
 * Privacy: URLs are normalized via templatePath() so session IDs and
 * directories aren't kept in the ring. Query strings are stripped.
 */

export interface PerfEntry {
  /** Templated URL (e.g. "/api/session/:id/info"). */
  pathTemplate: string;
  /** HTTP method, uppercase. */
  method: string;
  /** HTTP status code. 0 = network error / aborted. */
  status: number;
  /** Wall-clock duration including JSON parse, in milliseconds. */
  durationMs: number;
  /** performance.now() value at request start. */
  startedAt: number;
}

export interface PerfSummaryRow {
  pathTemplate: string;
  count: number;
  avgMs: number;
  p50Ms: number;
  p95Ms: number;
  maxMs: number;
  errorCount: number;
}

const PERF_RING_CAPACITY = 100;

const ring: PerfEntry[] = [];
let writeCursor = 0;

/**
 * templatePath strips query strings and replaces likely-identifier
 * segments with placeholders so the summary aggregates by endpoint
 * shape rather than by individual session/directory.
 *
 * Heuristics (in order):
 *   - drop the query string
 *   - any segment matching a UUID-ish or hex-ish pattern → :id
 *   - any segment that's a long base64-ish blob → :id
 *   - any segment that *contains* an encoded slash (%2F) → :path
 *
 * The result is intentionally lossy. We're aggregating for
 * percentile rollups, not preserving routing detail.
 */
export function templatePath(rawUrl: string): string {
  let path = rawUrl;
  const qIdx = path.indexOf('?');
  if (qIdx >= 0) path = path.slice(0, qIdx);

  const segments = path.split('/');
  for (let i = 0; i < segments.length; i++) {
    const s = segments[i];
    if (!s) continue;
    if (/^[0-9a-fA-F-]{8,}$/.test(s)) {
      segments[i] = ':id';
      continue;
    }
    if (s.length >= 16 && /^[A-Za-z0-9_-]+$/.test(s)) {
      segments[i] = ':id';
      continue;
    }
    if (s.includes('%2F') || s.includes('%2f')) {
      segments[i] = ':path';
      continue;
    }
  }
  return segments.join('/');
}

export function record(entry: PerfEntry): void {
  if (ring.length < PERF_RING_CAPACITY) {
    ring.push(entry);
  } else {
    ring[writeCursor] = entry;
    writeCursor = (writeCursor + 1) % PERF_RING_CAPACITY;
  }
}

/**
 * snapshot returns a copy of all entries in chronological order
 * (oldest first), suitable for `console.table` and the like.
 */
export function snapshot(): PerfEntry[] {
  if (ring.length < PERF_RING_CAPACITY) {
    return ring.slice();
  }
  // Ring is full — splice from cursor to wrap correctly.
  return ring.slice(writeCursor).concat(ring.slice(0, writeCursor));
}

/**
 * summary groups entries by pathTemplate and reports basic
 * percentiles. Sorted by max latency descending so the worst
 * offenders show up first. Cheap O(n log n); fine to call ad-hoc
 * from devtools.
 */
export function summary(): PerfSummaryRow[] {
  const groups = new Map<string, number[]>();
  const errors = new Map<string, number>();
  for (const e of ring) {
    if (!e) continue;
    let arr = groups.get(e.pathTemplate);
    if (!arr) {
      arr = [];
      groups.set(e.pathTemplate, arr);
    }
    arr.push(e.durationMs);
    if (e.status === 0 || e.status >= 400) {
      errors.set(e.pathTemplate, (errors.get(e.pathTemplate) ?? 0) + 1);
    }
  }
  const rows: PerfSummaryRow[] = [];
  for (const [pathTemplate, durations] of groups) {
    const sorted = durations.slice().sort((a, b) => a - b);
    const sum = sorted.reduce((a, b) => a + b, 0);
    rows.push({
      pathTemplate,
      count: sorted.length,
      avgMs: Math.round(sum / sorted.length),
      p50Ms: percentile(sorted, 0.5),
      p95Ms: percentile(sorted, 0.95),
      maxMs: sorted[sorted.length - 1],
      errorCount: errors.get(pathTemplate) ?? 0,
    });
  }
  rows.sort((a, b) => b.maxMs - a.maxMs);
  return rows;
}

/**
 * clear empties the ring. Exposed so a user can isolate timings for
 * a specific user action: `__ocmanPerf.clear()`, then click around,
 * then `console.table(__ocmanPerf.summary())`.
 */
export function clear(): void {
  ring.length = 0;
  writeCursor = 0;
}

function percentile(sortedAsc: number[], q: number): number {
  if (sortedAsc.length === 0) return 0;
  const idx = Math.min(sortedAsc.length - 1, Math.floor(q * sortedAsc.length));
  return Math.round(sortedAsc[idx]);
}

// ---------------------------------------------------------------------------
// window.__ocmanPerf — devtools surface.
//
// We attach lazily so the symbol is undefined in non-browser test
// environments (Node, jsdom-less vitest). Production builds simply
// attach it on first import.
// ---------------------------------------------------------------------------

declare global {
  interface Window {
    __ocmanPerf?: {
      entries: () => PerfEntry[];
      summary: () => PerfSummaryRow[];
      clear: () => void;
      capacity: number;
    };
  }
}

export function installDevHandle(): void {
  if (typeof window === 'undefined') return;
  // Idempotent — safe to call from multiple module imports.
  if (window.__ocmanPerf) return;
  window.__ocmanPerf = {
    entries: snapshot,
    summary,
    clear,
    capacity: PERF_RING_CAPACITY,
  };
}

// Reset helpers for tests. NOT exported via window.__ocmanPerf.
export function _resetForTests(): void {
  clear();
}
