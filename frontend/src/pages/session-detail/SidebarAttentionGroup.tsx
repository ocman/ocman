import { useNavigate, useSearchParams } from 'react-router-dom';
import type { NotifyEntry } from '../../lib/api';
import { cleanTitle } from '../../lib/format';
import { sessionAttentionKey } from '../../lib/sessionAttention';
import { StatusBadge } from '../../components/StatusBadge';
import { ShortPath } from '../../components/SessionTable';
import { SidebarRow } from './SidebarRow';

/** Render notification metadata directly, including sessions outside the recent window. */
export function SidebarAttentionGroup({ entries, activeId }: { entries: NotifyEntry[]; activeId?: string }) {
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const activePlatform = searchParams.get('platform');
  if (!entries.length) return null;
  return <section className="session-sidebar-group" aria-label="Needs attention">
    <div className="session-sidebar-group-header-row">
      <div className="session-sidebar-group-header">
        <span className="session-sidebar-group-label">Needs attention</span>
      </div>
    </div>
    {entries.map((entry) => <SidebarRow key={sessionAttentionKey(entry)} active={entry.id === activeId && (!activePlatform || entry.platform === activePlatform)} flat
      onClick={() => {
        const query = new URLSearchParams();
        if (entry.platform) query.set('platform', entry.platform);
        navigate(`/session/${encodeURIComponent(entry.id)}${query.size ? `?${query}` : ''}`);
      }}>
      <span className="session-sidebar-item-body">
        <span className="session-sidebar-project">
          <StatusBadge status={entry.status} compact seen={entry.seen} pending={entry.pendingPermission || entry.pendingQuestion} />
          <span className="session-sidebar-project-path"><ShortPath path={entry.directory ?? ''} /></span>
        </span>
        <span className="session-sidebar-title">{cleanTitle(entry.title ?? '') || 'Untitled'}</span>
      </span>
    </SidebarRow>)}
  </section>;
}
