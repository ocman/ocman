import { onPageResume } from './pageResume';

/** Reconcile visible UI without background polling or overlapping reads. */
export function startVisiblePolling(load: () => Promise<unknown>, interval: number): () => void {
  let active = true;
  let running = false;
  let reconcile = false;
  let timer: ReturnType<typeof setTimeout> | undefined;
  const clear = () => { clearTimeout(timer); timer = undefined; };
  const refresh = () => {
    if (!active || document.hidden || running) return;
    clear();
    reconcile = false;
    running = true;
    void load().catch(() => { /* Callers own error reporting; keep retrying periodically. */ }).finally(() => {
      running = false;
      if (!active || document.hidden) return;
      if (reconcile) { reconcile = false; refresh(); }
      else timer = setTimeout(refresh, interval);
    });
  };
  const visibility = () => {
    clear();
    if (document.hidden) return;
    // A read started before hiding may not include changes made while away.
    if (running) reconcile = true;
    else refresh();
  };
  document.addEventListener('visibilitychange', visibility);
  window.addEventListener('focus', refresh);
  const stopResume = onPageResume(refresh);
  refresh();
  return () => {
    active = false;
    clear();
    stopResume();
    document.removeEventListener('visibilitychange', visibility);
    window.removeEventListener('focus', refresh);
  };
}
