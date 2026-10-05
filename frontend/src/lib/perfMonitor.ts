/**
 * perfMonitor — opt-in client-side interaction-performance measurement.
 *
 * Off by default and free when off: every public function returns after one
 * cached boolean check. Enable with `?debug` in the URL or
 * `localStorage.setItem('ocman:perf', '1')` (the latter avoids the SSE debug
 * overlay `?debug` also turns on, so it is what the bench uses).
 *
 * What it records, each as a series of numbers keyed by metric + label:
 *   - `interaction`  Event Timing entries (INP-like), keyed by event type and
 *                    the nearest `[data-perf]` label of the target.
 *   - `loaf`         long-animation-frame duration / blocking duration, plus
 *                    the top script attribution.
 *   - `sse.batch`    reducer time per SSE batch (+ events / messages / parts).
 *   - `sse.commit`   SSE flush → React commit for that batch.
 *   - `render`       React Profiler actualDuration per profiled subtree.
 *   - `switch`       session switch → first frame after the new thread renders,
 *                    tagged `hit` / `miss` (session cache).
 *   - Server-Timing  copied onto perfRing entries from resource timing.
 *
 * Read with `window.__ocmanPerf.metrics()`; reset with `resetMetrics()`.
 * A summary is beaconed to `POST /api/debug/log` when the tab is hidden.
 *
 * Entries are taken from PerformanceObservers, never from the global
 * performance buffer, so usePerformanceCleanup's periodic
 * clearMarks/clearMeasures/clearResourceTimings cannot drop them.
 */
import { attachServerTiming, installDevHandle, templatePath } from './perfRing';

const SERIES_CAP = 2000;
const FLAG_KEY = 'ocman:perf';

let enabled: boolean | null = null;

export function perfEnabled(): boolean {
  if (enabled !== null) return enabled;
  enabled = false;
  if (typeof window === 'undefined') return false;
  try {
    enabled = new URLSearchParams(window.location.search).has('debug')
      || window.localStorage?.getItem(FLAG_KEY) === '1';
  } catch { /* storage blocked */ }
  return enabled;
}

const series = new Map<string, number[]>();
const counters = new Map<string, number>();
const loafScripts = new Map<string, number>();

/** Append one sample to `metric[label]`. No-op when disabled. */
export function recordSample(metric: string, label: string, value: number): void {
  if (!perfEnabled()) return;
  const key = `${metric}|${label}`;
  let arr = series.get(key);
  if (!arr) series.set(key, (arr = []));
  if (arr.length >= SERIES_CAP) arr.shift();
  arr.push(value);
}

function bump(key: string, by = 1) {
  counters.set(key, (counters.get(key) ?? 0) + by);
}

// ---------------------------------------------------------------------------
// SSE batches
// ---------------------------------------------------------------------------

let pendingFlushAt: number | null = null;

/** Wrap a reducer call for one SSE batch. */
export function timeSseBatch<T>(events: number, run: () => T, size?: (out: T) => [number, number]): T {
  if (!perfEnabled()) return run();
  const t0 = performance.now();
  const out = run();
  recordSample('sse.batch', 'reduce', performance.now() - t0);
  recordSample('sse.batch', 'events', events);
  if (size) {
    const [messages, parts] = size(out);
    recordSample('sse.batch', 'messages', messages);
    recordSample('sse.batch', 'parts', parts);
  }
  return out;
}

/** Called at SSE flush time; the matching commit closes the span. */
export function markSseFlush(): void {
  if (!perfEnabled() || pendingFlushAt !== null) return;
  pendingFlushAt = performance.now();
}

/** Called from a layout effect after a view commit. */
export function markSseCommit(): void {
  if (pendingFlushAt === null) return;
  recordSample('sse.commit', 'flush→commit', performance.now() - pendingFlushAt);
  pendingFlushAt = null;
}

// ---------------------------------------------------------------------------
// Session switch
// ---------------------------------------------------------------------------

let pendingSwitch: { id: string; at: number; cacheHit: boolean } | null = null;

/** `cacheHit`: the session cache already held `nextId` at click time. */
export function markSessionSwitchStart(nextId: string, cacheHit: boolean): void {
  if (!perfEnabled()) return;
  pendingSwitch = { id: nextId, at: performance.now(), cacheHit };
  performance.mark(`ocman:switch:${nextId}`);
}

/** Call once the thread for `id` has rendered; resolves on the next frame. */
export function markSessionSwitchRendered(id: string): void {
  const start = pendingSwitch;
  if (!start || start.id !== id) return;
  pendingSwitch = null;
  const label = start.cacheHit ? 'hit' : 'miss';
  requestAnimationFrame(() => {
    const ms = performance.now() - start.at;
    recordSample('switch', label, ms);
    try { performance.measure(`ocman:switch:${label}`, { start: start.at, duration: ms }); } catch { /* old browsers */ }
  });
}

// ---------------------------------------------------------------------------
// React Profiler
// ---------------------------------------------------------------------------

/** Render counter fed by renderRateMonitor.trackRender. */
export function countRender(key: string): void {
  bump(`renders|${key}`);
}

export function onProfilerRender(id: string, phase: string, actualDuration: number): void {
  recordSample('render', id, actualDuration);
  bump(`commits|${id}|${phase}`);
}

// ---------------------------------------------------------------------------
// Observers
// ---------------------------------------------------------------------------

