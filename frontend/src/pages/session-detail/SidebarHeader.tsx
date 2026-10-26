import { useRef, useState } from 'react';
import { useClickOutside } from '../../lib/useClickOutside';
import { ArchiveFilterIcon } from './SidebarIcons';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { SessionFilterControls } from '../../components/SessionFilterControls';
import type { SidebarProjectGroup } from './SessionSidebar';
import './SidebarHeader.css';

export interface SidebarHeaderProps {
  projects: SidebarProjectGroup[];
  projectFilter: string;
  setProjectFilter: (key: string) => void;
  searchQuery: string;
  setSearchQuery: (query: string) => void;
  showArchivedRecent: boolean;
  setShowArchivedRecent: (updater: (current: boolean) => boolean) => void;
  showChildren: boolean;
  showSnoozed: boolean;
  setShowSnoozed: (show: boolean) => void;
  setShowChildren: (show: boolean) => void;
  showFactory: boolean;
  setShowFactory: (show: boolean) => void;
  showRoutines: boolean;
  setShowRoutines: (show: boolean) => void;
  showPlugins: boolean;
  setShowPlugins: (show: boolean) => void;
  alwaysShowPrompts: boolean;
  setAlwaysShowPrompts: (show: boolean) => void;
  sidebarView: 'recent' | 'projects';
  setSidebarView: (view: 'recent' | 'projects') => void;
  onNewSession: () => void;
}

/** Sidebar search and controls. */
export function SidebarHeader({
  projects,
  projectFilter,
  setProjectFilter,
  searchQuery,
  setSearchQuery,
  showArchivedRecent,
  setShowArchivedRecent,
  showSnoozed,
  setShowSnoozed,
  showChildren,
  setShowChildren,
  showFactory,
  setShowFactory,
  showRoutines,
  setShowRoutines,
  showPlugins,
  setShowPlugins,
  alwaysShowPrompts,
  setAlwaysShowPrompts,
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
          className="session-sidebar-new"
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
            <ProjectScopePicker projects={projects} value={projectFilter} onChange={setProjectFilter} label="Project" scopePrefix="scope:" />
            <SessionFilterControls value={{ alwaysShowPrompts, showArchived: showArchivedRecent, showSnoozed, showChildren, showFactory, showRoutines, showPlugins }}
              onChange={(key, checked) => ({
                alwaysShowPrompts: setAlwaysShowPrompts,
                showArchived: (value: boolean) => setShowArchivedRecent(() => value),
                showSnoozed: setShowSnoozed, showChildren: setShowChildren,
                showFactory: setShowFactory, showRoutines: setShowRoutines, showPlugins: setShowPlugins,
              })[key](checked)} />
          </div>
        )}
      </div>
    </div>
  );
}
