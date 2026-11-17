import { useState } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { formatExactDate, formatProjectActivity, fuzzyMatch, relativeTime } from '../../lib/format';
import { ACTIVITY_DEFINITIONS, formatCost, projectActivityDetails } from '../../lib/listActivity';
import { sortBy, useUrlSort } from '../../lib/useUrlSort';
import { SortableHeader } from '../../components/SortableHeader';
import { ProjectLabel } from '../../components/ProjectLabel';
import { usePageTitle } from '../../lib/headerContext';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';
import { DataTable } from '../../components/DataTable';
import { LoadingState } from '../../components/LoadingState';
import { InlineAlert } from '../../components/InlineAlert';
import { Button } from '../../components/Control';
import type { Project } from '../../lib/api';
import { MergeProjectModal } from './MergeProjectModal';
import '../../components/PhoneTables.css';

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

const PROJECT_SORT_KEYS = ['sessions', 'messages', 'cost', 'active'] as const;
type ProjectSortKey = typeof PROJECT_SORT_KEYS[number];
const projectSortValue = (p: Project, key: ProjectSortKey) =>
  ({ sessions: p.sessionCount, messages: p.messageCount, cost: p.totalCost ?? 0, active: p.lastUsed })[key];

export function ProjectsTab() {
  usePageTitle('Projects');
  const {
    projects, projectsLoading, projectsFetching, projectsError, refetchProjects, dirScope, setDirScope,
  } = useDashboardCtx();
  const navigate = useNavigate();
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);
  const [search, setSearch] = useState('');
  const [mergeSource, setMergeSource] = useState<Project | null>(null);
  const sort = useUrlSort(PROJECT_SORT_KEYS);

  // The picker is sourced from the full project list (so the user can
  // navigate up/down the tree); the table itself is filtered to the
  // active scope. matchesScope mirrors the SQL predicate used by the
  // backend (see spec/stats-project-filter/architecture.md, AD-7).
  const q = search.trim();
  const visibleProjects = projects
    // Remote projects carry no session aggregates (inventory-only), so
    // don't drop them on the sessionCount>0 gate that hides empty locals.
    .filter((p) => p.remoteId || p.sessionCount > 0)
    .filter((p) => matchesScope(p.directory, dirScope))
    .filter((p) => !q || fuzzyMatch(q, p.directory));

  if (projectsLoading && projects.length === 0) {
    return <LoadingState>Loading projects...</LoadingState>;
  }

  // A failed query must never fall through to the table below: with no
  // data, the empty branch would render the first-run onboarding state
  // and tell a user with dozens of projects that they have none.
  if (projectsError && projects.length === 0) {
    return (
      <InlineAlert onRetry={() => refetchProjects()} retrying={projectsFetching}>
        {projectsError}
      </InlineAlert>
    );
  }

  return (
    <div>
      {projectsError && (
        <InlineAlert onRetry={() => refetchProjects()} retrying={projectsFetching}>
          {projectsError}
        </InlineAlert>
      )}
      <DashboardToolbar
        projects={projects}
        dirScope={dirScope}
        setDirScope={setDirScope}
        search={search}
        setSearch={setSearch}
        searchLabel="Search projects"
        actionIcon="bi-folder-plus"
        actionLabel="New project"
        actionTitle="Start a session in a project directory"
        onAction={openProjectPalette}
      />
    <DataTable className="phone-project-table">
      <thead>
        <tr>
          <th>Project</th>
          <SortableHeader column="sessions" sort={sort} numeric description="Sessions: top-level sessions, excluding helpers">Sessions</SortableHeader>
          <SortableHeader column="messages" sort={sort} numeric description={ACTIVITY_DEFINITIONS.messages}>Messages</SortableHeader>
          <SortableHeader column="cost" sort={sort} numeric description={ACTIVITY_DEFINITIONS.cost}>Cost</SortableHeader>
          <SortableHeader column="active" sort={sort}>Last active</SortableHeader>
          <th aria-label="Actions" />
        </tr>
      </thead>
      <tbody>
        {visibleProjects.length === 0 ? (
          <tr>
            <td colSpan={6} style={{ padding: 24 }}>
              {projects.length === 0 ? (
                <GettingStartedEmpty />
              ) : (
                <div style={{ textAlign: 'center', color: 'var(--text-dim)' }}>
                  No projects found
                </div>
              )}
            </td>
          </tr>
        ) : sortBy(visibleProjects, sort, projectSortValue).map((p) => (
          <tr
            key={`${p.remoteId ?? 'local'}:${p.directory}`}
            onClick={() =>
              navigate(`/project/${encodeURIComponent(p.directory)}${p.remoteId ? `?remoteId=${encodeURIComponent(p.remoteId)}` : ''}`)
            }
          >
            <td>
              <Link className="phone-project-link" to={`/project/${encodeURIComponent(p.directory)}${p.remoteId ? `?remoteId=${encodeURIComponent(p.remoteId)}` : ''}`} onClick={event => event.stopPropagation()}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <ProjectLabel path={p.directory} style={{ color: 'var(--accent)', fontWeight: 500 }} />
                {p.remoteName ? (
                  <span className="oc-cmd-badge" title={`On remote ${p.remoteName}`}>{p.remoteName}</span>
                ) : null}
              </div>
              <span className="phone-project-meta">{formatProjectActivity(p.sessionCount, p.lastUsed)}</span>
              </Link>
            </td>
            <td className="list-numeric">{p.sessionCount}</td>
            <td className="list-numeric" title={projectActivityDetails(p)}>{p.messageCount.toLocaleString('en-US')}</td>
            <td className="mono list-numeric">{formatCost(p.totalCost ?? 0)}</td>
            <td>{p.lastUsed > 0 ? <span title={formatExactDate(p.lastUsed)}>{relativeTime(p.lastUsed)}</span>
              : <span title="No sessions yet">—</span>}</td>
            <td className="project-action-cell"><Button type="button" size="small" variant="ghost" className="project-merge-btn"
              aria-label={`Merge ${p.directory}`} title="Merge this project's sessions into another project"
              onClick={event => { event.stopPropagation(); setMergeSource(p); }}><i className="bi bi-sign-merge-left" aria-hidden="true" /><span className="project-merge-label"> Merge…</span></Button></td>
          </tr>
        ))}
      </tbody>
    </DataTable>
    {mergeSource && <MergeProjectModal source={mergeSource} projects={projects}
      onClose={() => setMergeSource(null)} onMerged={() => { void refetchProjects(); }} />}
    </div>
  );
}
