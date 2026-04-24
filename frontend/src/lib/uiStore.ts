import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 600;
export const SIDEBAR_DEFAULT_WIDTH = 260;

type PaletteMode = 'command' | 'search' | 'project';

export type PaletteCommand =
  | { kind: 'nav'; id: string; label: string; path: string }
  | { kind: 'scoped'; id: string; label: string; description: string };

export type SidebarView = 'recent' | 'projects';

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
      // Only persist layout preferences; transient UI state (shortcutsOpen) stays in memory.
      partialize: (s) => ({
        sidebarWidth: s.sidebarWidth,
        bellEnabled: s.bellEnabled,
        sidebarView: s.sidebarView,
        collapsedProjects: s.collapsedProjects,
      }),
    },
  ),
);
