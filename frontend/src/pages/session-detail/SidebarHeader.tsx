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
  showFactory: boolean;
  setShowFactory: (show: boolean) => void;
  sidebarView: 'recent' | 'projects';
  setSidebarView: (view: 'recent' | 'projects') => void;
  onNewSession: () => void;
}

/** Sidebar search and controls. */
export function SidebarHeader({
  searchQuery,
  setSearchQuery,
  showArchivedRecent,
  setShowArchivedRecent,
  showChildren,
  setShowChildren,
  showFactory,
  setShowFactory,
  sidebarView,
  setSidebarView,
  onNewSession,
}: SidebarHeaderProps) {
  const filterRef = useRef<HTMLDivElement>(null);
  const [filtersOpen, setFiltersOpen] = useState(false);
  useClickOutside(filterRef, filtersOpen, () => setFiltersOpen(false));
  return (
    <div className="session-sidebar-header">
      <label className="session-sidebar-search" data-testid="sidebar-search">
        <i className="bi bi-search session-sidebar-search-icon" aria-hidden="true" />
        <input
          type="search"
          className="session-sidebar-search-input"
          aria-label="Search sessions"
          placeholder="Search"
          value={searchQuery}
          onChange={(event) => setSearchQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key !== 'Escape') return;
            setSearchQuery('');
            event.currentTarget.blur();
          }}
        />
      </label>
      <div className="session-sidebar-header-actions" ref={filterRef}>
        <button
          type="button"
          className="session-sidebar-new"
          onClick={() => setSidebarView(sidebarView === 'recent' ? 'projects' : 'recent')}
          title={sidebarView === 'recent' ? 'Group sessions by project' : 'Show flat session list'}
          aria-label={sidebarView === 'recent' ? 'Group sessions by project' : 'Show flat session list'}
        >
          <i className={`bi ${sidebarView === 'recent' ? 'bi-folder2' : 'bi-list-ul'}`} aria-hidden="true" />
        </button>
        <button
          type="button"
          className={`session-sidebar-new${showArchivedRecent || !showChildren || showFactory ? ' active' : ''}`}
          onClick={() => setFiltersOpen((open) => !open)}
          title="Filter sessions"
          aria-label="Filter sessions"
          aria-expanded={filtersOpen}
          aria-controls="session-sidebar-filters"
        ><ArchiveFilterIcon /></button>
        <button
          type="button"
          className="session-sidebar-new"
          onClick={onNewSession}
          title="New session"
          aria-label="New session"
        >
          <i className="bi bi-plus-lg" aria-hidden="true" />
        </button>
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
            <label>
              <input
                type="checkbox"
                checked={showFactory}
                onChange={(event) => setShowFactory(event.target.checked)}
              />
              <span>Show factory</span>
            </label>
          </div>
        )}
      </div>
    </div>
  );
}
