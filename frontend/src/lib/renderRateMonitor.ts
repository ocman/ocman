// Tiny diagnostic helper that counts render frequency per key and logs
// a warning when a per-second budget is exceeded. Gated on the URL
// `?debug` flag so it stays completely out of production builds.
//
// USE
//
//   // In any render body:
//   trackRender('SessionDetail', { id });
//
//   // In a hook that runs on every render:
//   trackRender('useSessionStatus', { id, status });
//
// When the per-second count for a key exceeds DEFAULT_BUDGET, the
// helper logs a warning (via remoteLog so it shows up in both the
// browser console AND the server's air log) with the count, the
// most recent props, and a sampled stack so the offending hot path
// is identifiable. The remoteLog forward is what makes this useful
// in environments where opening devtools is inconvenient — every
// warning ends up in `make dev-backend`'s output.
//
// The helper also installs `window.__ocmanRenderRates` so operators
// can inspect / reset counters from devtools:
//
//   __ocmanRenderRates.snapshot()   // recent counts per key
//   __ocmanRenderRates.reset()      // zero everything
//   __ocmanRenderRates.setBudget(n) // change the warn threshold
//
// Logs are intentionally rate-limited: after a key trips the warn
// budget once we suppress further warnings for that key for 2s so
// the console doesn't drown in repeats while the loop is active.

import { remoteLog } from './remoteLog';

const WINDOW_MS = 1000;
// Browser paint rate is capped at ~60 Hz; we want to warn only when a
// component is genuinely running away (e.g. >100 renders/sec from a
// setState-in-effect cycle). React StrictMode + React Compiler's
// verification render double-invoke components in dev, so a budget
// of 30 trips on perfectly-healthy streaming code; bump to 80 so the
// warning identifies real loops rather than dev-mode amplification.
const DEFAULT_BUDGET = 80;
const WARN_COOLDOWN_MS = 2000;

interface KeyState {
  windowStart: number;
  count: number;
  totalCount: number;
  lastWarnAt: number;
  lastProps: unknown;
}

const state = new Map<string, KeyState>();
let budget = DEFAULT_BUDGET;
let enabled: boolean | null = null;

function isEnabled(): boolean {
  if (enabled !== null) return enabled;
  if (typeof window === 'undefined') {
    enabled = false;
    return false;
  }
  try {
    const sp = new URLSearchParams(window.location.search);
    enabled = sp.has('debug');
  } catch {
    enabled = false;
  }
  return enabled;
}

/**
 * Record a render for `key`. When called more than `budget` times in
 * a one-second window, emits a `console.warn` (rate-limited per
 * key) with the most-recent props for inspection.
 *
 * No-op when the page isn't loaded with `?debug`. Safe to call from
 * a render body — every operation is O(1) and writes never trigger
 * React state updates.
 */
export function trackRender(key: string, props?: unknown): void {
  if (!isEnabled()) return;
  const now = Date.now();
  let entry = state.get(key);
  if (!entry) {
    entry = {
      windowStart: now,
      count: 0,
      totalCount: 0,
      lastWarnAt: 0,
      lastProps: props,
    };
    state.set(key, entry);
  }
  entry.lastProps = props;
  entry.totalCount += 1;
  if (now - entry.windowStart >= WINDOW_MS) {
    entry.windowStart = now;
    entry.count = 1;
    return;
  }
  entry.count += 1;
  if (entry.count > budget && now - entry.lastWarnAt > WARN_COOLDOWN_MS) {
    entry.lastWarnAt = now;
    // Capture a stack so we can see who's driving the renders.
    const stack = new Error().stack;
    // Use remoteLog so the warning is forwarded to the backend (and
    // therefore visible in `make dev-backend` / air logs) in addition
    // to the browser console.
    remoteLog.warn(
      `[ocman:render-rate] "${key}" rendered ${entry.count} times in <1s ` +
      `(total ${entry.totalCount}). Likely render loop or runaway effect.`,
      {
        key,
        countInWindow: entry.count,
        totalCount: entry.totalCount,
        props,
        stack,
      },
    );
  }
}

/**
 * Log a one-shot diagnostic the first time a value changes (per
 * key). Useful for capturing "URL changed at T, page state caught
 * up at T'" timings without spamming the console on every render.
 *
 * The helper records each (key, value) pair it has seen; subsequent
 * calls with the same value are silent. Reset via the devtools
 * handle.
 */
const oneShotState = new Map<string, unknown>();
export function logChange(key: string, value: unknown): void {
  if (!isEnabled()) return;
  const prev = oneShotState.get(key);
  if (prev === value) return;
  oneShotState.set(key, value);
  // Use remoteLog so the change event is forwarded to the backend log.
  remoteLog.info(`[ocman:change] ${key}`, {
    key,
    value,
    at: performance.now(),
  });
}

interface DevHandle {
  snapshot(): Record<string, { count: number; totalCount: number; lastProps: unknown }>;
  reset(): void;
  setBudget(n: number): void;
  enable(): void;
}

function devHandle(): DevHandle {
  return {
    snapshot() {
      const out: Record<string, { count: number; totalCount: number; lastProps: unknown }> = {};
      for (const [k, v] of state) {
        out[k] = { count: v.count, totalCount: v.totalCount, lastProps: v.lastProps };
      }
      return out;
    },
    reset() {
      state.clear();
      oneShotState.clear();
    },
    setBudget(n: number) {
      budget = n;
    },
    enable() {
      enabled = true;
    },
  };
}

if (typeof window !== 'undefined') {
  // Install eagerly so the handle is available even without ?debug
  // — operators can still call __ocmanRenderRates.enable() from
  // devtools to flip the switch on a live session without reloading.
  (window as unknown as { __ocmanRenderRates: DevHandle }).__ocmanRenderRates = devHandle();
}
