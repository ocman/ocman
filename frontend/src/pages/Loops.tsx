import { useEffect, useMemo, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useLoopsStore } from '../lib/loopsStore';
import { useAgentLoops } from '../lib/useCapabilities';
import { usePageTitle } from '../lib/headerContext';
import { fuzzyMatch, shortPath } from '../lib/format';
import { matchesScope } from '../lib/projectTree';
import { useProjects } from '../lib/queries';
import { LoopTableRow } from '../components/LoopTableRow';
import { LoopCreateModal } from '../components/LoopCreateModal';
import { DashboardToolbar } from './Dashboard';
import './Dashboard.css';
import './Loops.css';

/**
 * Loops view. Renders the project-scoped list at /project/:dir/loops and
 * the global cross-project list at /loops. Capability-gated on agentLoops.
 */
export function Loops() {
  const { dir } = useParams();
  const projectDir = dir ? decodeURIComponent(dir) : '';
  usePageTitle(projectDir ? `${shortPath(projectDir)} · Loops` : 'Loops');

  const enabled = useAgentLoops();
  const loops = useLoopsStore((s) => s.loops);
  const loading = useLoopsStore((s) => s.loading);
  const error = useLoopsStore((s) => s.error);
  const load = useLoopsStore((s) => s.load);
  const create = useLoopsStore((s) => s.create);

  // Global /loops view gets the shared dashboard toolbar (scope picker +
  // fuzzy search + New loop). The project-scoped view is already narrowed
  // by its route, so it keeps just the back link.
  const projectsQ = useProjects({ enabled: enabled && !projectDir });
  const projects = useMemo(() => projectsQ.data ?? [], [projectsQ.data]);
  const [dirScope, setDirScope] = useState('');
  const [search, setSearch] = useState('');
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (!enabled) return;
    void load(projectDir ? { dir: projectDir } : {});
  }, [enabled, projectDir, load]);

  const q = search.trim();
  const visibleLoops = loops
    .filter((l) => projectDir || matchesScope(l.directory, dirScope))
    .filter((l) => !q || fuzzyMatch(q, `${l.title} ${l.projectName} ${l.directory} ${l.currentTask}`));

  // Directories offered in the create modal's project selector.
  const projectDirs = useMemo(
    () => Array.from(new Set(projects.map((p) => p.directory))).sort(),
    [projects],
  );

  // The global /loops view renders inside DashboardLayout, which already
  // provides the app header + nav tabs — so no back link there. The
  // project-scoped /project/:dir/loops route is standalone, so it gets a
  // "Back to project" link.
  const backLink = projectDir ? (
    <Link className="oc-time-range-btn" to={`/project/${encodeURIComponent(projectDir)}`}>
      Back to project
    </Link>
  ) : null;

  if (!enabled) {
    return (
      <div className="metrics-page oc-loops-page" data-testid="loops-page">
        <p className="oc-loops-empty">Agent loops are unavailable on this host.</p>
      </div>
    );
  }

  return (
    <div className="metrics-page oc-loops-page" data-testid="loops-page">
      {projectDir ? (
        <div className="oc-loops-header">
          <div>
            <div className="mono oc-loops-project">{shortPath(projectDir)}</div>
          </div>
          <div className="oc-loops-header-actions">
            {backLink}
            <button className="oc-time-range-btn" type="button" onClick={() => void load({ dir: projectDir })}>
              Refresh
            </button>
          </div>
        </div>
      ) : (
        <DashboardToolbar
          projects={projects}
          dirScope={dirScope}
          setDirScope={setDirScope}
          search={search}
          setSearch={setSearch}
          searchLabel="Search loops"
          actionIcon="bi-plus-lg"
          actionLabel="New loop"
          actionTitle="Create a new agent loop anchored to a project"
          onAction={() => setCreating(true)}
        />
      )}
      {loading && <p data-testid="loops-loading">Loading…</p>}
      {error && <p className="oc-loops-error">{error}</p>}
      {!loading && visibleLoops.length === 0 && (
        <p className="oc-loops-empty" data-testid="loops-empty">
          {loops.length === 0
            ? 'No loops yet. Create one with the New loop button, from a session via the /loop command, or the MCP create_loop tool.'
            : 'No loops match the current filter.'}
        </p>
      )}
      {visibleLoops.length > 0 && (
        <table className="oc-loops-table">
          <thead>
            <tr>
              <th>Loop</th>
              <th>State</th>
              <th>Project</th>
              <th>Activity</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {visibleLoops.map((loop) => (
              <LoopTableRow key={loop.id} loop={loop} />
            ))}
          </tbody>
        </table>
      )}
      {creating && (
        <LoopCreateModal
          projectOptions={projectDirs}
          onCreate={async (req) => {
            await create(req);
            void load({});
          }}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  );
}
