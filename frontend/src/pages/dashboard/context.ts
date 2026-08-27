/**
 * Shared dashboard context — sessions + projects are fetched once in the
 * layout and shared across all three tab routes.
 */
import { createContext, useContext } from 'react';
import type { Project, Session } from '../../lib/api';

export interface DashboardCtx {
  sessions: Session[];
  projects: Project[];
  sessionsLoading: boolean;
  sessionsError: string | null;
  projectsLoading: boolean;
  /**
   * Error message from the projects query, or null. Distinct from
   * "loaded and empty": consumers must render this instead of an
   * onboarding empty state, otherwise a backend outage reads as "you
   * have no projects".
   */
  projectsError: string | null;
  loadSessions: () => void;
  refetchProjects: () => void;
  timeRange: number;
  setTimeRange: (v: number) => void;
  showArchived: boolean;
  setShowArchived: (v: boolean) => void;
  /**
   * Active project-prefix scope, persisted in the URL as `?dir=`. Empty
   * string means "all projects". Shared across Analytics and Projects so a
   * chosen scope survives tab switches.
   * See spec/stats-project-filter/architecture.md (AD-3, AD-5).
   */
  dirScope: string;
  setDirScope: (v: string) => void;
}

export const DashboardContext = createContext<DashboardCtx | null>(null);

export function useDashboard(): DashboardCtx {
  const ctx = useContext(DashboardContext);
  if (!ctx) throw new Error('useDashboard must be used inside DashboardLayout');
  return ctx;
}
