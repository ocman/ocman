import { useLayoutEffect, useState } from 'react';
import { createPortal } from 'react-dom';
import { Link } from 'react-router-dom';
import { IconButton } from '../../components/IconButton';
import { useIsPhone } from '../../lib/useIsPhone';
import type { UseMobilePanelResult } from './useMobilePanel';
import './PhoneConversation.css';

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
  const phone = useIsPhone();
  return (
    <>
      <HeaderPortal slot="header-navigation-slot">
        {mobilePanel !== 'sidebar' && (
          <Link
            to="/sessions"
            className="mobile-sessions-back"
            aria-label="Back to sessions"
          >
            <i className="bi bi-chevron-left" aria-hidden="true" />
            <span className="mobile-sessions-back-label">Sessions</span>
          </Link>
        )}
      </HeaderPortal>
      <HeaderPortal slot="header-mobile-title-slot">
        {mobilePanel === 'sidebar' && <span>Sessions</span>}
      </HeaderPortal>
      {(!phone || mobilePanel === 'details') && <HeaderPortal key={mobilePanel} slot={phone ? 'phone-session-tools-slot' : 'header-actions-slot'}>
        {mobilePanel !== 'sidebar' && (
          <IconButton
            variant="ghost"
            className="mobile-panel-toggle"
            data-testid="mobile-sessions-toggle"
            label="Open session list"
            icon="bi-list-ul"
            aria-expanded="false"
            onClick={toggleMobileSidebar}
          />
        )}
      </HeaderPortal>}
      <HeaderPortal>
        {!phone && mobilePanel === 'sidebar' && (
          <IconButton variant="ghost" className="mobile-panel-toggle"
            label="Close session list" icon="bi-x-lg" aria-expanded="true"
            onClick={toggleMobileSidebar} />
        )}
        {mobilePanel !== 'sidebar' && (
          <IconButton
            variant="ghost"
            className="mobile-panel-toggle"
            data-testid="mobile-details-toggle"
            label={mobilePanel === 'details' ? 'Close session details' : 'Open session details'}
            icon={mobilePanel === 'details' ? 'bi-x-lg' : 'bi-layout-sidebar-reverse'}
            aria-expanded={mobilePanel === 'details'}
            onClick={toggleMobileDetails}
          />
        )}
      </HeaderPortal>
    </>
  );
}
