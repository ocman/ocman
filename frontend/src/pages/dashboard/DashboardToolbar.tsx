import type { Project } from '../../lib/api';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';

// ---------------------------------------------------------------------------
// Shared dashboard toolbar: project scope picker + fuzzy search + a
// primary "create" action. Used by both the Sessions and Projects tabs.
// ---------------------------------------------------------------------------

export function DashboardToolbar({
  projects,
  dirScope,
  setDirScope,
  search,
  setSearch,
  searchLabel,
  actionIcon,
  actionLabel,
  actionTitle,
  onAction,
}: {
  projects: Project[];
  dirScope: string;
  setDirScope: (v: string) => void;
  search: string;
  setSearch: (v: string) => void;
  searchLabel: string;
  actionIcon: string;
  actionLabel: string;
  actionTitle: string;
  onAction: () => void;
}) {
  return (
    <div className="metrics-filters oc-projects-toolbar">
      <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} />
      <input
        type="search"
        className="oc-project-search"
        placeholder={`${searchLabel}\u2026`}
        aria-label={searchLabel}
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      <button
        type="button"
        className="vscode-btn oc-dashboard-primary-action"
        onClick={onAction}
        title={actionTitle}
      >
        <i className={`bi ${actionIcon}`} aria-hidden="true" />
        {actionLabel}
      </button>
    </div>
  );
}
