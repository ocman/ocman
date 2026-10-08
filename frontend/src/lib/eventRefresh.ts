/** Trailing event refresh, with one follow-up when events arrive during a fetch. */
export function eventRefresh(refresh: () => Promise<unknown>) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let running = false;
  let pending = false;
  let disposed = false;
  const run = async () => {
    timer = undefined;
    if (running || disposed) return;
    running = true;
    pending = false;
    try { await refresh(); }
    finally {
      running = false;
      if (pending && !timer && !disposed) timer = setTimeout(() => void run(), 150);
    }
  };
  return {
    schedule() {
      if (disposed) return;
      pending = true;
      clearTimeout(timer);
      timer = setTimeout(() => void run(), 150);
    },
    dispose() { disposed = true; clearTimeout(timer); },
  };
}
