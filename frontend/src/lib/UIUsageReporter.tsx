import { useEffect } from 'react';
import { apiFetch, readJSON, raiseForUnauthorized } from './api.requests';
import { UIUsageTracker, USAGE_HEARTBEAT_MS } from './uiUsage';

export function UIUsageReporter() {
  useEffect(() => {
    let mounted = true;
    let inFlight = false;
    let offset = 0;
    let tracker: UIUsageTracker | undefined;
    const foreground = () => !document.hidden && document.hasFocus();
    const now = () => Date.now() + offset;
    const checkpoint = (interacted = false) => tracker?.checkpoint(now(), foreground(), interacted);
    const send = async (keepalive = false) => {
      checkpoint();
      if (!mounted || (inFlight && !keepalive)) return;
      const intervals = tracker?.snapshot() ?? [];
      if (tracker && intervals.length === 0) return;
      inFlight = true;
      try {
        const response = await apiFetch('/api/ui-usage', {
          method: 'POST', headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ intervals }), keepalive,
          signal: AbortSignal.timeout(10_000),
        });
        await raiseForUnauthorized(response);
        if (response.status === 400) {
          tracker = undefined;
          return;
        }
        if (!response.ok) return;
        const result = await readJSON<{ now: number }>(response);
        if (!mounted) return;
        tracker?.acknowledge(intervals.at(-1)?.end ?? 0);
        const nextOffset = result.now - Date.now();
        // Recalibrate after sleep or clock changes; never replay a long gap.
        if (!tracker || Math.abs(nextOffset - offset) > 5_000) {
          offset = nextOffset;
          tracker = new UIUsageTracker(now(), foreground());
        }
      } catch {
        // Best-effort analytics: retry recent intervals at the next heartbeat.
      } finally {
        inFlight = false;
      }
    };
    const onInteraction = () => checkpoint(true);
    const onStateChange = () => { checkpoint(); void send(true); };
    const onFocus = () => { checkpoint(true); void send(); };
    const onPageHide = () => { checkpoint(); void send(true); };
    document.addEventListener('visibilitychange', onStateChange);
    window.addEventListener('focus', onFocus);
    window.addEventListener('blur', onStateChange);
    window.addEventListener('pagehide', onPageHide);
    for (const event of ['pointerdown', 'keydown', 'scroll']) window.addEventListener(event, onInteraction, { passive: true });
    const heartbeat = setInterval(() => { void send(); }, USAGE_HEARTBEAT_MS);
    void send();
    return () => {
      void send(true);
      mounted = false;
      clearInterval(heartbeat);
      document.removeEventListener('visibilitychange', onStateChange);
      window.removeEventListener('focus', onFocus);
      window.removeEventListener('blur', onStateChange);
      window.removeEventListener('pagehide', onPageHide);
      for (const event of ['pointerdown', 'keydown', 'scroll']) window.removeEventListener(event, onInteraction);
    };
  }, []);
  return null;
}
