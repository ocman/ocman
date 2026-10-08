/** Trail by 150 ms, at most 500 ms, retaining one follow-up during a fetch. */
export function eventRefresh(refresh: () => Promise<unknown>) {
  let timer: ReturnType<typeof setTimeout> | undefined;
  let running = false;
  let pending = false;
  let pendingSince = 0;
  let disposed = false;
  const delay = () => Math.max(0, Math.min(150, 500 - (Date.now() - pendingSince)));
  const run = async () => {
    timer = undefined;
    if (running || disposed) return;
    running = true;
    pending = false;
    try { await refresh(); }
    finally {
      running = false;
      if (pending && !timer && !disposed) timer = setTimeout(() => void run(), delay());
    }
  };
  return {
    schedule() {
      if (disposed) return;
      if (!pending) pendingSince = Date.now();
      pending = true;
      clearTimeout(timer);
      timer = setTimeout(() => void run(), delay());
    },
    dispose() { disposed = true; clearTimeout(timer); },
  };
}
