import { useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { useClickOutside } from '../lib/useClickOutside';
import { useInbox } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { SubscriptionUsageContent } from '../pages/SubscriptionUsage';
import './MainNav.css';

const NAV_ITEMS = [
  { to: '/', label: 'Home', icon: 'bi-house', activeOnSession: true },
  { to: '/sessions', label: 'Sessions', icon: 'bi-collection' },
  { to: '/projects', label: 'Projects', icon: 'bi-folder' },
  { to: '/factory/overview', label: 'Factory', icon: 'bi-buildings' },
  { to: '/routines', label: 'Routines', icon: 'bi-clock-history' },
  { to: '/analytics', label: 'Analytics', icon: 'bi-bar-chart' },
];

export function MainNav({
  mobileOpen = false,
  onMobileClose,
}: {
  mobileOpen?: boolean;
  onMobileClose?: () => void;
}) {
  const location = useLocation();
  const collapsed = useUiStore((state) => state.mainNavCollapsed);
  const toggleCollapsed = useUiStore((state) => state.toggleMainNav);
  const inbox = useInbox();
  const usageRoot = useRef<HTMLDivElement>(null);
  const [usageOpen, setUsageOpen] = useState(false);
  const toggleLabel = mobileOpen
    ? 'Close navigation'
    : collapsed ? 'Expand navigation' : 'Collapse navigation';

  useClickOutside(usageRoot, usageOpen, () => setUsageOpen(false));

  const navLink = (item: (typeof NAV_ITEMS)[number], className?: string) => (
    <NavLink
      key={item.to}
      to={item.to}
      aria-label={item.label}
      title={collapsed ? item.label : undefined}
      className={({ isActive }) => [
        isActive || (item.activeOnSession && location.pathname.startsWith('/session/')) ? 'active' : '',
        className ?? '',
      ].filter(Boolean).join(' ') || undefined}
      onClick={onMobileClose}
    >
      <i className={`bi ${item.icon}`} aria-hidden="true" />
      <span>{item.label}</span>
    </NavLink>
  );

  const inboxLabel = inbox.data?.unreadTotal
    ? `Inbox, ${inbox.data.unreadTotal} unread messages`
    : 'Inbox';

  return (
    <>
      <aside className={`main-nav${collapsed ? ' collapsed' : ''}${mobileOpen ? ' mobile-open' : ''}`}>
        <button
          type="button"
          className="main-nav-logo"
          aria-label={toggleLabel}
          aria-expanded={mobileOpen || !collapsed}
          aria-controls="main-navigation"
          onClick={mobileOpen ? onMobileClose : toggleCollapsed}
        >
          <img src="/favicon.svg" alt="" width={22} height={22} />
          <span className="main-nav-brand">ocman</span>
        </button>
        <nav id="main-navigation" aria-label="Main navigation">
          {NAV_ITEMS.map((item) => navLink(item))}
          <NavLink
            to="/inbox"
            aria-label={inboxLabel}
            title={collapsed ? inboxLabel : undefined}
            className="nav-bottom-start"
            onClick={onMobileClose}
          >
            <i className="bi bi-inbox" aria-hidden="true" />
            <span>Inbox</span>
            {inbox.data?.unreadTotal ? <b className="nav-unread-badge">{inbox.data.unreadTotal > 99 ? '99+' : inbox.data.unreadTotal}</b> : null}
          </NavLink>
          <div className="main-nav-usage-root" ref={usageRoot} onKeyDown={(event) => event.key === 'Escape' && setUsageOpen(false)}>
            <button
              type="button"
              className={usageOpen ? 'main-nav-usage active' : 'main-nav-usage'}
              aria-label="Usage"
              aria-expanded={usageOpen}
              aria-controls="subscription-usage-popover"
              title={collapsed ? 'Usage' : undefined}
              onClick={() => setUsageOpen((open) => !open)}
            >
              <i className="bi bi-speedometer2" aria-hidden="true" />
              <span>Usage</span>
            </button>
            {usageOpen && (
              <div
                id="subscription-usage-popover"
                className="subscription-usage-popover"
                role="dialog"
                aria-label="Subscription usage"
              >
                <SubscriptionUsageContent compact />
              </div>
            )}
          </div>
          {navLink({ to: '/settings', label: 'Settings', icon: 'bi-gear' })}
        </nav>
      </aside>
      <button
        type="button"
        className={`main-nav-backdrop${mobileOpen ? ' visible' : ''}`}
        aria-label="Close navigation"
        onClick={onMobileClose}
      />
    </>
  );
}
