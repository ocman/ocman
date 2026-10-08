import { useSyncExternalStore } from 'react';

function subscribeVisibility(notify: () => void) {
  document.addEventListener('visibilitychange', notify);
  return () => document.removeEventListener('visibilitychange', notify);
}

export function useDocumentVisible() {
  return useSyncExternalStore(subscribeVisibility, () => !document.hidden);
}

function subscribeViewport(notify: () => void) {
  window.addEventListener('resize', notify);
  return () => window.removeEventListener('resize', notify);
}

/** Matches SessionDetail.css: mobile drawers are hidden unless opened. */
export function usePanelOpen(mobileOpen: boolean) {
  const desktop = useSyncExternalStore(subscribeViewport, () => window.innerWidth > 768);
  return desktop || mobileOpen;
}

export function usePanelVisible(mobileOpen: boolean) {
  const visible = useDocumentVisible();
  const open = usePanelOpen(mobileOpen);
  return visible && open;
}
