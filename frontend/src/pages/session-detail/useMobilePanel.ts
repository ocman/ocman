import { useCallback, useEffect, useState } from 'react';
import { useUiStore } from '../../lib/uiStore';

export type MobilePanel = 'sidebar' | 'details' | null;

export interface UseMobilePanelResult {
  mobilePanel: MobilePanel;
  toggleMobileSidebar: () => void;
  toggleMobileDetails: () => void;
  closeMobilePanel: () => void;
}

/**
 * Phone-only overlay panels (sessions drawer / details panel). On
 * viewports <=768px the sidebar and right panel are hidden by default
 * and open as full-screen overlays via header toggles; the classes this
 * state drives are inert on wider viewports (see the @media block in
 * SessionDetail.css). Escape closes; any route change closes.
 */
export function useMobilePanel(id: string | undefined): UseMobilePanelResult {
  const [mobilePanel, setMobilePanel] = useState<MobilePanel>(null);
  const toggleMobileSidebar = useCallback(() => {
    setMobilePanel((p) => (p === 'sidebar' ? null : 'sidebar'));
  }, []);
  const toggleMobileDetails = useCallback(() => {
    // Seeding happens OUTSIDE the setState updater: updaters must be
    // pure (StrictMode double-invokes them), so the store mutation
    // can't live inside one.
    const opening = mobilePanel !== 'details';
    if (opening) {
      // The right panel may be collapsed (no open panes) from a
      // desktop session; a full-screen overlay with only the icon
      // strip reads as broken, so seed one pane. Deliberately
      // persisted: the desktop later reopens with that pane, which
      // beats snapshot/restore bookkeeping for a rare case.
      const ui = useUiStore.getState();
      if (ui.changesSidebarOpenTabs.length === 0) ui.toggleChangesSidebarTab('info');
    }
    setMobilePanel(opening ? 'details' : null);
  }, [mobilePanel]);
  const closeMobilePanel = useCallback(() => setMobilePanel(null), []);
  useEffect(() => {
    if (!mobilePanel) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMobilePanel(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [mobilePanel]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- closing a phone overlay in response to an external route change; no render loop (id only changes via navigation).
    setMobilePanel(null);
  }, [id]);
  return { mobilePanel, toggleMobileSidebar, toggleMobileDetails, closeMobilePanel };
}
