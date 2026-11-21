import { useMemo, useState, type CSSProperties } from 'react';
import { useQuery } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { api, type Session } from '../lib/api';
import { prWorkflowApi } from '../lib/prWorkflowApi';
import { lineageKey, sessionLineage } from '../lib/sessionLineage';
import { projectRootForDirectory } from '../lib/worktrees';
import { Button } from './Control';
import { EmptyState } from './EmptyState';
import { IconButton } from './IconButton';
import { LoadingState } from './LoadingState';
import { ProjectLabel } from './ProjectLabel';
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
      <ul className="oc-lineage-tree">
        <LineageNode session={family.root} family={family} currentKey={currentKey}
          currentProject={projectRootForDirectory(session.directory || '')} depth={0} />
      </ul>
    </section>
  );
}

function LineageNode({ session, family, currentKey, currentProject, depth }: {
  session: Session;
  family: ReturnType<typeof sessionLineage>;
  currentKey: string;
  currentProject: string;
  depth: number;
}) {
  const key = lineageKey(session);
  const current = key === currentKey;
  const children = family.children.get(key);
  const [expanded, setExpanded] = useState<boolean>();
  const open = expanded ?? family.path.has(key);
  const title = session.title || 'Untitled session';
  const project = projectRootForDirectory(session.directory || '');
  const prQuery = useQuery({
    queryKey: ['session-lineage-prs', session.platform, session.id],
    queryFn: ({ signal }) => prWorkflowApi.background(session.id, session.platform, signal),
    select: result => result.pullRequests,
    staleTime: 60_000,
    retry: false,
  });
  const pullRequests = prQuery.data ?? [];
  const otherProject = !!project && project !== currentProject;

  return (
    <li>
      <div className="oc-lineage-row" data-current={current || undefined} style={{ '--depth': Math.min(depth, 6) } as CSSProperties}>
        {children ? <IconButton variant="ghost" size="compact"
          icon={open ? 'bi-chevron-down' : 'bi-chevron-right'}
          label={`${open ? 'Collapse' : 'Expand'} ${title}`}
          aria-expanded={open}
          onClick={() => setExpanded(!open)}
        /> : <span className="oc-lineage-leaf" aria-hidden="true" />}
        <StatusBadge status={session.status} pending={session.pendingPermission || session.pendingQuestion} compact />
        <span className="oc-lineage-body">
          <Link className="oc-lineage-session" to={`/session/${encodeURIComponent(session.id)}`} aria-current={current ? 'page' : undefined} title={title}>
            {title}
          </Link>
          {(otherProject || pullRequests.length > 0 || prQuery.isError) && (
            <span className="oc-lineage-details">
              {otherProject && <ProjectLabel className="oc-lineage-project" path={project} />}
              {pullRequests.map(link => (
                <a key={`${link.remote.host}/${link.remote.repo}/${link.number}`}
                  className="oc-lineage-pr" data-status={link.pr.status}
                  href={link.pr.url} target="_blank" rel="noreferrer noopener"
                  title={`${link.remote.repo}#${link.number} ${link.pr.title} (${link.pr.status})`}>
                  <i className="bi bi-git" aria-hidden="true" />#{link.number}
                </a>
              ))}
              {prQuery.isError && <IconButton variant="ghost" size="compact" className="oc-lineage-pr-retry"
                icon="bi-exclamation-circle" label={`Could not load PRs for ${title}. Retry`}
                disabled={prQuery.isFetching} onClick={() => void prQuery.refetch()} />}
            </span>
          )}
        </span>
        {session.archived && <span className="oc-lineage-meta">Archived</span>}
      </div>
      {!!children && open && (
        <ul className="oc-lineage-branch">
          {children.map(child => <LineageNode key={lineageKey(child)} session={child} family={family} currentKey={currentKey} currentProject={currentProject} depth={depth + 1} />)}
        </ul>
      )}
    </li>
  );
}
