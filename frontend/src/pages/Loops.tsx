import { useEffect } from 'react';
import { Link, useParams } from 'react-router-dom';
import { useLoopsStore } from '../lib/loopsStore';
import { useAgentLoops } from '../lib/useCapabilities';
import { usePageTitle } from '../lib/headerContext';
import { shortPath } from '../lib/format';
import { LoopTableRow } from '../components/LoopTableRow';
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

  useEffect(() => {
    if (!enabled) return;
    void load(projectDir ? { dir: projectDir } : {});
  }, [enabled, projectDir, load]);

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
      <div className="oc-loops-header">
        {/* Project-scoped view has no nav tab, so it shows the project
            path + a back link. The global /loops view is labelled by the
            dashboard nav tab, so no title here. */}
        <div>
          {projectDir && <div className="mono oc-loops-project">{shortPath(projectDir)}</div>}
        </div>
        <div className="oc-loops-header-actions">
          {backLink}
          <button className="oc-time-range-btn" type="button" onClick={() => void load(projectDir ? { dir: projectDir } : {})}>
            Refresh
          </button>
        </div>
      </div>
      {loading && <p data-testid="loops-loading">Loading…</p>}
      {error && <p className="oc-loops-error">{error}</p>}
      {!loading && loops.length === 0 && (
        <p className="oc-loops-empty" data-testid="loops-empty">
          No loops yet. Create one from a session via the <code>/loop</code> command or the
          MCP <code>create_loop</code> tool.
        </p>
      )}
      {loops.length > 0 && (
        <table className="oc-loops-table">
          <thead>
            <tr>
              <th>Title</th>
              <th>State</th>
              <th>Trigger</th>
              <th>Action</th>
              <th>Project</th>
              <th>Budget</th>
              <th>Last fired</th>
              <th>Next run</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {loops.map((loop) => (
              <LoopTableRow key={loop.id} loop={loop} />
            ))}
          </tbody>
        </table>
      )}
    </div>
  );
}
