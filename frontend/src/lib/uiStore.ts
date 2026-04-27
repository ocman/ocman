import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 600;
export const SIDEBAR_DEFAULT_WIDTH = 260;

// Right-hand session-changes sidebar bounds. Defaults match the
// historical fixed CSS sizing (`.oc-changes-sidebar` width / min /
// max in SessionChangesSidebar.css) so users who haven't dragged the
// handle see no visual change.
export const CHANGES_SIDEBAR_MIN_WIDTH = 320;
export const CHANGES_SIDEBAR_MAX_WIDTH = 720;
export const CHANGES_SIDEBAR_DEFAULT_WIDTH = 480;

type PaletteMode = 'command' | 'search' | 'project';

export type PaletteCommand =
  | { kind: 'nav'; id: string; label: string; path: string }
  | { kind: 'scoped'; id: string; label: string; description: string };

export type SidebarView = 'recent' | 'projects';

// One of the views available in the right-hand panel. Adding a new
// view is just an extra entry here plus a render branch in
// RightPanel — the strip / open-tabs logic handles n tabs uniformly.
export type ChangesSidebarTab = 'session' | 'working-tree';

// Per-tab height fraction in split mode. Sums to 1 across openTabs.
// Values are pinned to a minimum of 0.1 so a pane can't be dragged
// to zero (it would become unrecoverable without keyboard support).
export type ChangesSidebarTabSizes = Partial<Record<ChangesSidebarTab, number>>;

type UiStore = {
  shortcutsOpen: boolean;
  openShortcuts: () => void;
  closeShortcuts: () => void;
  toggleShortcuts: () => void;

  sidebarWidth: number;
  setSidebarWidth: (width: number) => void;

  sidebarView: SidebarView;
  setSidebarView: (view: SidebarView) => void;
  toggleSidebarView: () => void;

  // Collapsed project directories in the "projects" sidebar view. Stored as
  // a plain string[] (not Set) so Zustand's persist middleware can serialise
  // it. Missing entries are treated as expanded.
  collapsedProjects: string[];
  toggleCollapsedProject: (directory: string) => void;

  bellEnabled: boolean;
  setBellEnabled: (enabled: boolean) => void;

  // Ordered list of currently-open views in the right-hand panel.
  // Empty = panel is collapsed (strip-only). One entry = single
  // view. Multiple entries = vertically split, in order top-to-
  // bottom. Order matches the click sequence so the most recently
  // opened view appears at the bottom.
  changesSidebarOpenTabs: ChangesSidebarTab[];
  // Per-tab vertical size as a fraction of the panel content area.
  // Values for tabs not present in openTabs are ignored. Missing
  // entries default to "even share" (1 / openTabs.length).
  changesSidebarTabSizes: ChangesSidebarTabSizes;
  // Click on a strip icon. Implements:
  //   - tab not open  -> add it (creating a split if another view
  //                      was already open).
  //   - tab is open   -> close it (collapse if it was the only one).
  toggleChangesSidebarTab: (tab: ChangesSidebarTab) => void;
  // Direct setters used by tests, command palette, and pane action
  // buttons (e.g. "show only this view" / "close this pane").
  setChangesSidebarOpenTabs: (tabs: ChangesSidebarTab[]) => void;
  closeChangesSidebarTab: (tab: ChangesSidebarTab) => void;
  // Updates the size fractions during a drag. Caller is responsible
  // for keeping the values in sync (typically pairs of adjacent
  // panes that grow/shrink together).
  setChangesSidebarTabSize: (tab: ChangesSidebarTab, size: number) => void;

  // Width of the right-hand session-changes sidebar (in px) when
  // expanded. Persisted so the user's choice survives reloads.
  changesSidebarWidth: number;
  setChangesSidebarWidth: (width: number) => void;

  paletteOpen: boolean;
  paletteMode: PaletteMode;
  openCommandPalette: () => void;
  openSearchPalette: () => void;
  openProjectPalette: () => void;
  openPalette: (mode: PaletteMode) => void;
  closePalette: () => void;

  paletteCommand: PaletteCommand | null;
  dispatchCommand: (cmd: PaletteCommand) => void;
};

function clampWidth(width: number): number {
  if (!Number.isFinite(width)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)));
}

function clampChangesWidth(width: number): number {
  if (!Number.isFinite(width)) return CHANGES_SIDEBAR_DEFAULT_WIDTH;
  return Math.min(
    CHANGES_SIDEBAR_MAX_WIDTH,
    Math.max(CHANGES_SIDEBAR_MIN_WIDTH, Math.round(width)),
  );
}

