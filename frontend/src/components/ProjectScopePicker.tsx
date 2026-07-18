import { useMemo } from 'react';
import { buildScopeTree, flattenForOptions } from '../lib/projectTree';
import { shortPath } from '../lib/format';
import { SearchSelect } from './SearchSelect';

/**
 * ProjectScopePicker — a single dropdown that lets the user scope the
 * Stats / Usage / Projects views to a project subtree.
 *
 * The dropdown is populated from the current `/api/projects` payload by
 * walking the prefix trie produced by buildScopeTree(). Both leaf project
 * directories and intermediate parent directories are selectable, so the
 * user can scope to e.g. one repo, the whole org, or the whole host —
 * exactly the use case described in the feature request.
 *
 * See spec/stats-project-filter/architecture.md (AD-1, AD-3, AD-8).
 */
export interface ProjectScopePickerProps {
  /**
   * Current project list, e.g. from useDashboard().projects. The picker
   * only reads `directory`; we type the prop on a structural minimum so
   * tests (and any future callers with different project shapes) can
   * pass anything that exposes a directory field without needing to
   * fabricate the full Project shape.
   */
  projects: Array<{ directory: string }>;
  /** The active scope (URL ?dir=…). Empty string means "all projects". */
  value: string;
  /** Called with the new scope path, or '' when "All projects" is chosen. */
  onChange: (dir: string) => void;
  /**
   * Accessible label (aria-label). Defaults to 'Project scope'.
   */
  label?: string;
  /**
   * When true, render `label` as a visible caption above the select so
   * the control lines up with the other captioned `.metrics-filter`
   * controls (Stats / Usage). Off by default: most callers (Dashboard,
   * standalone pages) show the picker bare.
   */
  showLabel?: boolean;
}

// Indent character: an em-space, repeated. Keeps the dropdown readable
// without relying on CSS that <option> elements don't render.
const INDENT = '\u2003';

export function ProjectScopePicker({
  projects,
  value,
  onChange,
  label = 'Project scope',
  showLabel = false,
}: ProjectScopePickerProps) {
  // Memoise so we don't rebuild the trie on every parent re-render. The
  // input list reference changes whenever `projects` is reloaded, which
  // is rare relative to render frequency.
  const options = useMemo(() => flattenForOptions(buildScopeTree(projects)), [projects]);

  const disabled = options.length === 0;

  return (
    <label className="metrics-filter">
      {showLabel && <span>{label}</span>}
      <SearchSelect
        value={value}
        disabled={disabled}
        ariaLabel={label}
        placeholder="All projects"
        searchLabel="Search projects"
        onChange={onChange}
        options={[
          { value: '', label: 'All projects' },
          ...options.map((option) => ({
            value: option.path,
            label: `${INDENT.repeat(option.depth)}${shortPath(option.path)}${option.projectCount > 1 ? ` (${option.projectCount} projects)` : ''}`,
          })),
        ]}
      />
    </label>
  );
}
