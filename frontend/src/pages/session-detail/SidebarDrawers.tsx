import { useId, type ReactNode } from 'react';
import { Button } from '../../components/Control';
import type { Session } from '../../lib/api';
import type { SidebarDrawer, SidebarDrawerId } from './useSidebarDrawers';
import './SidebarDrawers.css';

interface Props {
  drawers: SidebarDrawer[];
  expanded: ReadonlySet<SidebarDrawerId>;
  onToggle: (id: SidebarDrawerId) => void;
  renderRow: (session: Session, inGroup: boolean, depth?: number, flat?: boolean) => ReactNode;
}

export function SidebarDrawers({ drawers, expanded, onToggle, renderRow }: Props) {
  const prefix = useId();
  if (!drawers.length) return null;
  return (
    <div className="session-sidebar-drawers" role="group" aria-label="Session drawers">
      {drawers.map((drawer) => {
        const open = expanded.has(drawer.id);
        const id = `${prefix}-${drawer.id}`;
        return (
          <section className="session-sidebar-drawer" key={drawer.id}>
            <Button variant="ghost" size="small" className="session-sidebar-drawer-toggle"
              aria-expanded={open} aria-controls={id} id={`${id}-toggle`}
              onClick={() => onToggle(drawer.id)}>
              <i className={`bi ${open ? 'bi-chevron-down' : 'bi-chevron-right'}`} aria-hidden="true" />
              <i className={`bi ${drawer.icon}`} aria-hidden="true" />
              <span>{drawer.label}</span>{' '}
              <span className="session-sidebar-drawer-count">{drawer.sessions.length}</span>
            </Button>
            <div id={id} role="region" aria-labelledby={`${id}-toggle`} hidden={!open}>
              {open && drawer.sessions.map((session) => renderRow(session, false, 0, true))}
            </div>
          </section>
        );
      })}
    </div>
  );
}
