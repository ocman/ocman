const guards = new Set<() => boolean>();

/** Mounted owners register live readers, independent of debounced persistence. */
export function registerAutomaticReloadGuard(blocked: () => boolean) {
  guards.add(blocked);
  return () => { guards.delete(blocked); };
}

export function hasAutomaticReloadGuards() {
  return [...guards].some(blocked => {
    try { return blocked(); }
    catch { return true; } // An unreadable owner cannot authorize a reload.
  });
}
