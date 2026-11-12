export const BUILD_CHECK_MS = 3 * 60_000;
export const BUILD_REFRESH_MS = 60_000;

/** Transport failures, old deployments and SPA fallbacks are not updates. */
export async function deployedBuildHash(signal: AbortSignal): Promise<string | null> {
  try {
    const response = await fetch('/build-hash.json', { signal, cache: 'no-store' });
    if (!response.ok) return null;
    const value: unknown = await response.json();
    const hash = (value as { hash?: unknown } | null)?.hash;
    return typeof hash === 'string' && /^[a-f0-9]{64}$/.test(hash) ? hash : null;
  } catch {
    return null;
  }
}

export function watchBuildUpdate(currentHash: string, changed: (hash: string) => void): () => void {
  let stopped = false;
  let detected = false;
  let request: AbortController | undefined;
  const check = async () => {
    if (stopped || detected || document.hidden || request) return;
    const controller = new AbortController();
    request = controller;
    const timeout = setTimeout(() => controller.abort(), 10_000);
    try {
      const hash = await deployedBuildHash(controller.signal);
      if (!stopped && hash && hash !== currentHash) { detected = true; changed(hash); }
    } finally {
      clearTimeout(timeout);
      request = undefined;
    }
  };
  void check();
  const timer = setInterval(() => void check(), BUILD_CHECK_MS);
  const visible = () => { if (!document.hidden) void check(); };
  document.addEventListener('visibilitychange', visible);
  window.addEventListener('online', visible);
  return () => {
    stopped = true;
    clearInterval(timer);
    request?.abort();
    document.removeEventListener('visibilitychange', visible);
    window.removeEventListener('online', visible);
  };
}
