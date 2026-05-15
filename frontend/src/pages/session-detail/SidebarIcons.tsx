/** Inline SVG icons used in the session sidebar header. */

export function ArchiveIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2 3.5h12v2H2zm1 3h10v6H3zm3 2.5h4" fill="none" stroke="currentColor" strokeWidth="1.3" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function ArchiveFilterIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <path d="M2.5 3h11l-4.25 4.9v3.35l-2.5 1.25V7.9z" fill="none" stroke="currentColor" strokeWidth="1.2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

/**
 * Icon used for the "projects" sidebar-view toggle. Stack of horizontal bars
 * evokes a grouped-list.
 */
export function ProjectsViewIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2" y="3" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="4" y="7" width="10" height="2.2" rx="0.6" fill="currentColor" opacity="0.75" />
      <rect x="4" y="11" width="10" height="2.2" rx="0.6" fill="currentColor" opacity="0.75" />
    </svg>
  );
}

/**
 * Icon used when the sidebar is *in* projects view — shows a flat list,
 * hinting that clicking will return to the flat "recent" view.
 */
export function RecentViewIcon() {
  return (
    <svg width="12" height="12" viewBox="0 0 16 16" aria-hidden="true">
      <rect x="2" y="3" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="2" y="7" width="12" height="2.2" rx="0.6" fill="currentColor" />
      <rect x="2" y="11" width="12" height="2.2" rx="0.6" fill="currentColor" />
    </svg>
  );
}
