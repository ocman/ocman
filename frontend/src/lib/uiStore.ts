import { create } from 'zustand';
import { persist } from 'zustand/middleware';

export const SIDEBAR_MIN_WIDTH = 180;
export const SIDEBAR_MAX_WIDTH = 600;
export const SIDEBAR_DEFAULT_WIDTH = 260;

type UiStore = {
  shortcutsOpen: boolean;
  openShortcuts: () => void;
  closeShortcuts: () => void;
  toggleShortcuts: () => void;

  sidebarWidth: number;
  setSidebarWidth: (width: number) => void;
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
    }),
    {
      name: 'ocman:ui',
      // Only persist layout preferences; transient UI state (shortcutsOpen) stays in memory.
      partialize: (s) => ({ sidebarWidth: s.sidebarWidth }),
    },
  ),
);