interface EventTimingEntry extends PerformanceEntry {
  interactionId?: number;
  target?: Node | null;
  processingStart: number;
  processingEnd: number;
}
interface LoafScript { invoker?: string; sourceURL?: string; sourceFunctionName?: string; duration: number }
interface LoafEntry extends PerformanceEntry { blockingDuration: number; scripts?: LoafScript[] }
interface ResourceEntry extends PerformanceEntry {
  serverTiming?: ReadonlyArray<{ name: string; duration: number; description: string }>;
}

// Handlers without a meaningful event target (global hotkeys) tag the
// interaction explicitly; the tag is matched by processing-time overlap.
const tags: { label: string; at: number }[] = [];

/** Label the interaction whose handler is running now (e.g. a hotkey). */
export function labelInteraction(label: string): void {
  if (!perfEnabled()) return;
  tags.push({ label, at: performance.now() });
  if (tags.length > 20) tags.shift();
}

function taggedLabel(e: EventTimingEntry): string | undefined {
  return tags.find((t) => t.at >= e.processingStart && t.at <= e.processingEnd)?.label;
}

export function perfLabel(target: Node | null | undefined): string {
  const el = target instanceof Element ? target : target?.parentElement;
  return el?.closest('[data-perf]')?.getAttribute('data-perf') ?? 'other';
}

function observe(type: string, cb: (entries: PerformanceEntry[]) => void, opts: Record<string, unknown> = {}) {
  try {
    new PerformanceObserver((list) => cb(list.getEntries())).observe({ type, buffered: true, ...opts } as PerformanceObserverInit);
  } catch { /* entry type unsupported */ }
}

let installed = false;

export function installPerfMonitor(): void {
  installDevHandle();
  if (!perfEnabled() || installed || typeof PerformanceObserver === 'undefined') return;
  installed = true;
  Object.assign(window.__ocmanPerf!, { metrics: summary, resetMetrics: reset });

  observe('event', (entries) => {
    // One interaction has several entries (pointerdown/up/click); keep the
    // longest per interactionId so a click counts once.
    const longest = new Map<number, EventTimingEntry>();
    for (const e of entries as EventTimingEntry[]) {
      if (!e.interactionId) continue;
      const prev = longest.get(e.interactionId);
      if (!prev || e.duration > prev.duration) longest.set(e.interactionId, e);
    }
    for (const e of longest.values()) {
      const type = e.name.startsWith('key') ? 'keyboard' : e.name.startsWith('pointer') || e.name === 'click' ? 'pointer' : e.name;
      recordSample('interaction', `${type}:${taggedLabel(e) ?? perfLabel(e.target)}`, e.duration);
      recordSample('interaction', 'all', e.duration);
    }
  }, { durationThreshold: 16 });

  observe('long-animation-frame', (entries) => {
    for (const e of entries as LoafEntry[]) {
      recordSample('loaf', 'duration', e.duration);
      recordSample('loaf', 'blocking', e.blockingDuration);
      const top = (e.scripts ?? []).reduce<LoafScript | null>((a, s) => (!a || s.duration > a.duration ? s : a), null);
      if (top) {
        const file = (top.sourceURL ?? '').split('/').pop() || '?';
        const key = `${top.invoker ?? '?'} @ ${top.sourceFunctionName || 'anon'} (${file})`;
        loafScripts.set(key, (loafScripts.get(key) ?? 0) + top.duration);
      }
    }
  });

  observe('resource', (entries) => {
    for (const e of entries as ResourceEntry[]) {
      if (!e.serverTiming?.length) continue;
      let path: string;
      try { path = new URL(e.name).pathname; } catch { continue; }
      if (!path.startsWith('/api/')) continue;
      attachServerTiming(templatePath(path), e.startTime, e.serverTiming.map((t) => ({ name: t.name, durationMs: t.duration })));
    }
  });

  document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'hidden') beacon();
  });
}

// ---------------------------------------------------------------------------
// Reporting
// ---------------------------------------------------------------------------

export interface SeriesSummary { count: number; p50: number; p95: number; max: number; sum: number }

function pct(sorted: number[], q: number) {
  return sorted.length ? sorted[Math.min(sorted.length - 1, Math.floor(q * sorted.length))] : 0;
}

export function summarize(values: number[]): SeriesSummary {
  const s = values.slice().sort((a, b) => a - b);
  const r = (n: number) => Math.round(n * 100) / 100;
  return { count: s.length, p50: r(pct(s, 0.5)), p95: r(pct(s, 0.95)), max: r(s[s.length - 1] ?? 0), sum: r(s.reduce((a, b) => a + b, 0)) };
}

export function summary() {
  const metrics: Record<string, Record<string, SeriesSummary>> = {};
  for (const [key, values] of series) {
    const [metric, label] = key.split('|');
    (metrics[metric] ??= {})[label] = summarize(values);
  }
  const topLoafScripts = [...loafScripts].sort((a, b) => b[1] - a[1]).slice(0, 10)
    .map(([script, ms]) => ({ script, ms: Math.round(ms) }));
  return { metrics, counters: Object.fromEntries(counters), topLoafScripts };
}

export function reset(): void {
  series.clear();
  counters.clear();
  loafScripts.clear();
  tags.length = 0;
  pendingFlushAt = null;
  pendingSwitch = null;
}

function beacon() {
  if (series.size === 0 || typeof navigator.sendBeacon !== 'function') return;
  const body = JSON.stringify({ level: 'info', message: '[ocman:perf] summary', data: summary() });
  navigator.sendBeacon('/api/debug/log', new Blob([body], { type: 'application/json' }));
  reset();
}

/** Test seam. */
export function _resetPerfMonitorForTests(force?: boolean): void {
  enabled = force ?? null;
  installed = false;
  reset();
}
