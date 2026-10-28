import { useRef, useState } from 'react';
import { NavLink, useLocation } from 'react-router-dom';
import { useInbox } from '../lib/queries';
import { useUiStore } from '../lib/uiStore';
import { useNotifySnapshot } from '../lib/useNotifyData';
import { attentionSessionEntries } from '../lib/sessionAttention';
import { SubscriptionUsageContent } from '../pages/SubscriptionUsage';
import { Popover } from './Popover';
import { featureNav, featureNavigation } from '../features';
import { PhoneNavigation } from './PhoneNavigation';
import './MainNav.css';

// activePrefix: sub-pages living outside the link's own path (singular routes, sibling tabs).
const NAV_ITEMS: { to: string; label: string; icon: string; activePrefix?: string }[] = [
  { to: '/', label: 'Home', icon: 'bi-house', activePrefix: '/session/' },
  { to: '/sessions', label: 'Sessions', icon: 'bi-collection' },
  { to: '/projects', label: 'Projects', icon: 'bi-folder', activePrefix: '/project/' },
  ...featureNav,
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
  const notify = useNotifySnapshot();
  const unreadSessions = attentionSessionEntries(notify?.entries ?? []).length;
  const homeLabel = unreadSessions ? `Home, ${unreadSessions} unread sessions` : 'Home';
  const usageTrigger = useRef<HTMLButtonElement>(null);
  const [usageOpen, setUsageOpen] = useState(false);
  const toggleLabel = mobileOpen
    ? 'Close navigation'
    : collapsed ? 'Expand navigation' : 'Collapse navigation';

  const navLink = (item: (typeof NAV_ITEMS)[number], className?: string) => (
    <NavLink
      key={item.to}
      to={item.to}
      aria-label={item.to === '/' ? homeLabel : item.label}
      title={collapsed ? (item.to === '/' ? homeLabel : item.label) : undefined}
      className={({ isActive }) => [
        isActive || (item.activePrefix && location.pathname.startsWith(item.activePrefix)) ? 'active' : '',
        className ?? '',
      ].filter(Boolean).join(' ') || undefined}
      onClick={onMobileClose}
    >
      <i className={`bi ${item.icon}`} aria-hidden="true" />
      <span>{item.label}</span>
      {item.to === '/' && unreadSessions > 0 && <b className="nav-unread-badge" aria-hidden="true">{unreadSessions > 99 ? '99+' : unreadSessions}</b>}
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
          {featureNavigation.map((Navigation, index) => <Navigation key={index} onNavigate={onMobileClose} />)}
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
          <div className="main-nav-usage-root">
            <button
              ref={usageTrigger}
              type="button"
              className={usageOpen || ['/provider-usage', '/subscription-usage'].includes(location.pathname) ? 'main-nav-usage active' : 'main-nav-usage'}
              aria-label="Provider usage"
              aria-expanded={usageOpen}
              aria-haspopup="dialog"
              aria-controls="subscription-usage-popover"
              title={collapsed ? 'Provider usage' : undefined}
              onClick={() => setUsageOpen((open) => !open)}
            >
              <i className="bi bi-speedometer2" aria-hidden="true" />
              <span>Provider usage</span>
            </button>
            <Popover open={usageOpen} onClose={() => setUsageOpen(false)} triggerRef={usageTrigger}
              id="subscription-usage-popover" label="Provider usage"
              className={`subscription-usage-popover${collapsed ? ' subscription-usage-popover--collapsed' : ''}`}>
              <SubscriptionUsageContent compact />
            </Popover>
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
      <PhoneNavigation unreadTotal={inbox.data?.unreadTotal ?? 0} unreadSessions={unreadSessions} />
    </>
  );
}
