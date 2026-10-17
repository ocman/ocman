import { useId, useRef, useState, type ReactNode } from 'react';
import type { Project } from '../../lib/api';
import { SearchField } from '../../components/Control';
import { IconButton } from '../../components/IconButton';
import { Popover } from '../../components/Popover';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import styles from './PhoneSessionsToolbar.module.css';

interface Props {
  projects: Project[];
  dirScope: string;
  setDirScope: (scope: string) => void;
  search: string;
  setSearch: (query: string) => void;
  onNewSession: () => void;
  filtersActive: boolean;
  children: ReactNode;
}

/** One phone-sized row; expanded options float above, never push down the list. */
export function PhoneSessionsToolbar({ projects, dirScope, setDirScope, search, setSearch, onNewSession, filtersActive, children }: Props) {
  const [panel, setPanel] = useState<'project' | 'filters' | null>(null);
  const [top, setTop] = useState(0);
  const projectRef = useRef<HTMLButtonElement>(null);
  const filterRef = useRef<HTMLButtonElement>(null);
  const id = useId();
  const triggerRef = panel === 'project' ? projectRef : filterRef;
  function toggle(next: 'project' | 'filters', button: HTMLButtonElement) {
    setTop(button.getBoundingClientRect().bottom + 8);
    setPanel(current => current === next ? null : next);
  }

  return <div className={styles.toolbar} role="group" aria-label="Session list controls">
    <SearchField className={styles.search} aria-label="Search sessions" placeholder="Search"
      value={search} onChange={event => setSearch(event.target.value)} />
    <IconButton ref={projectRef} className={styles.action} label="Choose project" icon="bi-folder2"
      variant={dirScope ? 'accent' : 'ghost'} aria-haspopup="dialog" aria-expanded={panel === 'project'} aria-controls={id}
      onClick={event => toggle('project', event.currentTarget)} />
    <IconButton ref={filterRef} className={styles.action} label="Filter sessions" icon="bi-funnel"
      variant={filtersActive ? 'accent' : 'ghost'} aria-haspopup="dialog" aria-expanded={panel === 'filters'} aria-controls={id}
      onClick={event => toggle('filters', event.currentTarget)} />
    <IconButton className={styles.action} label="New session" icon="bi-plus-lg" variant="accent" onClick={onNewSession} />
    <Popover open={panel !== null} onClose={() => setPanel(null)} triggerRef={triggerRef} id={id}
      label={panel === 'project' ? 'Choose project' : 'Session filters'} className={panel === 'project' ? styles.projectPanel : ''}
      style={{ top, right: 12, maxHeight: `calc(100dvh - ${top + 12}px)` }}>
      {panel === 'project'
        ? <ProjectScopePicker projects={projects} value={dirScope} onChange={scope => { setDirScope(scope); setPanel(null); projectRef.current?.focus(); }} />
        : children}
    </Popover>
  </div>;
}
