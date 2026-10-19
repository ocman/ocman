import { Link, NavLink, useLocation } from 'react-router-dom';
import { featureNav, featureNavigation } from '../features';
import { useIsPhone } from '../lib/useIsPhone';
import { usePhoneOverlay } from '../lib/usePhoneOverlay';
import { Button } from './Control';
import { Modal } from './Modal';
import { ModalHeader } from './ModalHeader';
import './PhoneNavigation.css';
import './PhoneControls.css';

const tabs = [
  { to: '/sessions', label: 'Sessions', icon: 'bi-chat-left-text' },
  { to: '/projects', label: 'Projects', icon: 'bi-folder' },
  { to: '/inbox', label: 'Inbox', icon: 'bi-inbox' },
];

const moreItems = [
  { to: '/', label: 'Home', icon: 'bi-house' },
  ...featureNav,
  { to: '/analytics', label: 'Analytics', icon: 'bi-bar-chart' },
  { to: '/provider-usage', label: 'Provider usage', icon: 'bi-speedometer2' },
  { to: '/settings', label: 'Settings', icon: 'bi-gear' },
];

/** Conversations stay full-height, but their session list exposes global navigation. */
export function PhoneNavigation({ unreadTotal }: { unreadTotal: number }) {
  const phone = useIsPhone();
  const location = useLocation();
  const [panel] = usePhoneOverlay<'sidebar' | 'details'>('conversation-panel');
  const [navigation] = usePhoneOverlay<'more'>('navigation');
  if (!phone || (location.pathname.startsWith('/session/') && panel !== 'sidebar' && navigation !== 'more')) return null;
  return <PhoneTabs key={location.pathname + location.search} unreadTotal={unreadTotal} />;
}

function PhoneTabs({ unreadTotal }: { unreadTotal: number }) {
  const { pathname } = useLocation();
  const [overlay, setOverlay] = usePhoneOverlay<'more'>('navigation');
  const moreOpen = overlay === 'more';
  const inProjects = pathname.startsWith('/project/');
  const inSessions = pathname.startsWith('/session/');
  const inPrimary = tabs.some(({ to }) => pathname === to) || inProjects || inSessions;

  return <>
    <nav className="phone-tab-bar" aria-label="Phone navigation">
      {tabs.map(({ to, label, icon }) => {
        const active = pathname === to || (to === '/projects' && inProjects) || (to === '/sessions' && inSessions);
        return <Link key={to} to={to} className={active ? 'active' : ''}
          aria-current={active ? 'page' : undefined}
          aria-label={to === '/inbox' && unreadTotal ? `Inbox, ${unreadTotal} unread messages` : label}>
          <i className={`bi ${icon}`} aria-hidden="true" />
          <span>{label}</span>
          {to === '/inbox' && unreadTotal > 0 && <b className="phone-tab-badge" aria-hidden="true">{unreadTotal > 99 ? '99+' : unreadTotal}</b>}
        </Link>;
      })}
      <Button variant="ghost" className={moreOpen || !inPrimary ? 'active' : ''}
        aria-haspopup="dialog" aria-expanded={moreOpen} onClick={() => setOverlay('more')}>
        <i className="bi bi-three-dots" aria-hidden="true" /><span>More</span>
      </Button>
    </nav>
    {moreOpen && <div className="phone-more-sheet">
      <Modal label="More destinations" onClose={() => setOverlay(null)}>
        <ModalHeader title="More" onClose={() => setOverlay(null)} closeLabel="Close more destinations" />
        <nav aria-label="More destinations">
          {moreItems.map(({ to, label, icon }) => <NavLink key={to} to={to} replace end={to === '/'}>
            <i className={`bi ${icon}`} aria-hidden="true" /><span>{label}</span>
            <i className="bi bi-chevron-right" aria-hidden="true" />
          </NavLink>)}
          {featureNavigation.map((Navigation, index) => <Navigation key={index} onNavigate={() => setOverlay(null)} />)}
        </nav>
      </Modal>
    </div>}
  </>;
}
