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

// Session time windows (in hours), user-configurable in Settings.
// Both default to 3 days so returning after a weekend still shows
// recent work (issues #141 / #142).
//   dashboardTimeRangeDefault — the start-screen Sessions tab's default
//     lookback when no `?t=` URL param is set.
//   sidebarRecentHours — the per-session "recent sessions" sidebar window.
export const SESSION_HOURS_DEFAULT = 72;
export const SESSION_HOURS_MIN = 1;
export const SESSION_HOURS_MAX = 8760; // 1 year

type PaletteMode = 'command' | 'search' | 'project' | 'project-session';

export type PaletteCommand =
  | { kind: 'nav'; id: string; label: string; path: string }
  | { kind: 'scoped'; id: string; label: string; description: string };

// One of the views available in the right-hand panel. Adding a new
// view is just an extra entry here plus a render branch in
// RightPanel — the strip / open-tabs logic handles n tabs uniformly.
//
// 'info' is the per-session info view (context tokens / MCP / LSP);
// it stacks above the change-related panes. 'upstream' is the PR/Issue
// sidebar (spec/pr-issue-sidebar/), only shown when the current project
// has a supported GitHub/Forgejo remote.
export type ChangesSidebarTab = 'info' | 'session' | 'working-tree' | 'bookmarks' | 'upstream' | 'loops';

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

  // Collapsed project directories in the "projects" sidebar view. Stored as
  // a plain string[] (not Set) so Zustand's persist middleware can serialise
  // it. Missing entries are treated as expanded.
  collapsedProjects: string[];
  toggleCollapsedProject: (directory: string) => void;

  // User-controlled order of project groups in the "projects" sidebar
  // view. Stored as an ordered list of project root directories. The
  // sidebar sorts project groups alphabetically by default; any
  // directories present here are honoured first (in this order), with
  // unknown / newly-seen projects appended alphabetically. Mutated by
  // drag-and-drop reordering of the group headers. The synthetic
  // "__pinned__" group is never stored here — it stays pinned to the top.
  projectOrder: string[];
  setProjectOrder: (order: string[]) => void;

  bellEnabled: boolean;
  setBellEnabled: (enabled: boolean) => void;

  // OS-level Web Notifications. Off by default — enabling requires the
  // user to grant browser permission, so we never preemptively claim
  // they're enabled. Persisted alongside other preferences.
  notificationsEnabled: boolean;
  setNotificationsEnabled: (enabled: boolean) => void;

  // Auto-approve: global default and human-review delay.
  // autoApproveDefault — when true, new sessions start with auto-approve
  //   enabled (unless overridden per-session on the server).
  // autoApproveDelayMs — how long to wait after a permission prompt
  //   arrives before starting the AI judge, giving the human a window
  //   to respond manually. Default 5 000 ms.
  autoApproveDefault: boolean;
  setAutoApproveDefault: (enabled: boolean) => void;
  autoApproveDelayMs: number;
  setAutoApproveDelayMs: (ms: number) => void;

  // Session time windows (hours). See SESSION_HOURS_* constants above.
  dashboardTimeRangeDefault: number;
  setDashboardTimeRangeDefault: (hours: number) => void;
  sidebarRecentHours: number;
  setSidebarRecentHours: (hours: number) => void;

  // Custom sections appended to the AI judge prompt. Each section is
  // rendered as "## <title>\n<content>" and injected after the built-in
  // assessment criteria, allowing users to extend the default ruleset
  // (e.g. "allow commits to feature branches").
  promptSections: Array<{ title: string; content: string; enabled?: boolean }>;
  setPromptSections: (
    sections: Array<{ title: string; content: string; enabled?: boolean }>,
  ) => void;

  // Ordered list of currently-open views in the right-hand panel.
  // Empty = panel is collapsed (strip-only). One entry = single
  // view. Multiple entries = vertically split, in order top-to-
  // bottom. Order matches the click sequence so the most recently
  // opened view appears at the bottom.
  changesSidebarOpenTabs: ChangesSidebarTab[];
  // User-controlled order of ALL tabs in the strip (open or
  // closed). Mutated by drag-and-drop reordering of the icon strip.
  // The rendered pane stack uses this order, filtered by openTabs.
  // Missing entries (newly-introduced tabs in newer ocman versions)
  // are appended at the end on first render so old persisted state
  // remains compatible.
  changesSidebarTabOrder: ChangesSidebarTab[];
  setChangesSidebarTabOrder: (order: ChangesSidebarTab[]) => void;
  // Per-tab vertical size as a fraction of the panel content area.
  // Values for tabs not present in openTabs are ignored. Missing
  // entries default to "even share" (1 / openTabs.length).
  changesSidebarTabSizes: ChangesSidebarTabSizes;
  // Click on a strip icon. Implements:
  //   - tab not open  -> add it (creating a split if another view
  //                      was already open).
  //   - tab is open   -> close it (collapse if it was the only one).
  // Per-tab sizes are preserved across toggles so a re-opened pane
  // resumes at its previous height.
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
  openProjectSessionPalette: () => void;
  openPalette: (mode: PaletteMode) => void;
  closePalette: () => void;

  paletteCommand: PaletteCommand | null;
  dispatchCommand: (cmd: PaletteCommand) => void;

  // Worktree-creation modal (the /wt flow). Opening the modal also
  // closes the palette so the two never overlap. `worktreeFormGen`
  // increments on each open so the inner form component can be keyed
  // for a clean remount (fresh useState defaults) without reading
  // refs during render.
  worktreeFormOpen: boolean;
  worktreeFormGen: number;
  worktreeFormProject: string | undefined;
  worktreeFormBranch: string | undefined;
  openWorktreeForm: (opts?: { projectDir?: string; branch?: string }) => void;
  closeWorktreeForm: () => void;
};

