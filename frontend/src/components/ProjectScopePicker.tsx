import { useMemo } from 'react';
import { buildScopeTree, flattenForOptions } from '../lib/projectTree';
import { shortPath } from '../lib/format';

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
 * The control is intentionally a flat <select> with indented <option>s
 * rather than a custom popover. Reasons:
 *
 *   - Native accessibility (keyboard, focus, screen reader) for free.
 *   - Visually consistent with the existing `metrics-filter` controls.
 *   - Trivially testable without @testing-library/react.
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
   * Optional override for the field label. Defaults to 'Project scope';
   * pages can use a shorter label when space is tight.
   */
  label?: string;
}

// Indent character: an em-space, repeated. Keeps the dropdown readable
// without relying on CSS that <option> elements don't render.
const INDENT = '\u2003';

export function ProjectScopePicker({ projects, value, onChange, label = 'Project scope' }: ProjectScopePickerProps) {
  // Memoise so we don't rebuild the trie on every parent re-render. The
  // input list reference changes whenever `projects` is reloaded, which
  // is rare relative to render frequency.
  const options = useMemo(() => flattenForOptions(buildScopeTree(projects)), [projects]);

  const disabled = options.length === 0;

  return (
    <label className="metrics-filter">
      <span>{label}</span>
      <select
        value={value}
        disabled={disabled}
        aria-label={label}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">All projects</option>
        {options.map((opt) => {
          // Use shortPath() for the visible label so long absolute paths
          // ('/Users/foo/...') don't dominate the dropdown. The full path
          // remains in the option's title attribute and value, so users
          // can hover to disambiguate and the URL ?dir= still carries
          // the absolute path.
          const display = INDENT.repeat(opt.depth) + shortPath(opt.path);
          const suffix =
            opt.projectCount > 1 ? ` (${opt.projectCount} projects)` : '';
          return (
            <option key={opt.path} value={opt.path} title={opt.path}>
              {display}
              {suffix}
            </option>
          );
        })}
      </select>
    </label>
  );
}
