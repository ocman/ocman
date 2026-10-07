import type { PersistOptions } from 'zustand/middleware';
import type { UiStore } from './uiStore';

export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 600;
export const SIDEBAR_DEFAULT_WIDTH = 260;
export const CHANGES_SIDEBAR_MIN_WIDTH = 320;
export const CHANGES_SIDEBAR_MAX_WIDTH = 720;
export const CHANGES_SIDEBAR_DEFAULT_WIDTH = 480;
export const SESSION_HOURS_DEFAULT = 72;
export const SESSION_HOURS_MIN = 1;
export const SESSION_HOURS_MAX = 8760;

export type BuiltinSidebarTab = 'info' | 'session' | 'working-tree' | 'bookmarks' | 'upstream' | 'artifacts';
export type ChangesSidebarTab = BuiltinSidebarTab | `plugin:${string}/${string}`;
export type ChangesSidebarTabSizes = Partial<Record<ChangesSidebarTab, number>>;
export type ArtifactsSidebarScope = 'session' | 'project';
export type SidebarView = 'recent' | 'projects';
export type PaletteMode = 'command' | 'search' | 'project' | 'project-session';
export type PaletteCommand =
  | { kind: 'nav'; id: string; label: string; path: string }
  | { kind: 'scoped'; id: string; label: string; description: string };

export const uiStorePersistence: PersistOptions<UiStore, Partial<UiStore>> = {
  name: 'ocman:ui',
  version: 7,
  migrate: (persisted, version) => {
    if (!persisted || typeof persisted !== 'object') return persisted as Partial<UiStore>;
    const next = persisted as Record<string, unknown>;
    if (version < 1) {
      if (Array.isArray(next.changesSidebarOpenTabs)) {
        next.changesSidebarOpenTabs = next.changesSidebarOpenTabs.map((t) => t === 'thread' ? 'session' : t);
      }
      if (next.changesSidebarTabSizes && typeof next.changesSidebarTabSizes === 'object') {
        const sizes = next.changesSidebarTabSizes as Record<string, unknown>;
        if ('thread' in sizes) {
          sizes.session = sizes.thread;
          delete sizes.thread;
        }
      }
    }
    if (version < 4) next.changesSidebarTabOrder = ['info', 'session', 'working-tree', 'bookmarks', 'upstream'];
    return next as Partial<UiStore>;
  },
  partialize: (s) => ({
    mainNavCollapsed: s.mainNavCollapsed,
    lastOpenedSessionId: s.lastOpenedSessionId,
    sidebarWidth: s.sidebarWidth,
    sidebarView: s.sidebarView,
    bellEnabled: s.bellEnabled,
    showToolDetails: s.showToolDetails,
    showReasoning: s.showReasoning,
    showMessageMetadata: s.showMessageMetadata,
    autoReadAnswers: s.autoReadAnswers,
    speechVoiceURI: s.speechVoiceURI,
    speechRate: s.speechRate,
    notificationsEnabled: s.notificationsEnabled,
    collapsedProjects: s.collapsedProjects,
    projectOrder: s.projectOrder,
    changesSidebarWidth: s.changesSidebarWidth,
    changesSidebarOpenTabs: s.changesSidebarOpenTabs,
    changesSidebarTabOrder: s.changesSidebarTabOrder,
    changesSidebarTabSizes: s.changesSidebarTabSizes,
    artifactsSidebarScope: s.artifactsSidebarScope,
    autoApproveDefault: s.autoApproveDefault,
    autoApproveDelayMs: s.autoApproveDelayMs,
    promptSections: s.promptSections,
    dashboardTimeRangeDefault: s.dashboardTimeRangeDefault,
    sidebarRecentHours: s.sidebarRecentHours,
  }),
};
