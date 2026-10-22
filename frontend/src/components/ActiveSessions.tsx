import { useEffect, useId, useRef, useState, type CSSProperties } from 'react';
import type { Session } from '../lib/api';
import { useProjects, useSessions } from '../lib/queries';
import { projectIdentityIndex, type ProjectIdentity } from '../lib/projectIdentity';
import { cleanTitle } from '../lib/format';
import { Button, RouteButton } from './Control';
import { EmptyState } from './EmptyState';
import { Popover } from './Popover';
import { ProjectLabel } from './ProjectLabel';
import { TOOLTIP_DELAY_MS } from './Tooltip';
import styles from './ActiveSessions.module.css';

export function ActiveSessions({ count, enabled }: { count: number; enabled: boolean }) {
  const trigger = useRef<HTMLButtonElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const id = useId();
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const open = enabled && position !== null;
  const sessions = useSessions({ limit: 0 }, { enabled: open, poll: true });
  const projects = useProjects({ enabled: open });
  const identity = projectIdentityIndex(projects.data ?? []);
  const groups = new Map<string, { project: ProjectIdentity; sessions: Session[] }>();
  for (const session of sessions.data ?? []) {
    if (session.status !== 'busy') continue;
    const project = identity(session.directory, session.remoteId);
    const group = groups.get(project.key) ?? { project, sessions: [] };
    group.sessions.push(session);
    groups.set(project.key, group);
  }

  const cancelTimer = () => clearTimeout(timer.current);
  const close = () => { cancelTimer(); setPosition(null); };
  const show = () => {
    cancelTimer();
    const rect = trigger.current?.getBoundingClientRect();
    if (rect) setPosition({ left: Math.max(12, Math.min(rect.left, window.innerWidth - 352)), bottom: window.innerHeight - rect.top + 4 });
  };
  const enter = () => { cancelTimer(); if (!open) timer.current = setTimeout(show, TOOLTIP_DELAY_MS); };
  const leave = () => { cancelTimer(); timer.current = setTimeout(close, 150); };
  useEffect(() => () => clearTimeout(timer.current), [enabled]);

  return <>
    <Button ref={trigger} variant="ghost" size="compact" className="backend-stats-counter"
      title="Currently running sessions, including archived sessions and subagents"
      aria-label={`Total active sessions: ${count}`} aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
      disabled={!enabled} onMouseEnter={enter} onMouseLeave={leave} onFocus={() => { if (!open) show(); }}
      onKeyDown={event => {
        if (event.key === 'Tab' && !event.shiftKey && open) {
          event.preventDefault();
          document.getElementById(id)?.focus();
        }
      }}
      onBlur={event => { if (!document.getElementById(id)?.contains(event.relatedTarget)) close(); }}
      onClick={() => { if (!open) show(); }}>
      <span className="backend-stats-label">tas</span>: {count}
    </Button>
    <Popover open={open} onClose={close} triggerRef={trigger} id={id} label="Active sessions" style={position ?? undefined} autoFocus={false}>
      <div className={styles.content} onMouseEnter={cancelTimer} onMouseLeave={leave}
        onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget) && !trigger.current?.contains(event.relatedTarget)) close(); }}>
        <h2 className={styles.title}>Total active sessions</h2>
        {sessions.isError ? <EmptyState>Could not load active sessions. <Button variant="ghost" size="small" onClick={() => void sessions.refetch()}>Retry</Button></EmptyState>
          : sessions.isPending ? <EmptyState>Loading active sessions…</EmptyState>
          : groups.size === 0 ? <EmptyState>No active sessions</EmptyState>
          : [...groups.values()].map(({ project, sessions: rows }) => <section key={project.key} className={styles.project}>
            <h3 className={styles.projectTitle}><ProjectLabel path={project.directory} />{project.remoteName && <small> · {project.remoteName}</small>}</h3>
            {rows.map(session => <RouteButton key={`${session.platform}:${session.id}`} className={styles.session}
              to={`/session/${encodeURIComponent(session.id)}?platform=${encodeURIComponent(session.platform)}`}
              onClick={close}>{cleanTitle(session.title) || session.id}</RouteButton>)}
          </section>)}
      </div>
    </Popover>
  </>;
}
