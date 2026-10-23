import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useUiStore } from '../../lib/uiStore';
import { useLocation } from 'react-router-dom';
import { useIsPhone } from '../../lib/useIsPhone';
import { usePhoneOverlay } from '../../lib/usePhoneOverlay';

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
 * SessionDetail.css). Escape closes; route changes close the local state.
 * At phone widths the panel is a same-URL history entry, so native Back closes
 * it before leaving the conversation and Forward can reopen it.
 */
export function useMobilePanel(id: string | undefined): UseMobilePanelResult {
  const { pathname, search, key } = useLocation();
  const phone = useIsPhone();
  const [phonePanel, setPhonePanel] = usePhoneOverlay<'sidebar' | 'details'>('conversation-panel');
  const [localPanel, setLocalPanel] = useState<MobilePanel>(null);
  const mobilePanel = phone ? phonePanel : localPanel;
  const setMobilePanel = useCallback((panel: MobilePanel) => {
    if (phone) setPhonePanel(panel);
    else setLocalPanel(panel);
  }, [phone, setPhonePanel]);
  const toggleMobileSidebar = () => {
    setMobilePanel(mobilePanel === 'sidebar' ? null : 'sidebar');
  };
  const toggleMobileDetails = () => {
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
  };
  const setPanelRef = useRef(setMobilePanel);
  useLayoutEffect(() => { setPanelRef.current = setMobilePanel; }, [setMobilePanel]);
  const closeMobilePanel = useCallback(() => setPanelRef.current(null), []);
  useEffect(() => {
    if (!mobilePanel) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setMobilePanel(null);
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [mobilePanel, setMobilePanel]);
  useEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- closing a phone overlay in response to navigation, including draft-to-draft query changes.
    setLocalPanel(null);
  }, [id, pathname, search, key]);
  return { mobilePanel, toggleMobileSidebar, toggleMobileDetails, closeMobilePanel };
}
