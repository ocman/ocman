import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, type Session } from '../lib/api';
import { lineageKey, sessionLineage } from '../lib/sessionLineage';
import { Button } from './Control';
import { EmptyState } from './EmptyState';
import { IconButton } from './IconButton';
import { LoadingState } from './LoadingState';
import { StatusBadge } from './StatusBadge';
import './SessionLineagePane.css';

const noSessions: readonly Session[] = [];

export function SessionLineagePane({ session, sessionTree = noSessions }: { session?: Session; sessionTree?: readonly Session[] }) {
  const catalog = useQuery({
    queryKey: ['session-lineage-catalog'],
    queryFn: async ({ signal }) => (await api.sessions({ view: 'sidebar' }, signal)) ?? [],
    enabled: !!session,
    retry: false,
  });
  const family = useMemo(() => session && sessionLineage(session, sessionTree, catalog.data ?? noSessions), [session, sessionTree, catalog.data]);
  if (!session || !family) return <EmptyState>Lineage is available after the session starts.</EmptyState>;

  const currentKey = lineageKey(session);
  const hasRelatives = family.path.size > 1 || family.children.has(currentKey);
  return (
    <section className="oc-lineage" aria-label="Session lineage">
      {catalog.isError && (
        <EmptyState role="alert">
          Could not load the session catalog. Some relatives may be missing.
          <Button variant="ghost" size="small" onClick={() => void catalog.refetch()}>Retry</Button>
        </EmptyState>
      )}
      {catalog.isPending && <LoadingState>Looking up related sessions…</LoadingState>}
      {!catalog.isPending && family.unavailableParent && <p className="oc-lineage-note">Parent session unavailable.</p>}
      {!catalog.isPending && !catalog.isError && !family.unavailableParent && !hasRelatives
        && <p className="oc-lineage-note">No linked sessions found.</p>}
      <ul className="oc-lineage-tree">
        <LineageNode session={family.root} family={family} currentKey={currentKey} depth={0} />
      </ul>
    </section>
  );
}

function LineageNode({ session, family, currentKey, depth }: {
  session: Session;
  family: ReturnType<typeof sessionLineage>;
  currentKey: string;
  depth: number;
}) {
  const key = lineageKey(session);
  const current = key === currentKey;
  const children = family.children.get(key);
  const [expanded, setExpanded] = useState<boolean>();
  const open = expanded ?? family.path.has(key);
  const title = session.title || 'Untitled session';

  return (
    <li className="oc-lineage-node">
      <div className="oc-lineage-row" data-current={current || undefined}>
        {children ? <IconButton variant="ghost" size="compact"
          icon={open ? 'bi-chevron-down' : 'bi-chevron-right'}
          label={`${open ? 'Collapse' : 'Expand'} ${title}`}
          aria-expanded={open}
          onClick={() => setExpanded(!open)}
        /> : <span className="oc-lineage-leaf" aria-hidden="true" />}
        <Link className="oc-lineage-session" to={`/session/${encodeURIComponent(session.id)}`} aria-current={current ? 'page' : undefined} title={title}>
          <span className="oc-lineage-title">{title}</span>
          <span className="oc-lineage-meta">
            <StatusBadge status={session.status} pending={session.pendingPermission || session.pendingQuestion} />
            {session.archived && <span>Archived</span>}
          </span>
        </Link>
        {current && <span className="oc-lineage-current">Current</span>}
      </div>
      {!!children && open && (
        <ul className={depth < 6 ? 'oc-lineage-branch' : 'oc-lineage-branch oc-lineage-branch-flat'}>
          {children.map(child => <LineageNode key={lineageKey(child)} session={child} family={family} currentKey={currentKey} depth={depth + 1} />)}
        </ul>
      )}
    </li>
  );
}
