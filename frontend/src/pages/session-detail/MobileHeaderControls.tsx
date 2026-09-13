import { useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import type { UseMobilePanelResult } from './useMobilePanel';

/** Mounts session controls into a slot owned by the top-level header. */
export function HeaderPortal({
  children,
  slot = 'header-actions-slot',
}: {
  children: React.ReactNode;
  slot?: string;
}) {
  const [target, setTarget] = useState<HTMLElement | null>(null);
  useLayoutEffect(() => {
    // eslint-disable-next-line react-hooks/set-state-in-effect -- syncing with an external DOM node owned by <Header />; documented as a legitimate use of setState-in-effect.
    setTarget(document.getElementById(slot));
  }, [slot]);
  if (!target) return null;
  return createPortal(children, target);
}

/** The header-slot toggles for the phone overlays. */
export function MobileHeaderControls({
  mobilePanel,
  toggleMobileSidebar,
  toggleMobileDetails,
}: Omit<UseMobilePanelResult, 'closeMobilePanel'>) {
  return (
    <>
      <HeaderPortal slot="header-navigation-slot">
        {mobilePanel !== 'sidebar' && (
          <button
            type="button"
            className="mobile-sessions-back"
            data-testid="mobile-sessions-toggle"
            aria-label="Open session list"
            aria-expanded="false"
            onClick={toggleMobileSidebar}
          >
            <i className="bi bi-chevron-left" aria-hidden="true" />
            <span>Sessions</span>
          </button>
        )}
      </HeaderPortal>
      <HeaderPortal slot="header-mobile-title-slot">
        {mobilePanel === 'sidebar' && <span>Sessions</span>}
      </HeaderPortal>
      <HeaderPortal>
        {mobilePanel === 'sidebar' && (
          <button
            type="button"
            className="mobile-sessions-done"
            data-testid="mobile-sessions-toggle"
            aria-label="Close session list"
            aria-expanded="true"
            onClick={toggleMobileSidebar}
          >
            Done
          </button>
        )}
        {mobilePanel !== 'sidebar' && (
          <button
            type="button"
            className="mobile-panel-toggle"
            data-testid="mobile-details-toggle"
            aria-label={mobilePanel === 'details' ? 'Close session details' : 'Open session details'}
            aria-expanded={mobilePanel === 'details'}
            onClick={toggleMobileDetails}
          >
            <i className={`bi ${mobilePanel === 'details' ? 'bi-x-lg' : 'bi-layout-sidebar-reverse'}`} aria-hidden="true" />
          </button>
        )}
      </HeaderPortal>
    </>
  );
}
