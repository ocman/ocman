import { useRef, useState } from 'react';
import { useClickOutside } from '../../lib/useClickOutside';
import { ArchiveFilterIcon } from './SidebarIcons';

export interface SidebarHeaderProps {
  searchQuery: string;
  setSearchQuery: (query: string) => void;
  showArchivedRecent: boolean;
  setShowArchivedRecent: (updater: (current: boolean) => boolean) => void;
  showChildren: boolean;
  setShowChildren: (show: boolean) => void;
}

/** Sidebar heading: search toggle/input and the filters popover. */
export function SidebarHeader({
  searchQuery,
  setSearchQuery,
  showArchivedRecent,
  setShowArchivedRecent,
  showChildren,
  setShowChildren,
}: SidebarHeaderProps) {
  const filterRef = useRef<HTMLDivElement>(null);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [searching, setSearching] = useState(false);
  useClickOutside(filterRef, filtersOpen, () => setFiltersOpen(false));
  return (
    <div className="session-sidebar-header">
      {searching ? (
        <input
          type="search"
          className="session-sidebar-search"
          aria-label="Search sessions"
          placeholder="Search sessions"
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== 'Escape') return;
            setSearchQuery('');
            setSearching(false);
          }}
          autoFocus
        />
      ) : (
        <button
          type="button"
          className="session-sidebar-heading"
          data-testid="sidebar-heading"
          aria-label="Search sessions"
          onClick={() => setSearching(true)}
        >
          <i className="bi bi-search session-sidebar-search-icon" aria-hidden="true" />
          <span className="session-sidebar-heading-desktop">Sessions</span>
          <span className="session-sidebar-heading-mobile">Search sessions</span>
        </button>
      )}
      <div className="session-sidebar-header-actions" ref={filterRef}>
        <button
          type="button"
          className={`session-sidebar-new${showArchivedRecent || !showChildren ? ' active' : ''}`}
          onClick={() => setFiltersOpen((open) => !open)}
          title="Filter sessions"
          aria-label="Filter sessions"
          aria-expanded={filtersOpen}
          aria-controls="session-sidebar-filters"
        ><ArchiveFilterIcon /></button>
        {filtersOpen && (
          <div id="session-sidebar-filters" className="session-sidebar-filters" role="group" aria-label="Session filters">
            <label>
              <input
                type="checkbox"
                checked={showArchivedRecent}
                onChange={(event) => {
                  const checked = event.target.checked;
                  setShowArchivedRecent(() => checked);
                }}
              />
              <span>Show archived</span>
            </label>
            <label>
              <input
                type="checkbox"
                checked={showChildren}
                onChange={(event) => setShowChildren(event.target.checked)}
              />
              <span>Show children</span>
            </label>
          </div>
        )}
      </div>
    </div>
  );
}
