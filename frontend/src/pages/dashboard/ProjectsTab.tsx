import { useState } from 'react';
import { useNavigate } from 'react-router-dom';
import { formatNumber, fuzzyMatch, relativeTime, shortPath } from '../../lib/format';
import { usePageTitle } from '../../lib/headerContext';
import { GettingStartedEmpty } from '../../components/GettingStartedEmpty';
import { matchesScope } from '../../lib/projectTree';
import { useUiStore } from '../../lib/uiStore';
import { useDashboard as useDashboardCtx } from './context';
import { DashboardToolbar } from './DashboardToolbar';

// ---------------------------------------------------------------------------
// Projects tab
// ---------------------------------------------------------------------------

export function ProjectsTab() {
  usePageTitle('Projects');
  const {
    projects, projectsLoading, projectsError, refetchProjects, dirScope, setDirScope,
  } = useDashboardCtx();
  const navigate = useNavigate();
  const openProjectPalette = useUiStore((s) => s.openProjectPalette);
  const [search, setSearch] = useState('');

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
    return (
      <div className="oc-list-loading">
        <div className="oc-spinner" />
        Loading projects...
      </div>
    );
  }

  // A failed query must never fall through to the table below: with no
  // data, the empty branch would render the first-run onboarding state
  // and tell a user with dozens of projects that they have none.
  if (projectsError && projects.length === 0) {
    return (
      <div className="oc-error-banner">
        {projectsError}
        <button type="button" onClick={() => refetchProjects()}>Retry</button>
      </div>
    );
  }

  return (
    <div className="metrics-page" style={{ padding: 0 }}>
      {projectsError && (
        <div className="oc-error-banner">
          {projectsError}
          <button type="button" onClick={() => refetchProjects()}>Retry</button>
        </div>
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
    <table>
      <thead>
        <tr>
          <th>Project</th>
          <th>Sessions</th>
          <th>Messages</th>
          <th>Tokens (in/out)</th>
          <th>Last Active</th>
        </tr>
      </thead>
      <tbody>
        {visibleProjects.length === 0 ? (
          <tr>
            <td colSpan={5} style={{ padding: 24 }}>
              {projects.length === 0 ? (
                <GettingStartedEmpty />
              ) : (
                <div style={{ textAlign: 'center', color: 'var(--text-dim)' }}>
                  No projects found
                </div>
              )}
            </td>
          </tr>
        ) : visibleProjects.map((p) => (
          <tr
            key={`${p.remoteId ?? 'local'}:${p.directory}`}
            onClick={() =>
              p.remoteId ? undefined : navigate(`/project/${encodeURIComponent(p.directory)}`)
            }
            style={p.remoteId ? { cursor: 'default' } : undefined}
          >
            <td>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span style={{ color: 'var(--accent)', fontWeight: 500 }}>{shortPath(p.directory)}</span>
                {p.remoteName ? (
                  <span className="oc-cmd-badge" title={`On remote ${p.remoteName}`}>{p.remoteName}</span>
                ) : null}
              </div>
              <div className="mono">{p.directory}</div>
            </td>
            <td>{p.sessionCount}</td>
            <td>{p.messageCount}</td>
            <td className="mono">{formatNumber(p.totalTokensIn)} / {formatNumber(p.totalTokensOut)}</td>
            <td>{relativeTime(p.lastUsed)}</td>
          </tr>
        ))}
      </tbody>
    </table>
    </div>
  );
}