function clampWidth(width: number): number {
  if (!Number.isFinite(width)) return SIDEBAR_DEFAULT_WIDTH;
  return Math.min(SIDEBAR_MAX_WIDTH, Math.max(SIDEBAR_MIN_WIDTH, Math.round(width)));
}

function clampSessionHours(hours: number): number {
  if (!Number.isFinite(hours)) return SESSION_HOURS_DEFAULT;
  return Math.min(SESSION_HOURS_MAX, Math.max(SESSION_HOURS_MIN, Math.round(hours)));
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

      collapsedProjects: [],
      toggleCollapsedProject: (directory) =>
        set((s) => ({
          collapsedProjects: s.collapsedProjects.includes(directory)
            ? s.collapsedProjects.filter((d) => d !== directory)
            : [...s.collapsedProjects, directory],
        })),

      projectOrder: [],
      setProjectOrder: (order) => set({ projectOrder: order }),

      bellEnabled: true,
      setBellEnabled: (enabled) => set({ bellEnabled: enabled }),

      notificationsEnabled: false,
      setNotificationsEnabled: (enabled) => set({ notificationsEnabled: enabled }),

      autoApproveDefault: false,
      setAutoApproveDefault: (enabled) => set({ autoApproveDefault: enabled }),
      autoApproveDelayMs: 5000,
      setAutoApproveDelayMs: (ms) => set({ autoApproveDelayMs: Math.max(0, Math.round(ms)) }),

      dashboardTimeRangeDefault: SESSION_HOURS_DEFAULT,
      setDashboardTimeRangeDefault: (hours) =>
        set({ dashboardTimeRangeDefault: clampSessionHours(hours) }),
      sidebarRecentHours: SESSION_HOURS_DEFAULT,
      setSidebarRecentHours: (hours) =>
        set({ sidebarRecentHours: clampSessionHours(hours) }),

      promptSections: [
        {
          title: 'Feature branch commits and pushes',
          content:
            'git commit and git push are SAFE when the target branch is not "main" or "master". ' +
            'If the patterns or action mention a branch name that is not main or master, treat commit/push as safe.',
        },
      ],
      setPromptSections: (sections) => set({ promptSections: sections }),

      changesSidebarOpenTabs: ['session'],
      changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'loops'],
      setChangesSidebarTabOrder: (order) => set({ changesSidebarTabOrder: order }),
      changesSidebarTabSizes: {},
      toggleChangesSidebarTab: (tab) =>
        set((s) => {
          const open = s.changesSidebarOpenTabs;
          if (open.includes(tab)) {
            // Closing — drop the tab. Per-tab sizes for the
            // remaining open panes are preserved so reopening a
            // closed pane resumes at its previous height. The
            // closed tab's own size entry is kept too — it just
            // becomes inert until the tab reopens.
            return {
              changesSidebarOpenTabs: open.filter((t) => t !== tab),
            };
          }
          // Opening — append. Sizes are preserved; normaliseSizes
          // in RightPanel handles new tabs by giving them an even
          // share of the remaining space.
          return {
            changesSidebarOpenTabs: [...open, tab],
          };
        }),
      setChangesSidebarOpenTabs: (tabs) =>
        set({ changesSidebarOpenTabs: tabs }),
      closeChangesSidebarTab: (tab) =>
        set((s) => ({
          changesSidebarOpenTabs: s.changesSidebarOpenTabs.filter((t) => t !== tab),
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
      openProjectSessionPalette: () => set({ paletteOpen: true, paletteMode: 'project-session' }),
      openPalette: (mode: PaletteMode) => set({ paletteOpen: true, paletteMode: mode }),
      closePalette: () => set({ paletteOpen: false, paletteCommand: null }),

      paletteCommand: null,
      dispatchCommand: (cmd: PaletteCommand) => set({ paletteCommand: cmd }),

      worktreeFormOpen: false,
      worktreeFormGen: 0,
      worktreeFormProject: undefined,
      worktreeFormBranch: undefined,
      openWorktreeForm: (opts) => set((s) => ({
        worktreeFormOpen: true,
        worktreeFormGen: s.worktreeFormGen + 1,
        worktreeFormProject: opts?.projectDir,
        worktreeFormBranch: opts?.branch,
        // Close the palette if it happened to be open — the modal
        // takes over the focus.
        paletteOpen: false,
      })),
      closeWorktreeForm: () => set({
        worktreeFormOpen: false,
        worktreeFormProject: undefined,
        worktreeFormBranch: undefined,
      }),
    }),
    {
      name: 'ocman:ui',
      // v1: renamed right-panel tab id 'thread' -> 'session'.
      // v2: added autoApproveDefault + autoApproveDelayMs.
      // v3: added promptSections with a default feature-branch rule.
      // v4: added changesSidebarTabOrder (user-controlled strip order
      //     via drag-and-drop). Default seeded from the legacy
      //     hardcoded BASE_TABS so existing users see no change.
      // v5: added the 'loops' right-panel tab; append it to the
      //     persisted strip order so existing users get the new icon.
      // v6: added dashboardTimeRangeDefault + sidebarRecentHours
      //     (configurable session time windows, default 72h).
      version: 6,
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
        if (version < 4) {
          // Seed tab order from the legacy fixed order so the strip
          // looks identical on first load after the upgrade.
          next.changesSidebarTabOrder = ['info', 'session', 'working-tree', 'bookmarks', 'upstream'];
        }
        if (version < 5) {
          // Append the new 'loops' tab to the persisted order if absent
          // (RightPanel.reconcileTabOrder also backfills, but keeping
          // the stored value complete avoids a first-render reshuffle).
          const order = Array.isArray(next.changesSidebarTabOrder)
            ? (next.changesSidebarTabOrder as string[])
            : ['info', 'session', 'working-tree', 'bookmarks', 'upstream'];
          if (!order.includes('loops')) order.push('loops');
          next.changesSidebarTabOrder = order;
        }
        return next;
      },
      // Only persist layout preferences; transient UI state (shortcutsOpen) stays in memory.
      partialize: (s) => ({
        sidebarWidth: s.sidebarWidth,
        bellEnabled: s.bellEnabled,
        notificationsEnabled: s.notificationsEnabled,
        collapsedProjects: s.collapsedProjects,
        projectOrder: s.projectOrder,
        changesSidebarWidth: s.changesSidebarWidth,
        changesSidebarOpenTabs: s.changesSidebarOpenTabs,
        changesSidebarTabOrder: s.changesSidebarTabOrder,
        changesSidebarTabSizes: s.changesSidebarTabSizes,
        autoApproveDefault: s.autoApproveDefault,
        autoApproveDelayMs: s.autoApproveDelayMs,
        promptSections: s.promptSections,
        dashboardTimeRangeDefault: s.dashboardTimeRangeDefault,
        sidebarRecentHours: s.sidebarRecentHours,
      }),
    },
  ),
);