export const useUiStore = create<UiStore>()(
  persist(
    (set) => ({
      shortcutsOpen: false,
      openShortcuts: () => set({ shortcutsOpen: true }),
      closeShortcuts: () => set({ shortcutsOpen: false }),
      toggleShortcuts: () => set((s) => ({ shortcutsOpen: !s.shortcutsOpen })),

      sidebarWidth: SIDEBAR_DEFAULT_WIDTH,
      setSidebarWidth: (width) => set({ sidebarWidth: clampWidth(width) }),

      sidebarView: 'recent',
      setSidebarView: (view) => set({ sidebarView: view }),
      toggleSidebarView: () =>
        set((s) => ({ sidebarView: s.sidebarView === 'recent' ? 'projects' : 'recent' })),

      collapsedProjects: [],
      toggleCollapsedProject: (directory) =>
        set((s) => ({
          collapsedProjects: s.collapsedProjects.includes(directory)
            ? s.collapsedProjects.filter((d) => d !== directory)
            : [...s.collapsedProjects, directory],
        })),

      bellEnabled: true,
      setBellEnabled: (enabled) => set({ bellEnabled: enabled }),

      changesSidebarOpenTabs: ['session'],
      changesSidebarTabSizes: {},
      toggleChangesSidebarTab: (tab) =>
        set((s) => {
          const open = s.changesSidebarOpenTabs;
          if (open.includes(tab)) {
            // Closing — drop the tab. The other panes' size
            // fractions are dropped too so the remaining tabs get
            // an even share again. (User-set sizes are most
            // intuitive when they describe the *current* split,
            // not historical configurations.)
            const next = open.filter((t) => t !== tab);
            return {
              changesSidebarOpenTabs: next,
              changesSidebarTabSizes: {},
            };
          }
          // Opening — append. The most recently opened tab goes
          // at the bottom of the split.
          return {
            changesSidebarOpenTabs: [...open, tab],
            changesSidebarTabSizes: {},
          };
        }),
      setChangesSidebarOpenTabs: (tabs) =>
        set({ changesSidebarOpenTabs: tabs, changesSidebarTabSizes: {} }),
      closeChangesSidebarTab: (tab) =>
        set((s) => ({
          changesSidebarOpenTabs: s.changesSidebarOpenTabs.filter((t) => t !== tab),
          changesSidebarTabSizes: {},
        })),
      setChangesSidebarTabSize: (tab, size) =>
        set((s) => ({
          changesSidebarTabSizes: { ...s.changesSidebarTabSizes, [tab]: size },
        })),

      changesSidebarWidth: CHANGES_SIDEBAR_DEFAULT_WIDTH,
      setChangesSidebarWidth: (width) =>
        set({ changesSidebarWidth: clampChangesWidth(width) }),

      paletteOpen: false,
      paletteMode: 'command',
      openCommandPalette: () => set({ paletteOpen: true, paletteMode: 'command' }),
      openSearchPalette: () => set({ paletteOpen: true, paletteMode: 'search' }),
      openProjectPalette: () => set({ paletteOpen: true, paletteMode: 'project' }),
      openPalette: (mode: PaletteMode) => set({ paletteOpen: true, paletteMode: mode }),
      closePalette: () => set({ paletteOpen: false, paletteCommand: null }),

      paletteCommand: null,
      dispatchCommand: (cmd: PaletteCommand) => set({ paletteCommand: cmd }),
    }),
    {
      name: 'ocman:ui',
      // Bumped from implicit 0 -> 1 when the right-panel tab id was
      // renamed from 'thread' to 'session'. The migrate step below
      // rewrites persisted values for users who saved the old id.
      version: 1,
      migrate: (persisted, version) => {
        if (!persisted || typeof persisted !== 'object') return persisted;
        const next = persisted as Record<string, unknown>;
        if (version < 1) {
          if (Array.isArray(next.changesSidebarOpenTabs)) {
            next.changesSidebarOpenTabs = (next.changesSidebarOpenTabs as unknown[])
              .map((t) => (t === 'thread' ? 'session' : t));
          }
          if (next.changesSidebarTabSizes && typeof next.changesSidebarTabSizes === 'object') {
            const sizes = next.changesSidebarTabSizes as Record<string, unknown>;
            if ('thread' in sizes) {
              sizes.session = sizes.thread;
              delete sizes.thread;
            }
          }
        }
        return next;
      },
      // Only persist layout preferences; transient UI state (shortcutsOpen) stays in memory.
      partialize: (s) => ({
        sidebarWidth: s.sidebarWidth,
        bellEnabled: s.bellEnabled,
        sidebarView: s.sidebarView,
        collapsedProjects: s.collapsedProjects,
        changesSidebarWidth: s.changesSidebarWidth,
        changesSidebarOpenTabs: s.changesSidebarOpenTabs,
        changesSidebarTabSizes: s.changesSidebarTabSizes,
      }),
    },
  ),
);
