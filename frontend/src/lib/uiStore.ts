import { create } from 'zustand';
import { persist } from 'zustand/middleware';
import { labelInteraction } from './perfMonitor';

import {
  SIDEBAR_MIN_WIDTH, SIDEBAR_MAX_WIDTH, SIDEBAR_DEFAULT_WIDTH,
  CHANGES_SIDEBAR_MIN_WIDTH, CHANGES_SIDEBAR_MAX_WIDTH, CHANGES_SIDEBAR_DEFAULT_WIDTH,
  SESSION_HOURS_DEFAULT, SESSION_HOURS_MIN, SESSION_HOURS_MAX,
  uiStorePersistence,
  type PaletteMode, type PaletteCommand, type SidebarView,
  type ChangesSidebarTab, type ChangesSidebarTabSizes, type ArtifactsSidebarScope,
} from './uiStoreConfig';
export * from './uiStoreConfig';

export type UiStore = {
  mainNavCollapsed: boolean;
  toggleMainNav: () => void;
  lastOpenedSessionId: string | undefined;
  recordOpenedSession: (sessionId: string) => void;

  shortcutsOpen: boolean;
  openShortcuts: () => void;
  closeShortcuts: () => void;
  toggleShortcuts: () => void;

  sidebarWidth: number;
  setSidebarWidth: (width: number) => void;

  sidebarView: SidebarView;
  setSidebarView: (view: SidebarView) => void;

  // Collapsed project directories in the "projects" sidebar view. Stored as
  // a plain string[] (not Set) so Zustand's persist middleware can serialise
  // it. Missing entries are treated as expanded.
  collapsedProjects: string[];
  toggleCollapsedProject: (directory: string) => void;
  /**
   * Drop `directories` from the collapsed set, persisting the expansion.
   * Called when a session is opened so its project group stays open after
   * the user navigates elsewhere — collapsing is a deliberate click, so it
   * must never be undone by a derived, unpersisted expansion (which used
   * to hide the session the user had just been working in).
   *
   * A no-op returns the previous state so callers can invoke it from an
   * effect without triggering a re-render loop.
   */
  expandProjects: (directories: string[]) => void;

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

  // Tool-execution detail visibility in the conversation thread. When
  // false, the per-message tool call blocks are hidden (mirrors
  // OpenCode's TUI /details toggle). Persisted so it survives reload.
  showToolDetails: boolean;
  toggleToolDetails: () => void;

  // Display-only toggle for assistant reasoning/thinking blocks in the
  // thread view (the `/thinking` command, #290). Does NOT enable or
  // disable model reasoning — it only shows/hides reasoning parts that
  // are already present in the stream, matching OpenCode's semantics.
  showReasoning: boolean;
  setShowReasoning: (enabled: boolean) => void;
  toggleShowReasoning: () => void;

  // Timestamp/model details shown after each assistant message section.
  // Turn summaries remain visible regardless of this preference.
  showMessageMetadata: boolean;
  setShowMessageMetadata: (enabled: boolean) => void;
  autoReadAnswers: boolean;
  setAutoReadAnswers: (enabled: boolean) => void;
  speechVoiceURI: string;
  setSpeechVoiceURI: (uri: string) => void;
  speechRate: number;
  setSpeechRate: (rate: number) => void;

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
  // Sub-tab of the Artifacts pane: this session (+ descendants) or the project.
  artifactsSidebarScope: ArtifactsSidebarScope;
  setArtifactsSidebarScope: (scope: ArtifactsSidebarScope) => void;
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
  projectSessionInitialDirectory: string | undefined;
  openCommandPalette: () => void;
  openSearchPalette: () => void;
  openProjectPalette: () => void;
  openProjectSessionPalette: (initialDirectory?: string) => void;
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
  // Session the /wt flow was launched from, if any. When set, the new
  // worktree session inherits this session's always-allow permissions
  // (#101). Undefined for project-scoped launches (command palette,
  // Worktrees view) that have no "current session".
  worktreeFormParentSessionId: string | undefined;
  // Owning machine of worktreeFormProject; undefined = unknown (backend infers).
  worktreeFormRemoteId: string | undefined;
  openWorktreeForm: (opts?: { projectDir?: string; branch?: string; parentSessionId?: string; remoteId?: string }) => void;
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
      mainNavCollapsed: false,
      toggleMainNav: () => set((s) => ({ mainNavCollapsed: !s.mainNavCollapsed })),
      lastOpenedSessionId: undefined,
      recordOpenedSession: (sessionId) => set({ lastOpenedSessionId: sessionId }),

      shortcutsOpen: false,
      openShortcuts: () => set({ shortcutsOpen: true }),
      closeShortcuts: () => set({ shortcutsOpen: false }),
      toggleShortcuts: () => set((s) => ({ shortcutsOpen: !s.shortcutsOpen })),

      sidebarWidth: SIDEBAR_DEFAULT_WIDTH,
      setSidebarWidth: (width) => set({ sidebarWidth: clampWidth(width) }),

      sidebarView: 'recent',
      setSidebarView: (view) => set({ sidebarView: view }),

      collapsedProjects: [],
      toggleCollapsedProject: (directory) =>
        set((s) => ({
          collapsedProjects: s.collapsedProjects.includes(directory)
            ? s.collapsedProjects.filter((d) => d !== directory)
            : [...s.collapsedProjects, directory],
        })),
      expandProjects: (directories) =>
        set((s) => {
          const drop = new Set(directories);
          const next = s.collapsedProjects.filter((d) => !drop.has(d));
          // Nothing was collapsed: keep the old array so the reference is
          // stable and subscribers don't re-render.
          return next.length === s.collapsedProjects.length ? s : { collapsedProjects: next };
        }),

      projectOrder: [],
      setProjectOrder: (order) => set({ projectOrder: order }),

      bellEnabled: true,
      setBellEnabled: (enabled) => set({ bellEnabled: enabled }),

      showToolDetails: true,
      toggleToolDetails: () => set((s) => ({ showToolDetails: !s.showToolDetails })),

      showReasoning: true,
      setShowReasoning: (enabled) => set({ showReasoning: enabled }),
      toggleShowReasoning: () => set((s) => ({ showReasoning: !s.showReasoning })),

      showMessageMetadata: false,
      setShowMessageMetadata: (enabled) => set({ showMessageMetadata: enabled }),
      autoReadAnswers: false,
      setAutoReadAnswers: (enabled) => set({ autoReadAnswers: enabled }),
      speechVoiceURI: '',
      setSpeechVoiceURI: (uri) => set({ speechVoiceURI: uri }),
      speechRate: 1,
      setSpeechRate: (rate) => set({ speechRate: Math.min(2, Math.max(0.5, rate)) }),

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
      changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'artifacts'],
      setChangesSidebarTabOrder: (order) => set({ changesSidebarTabOrder: order }),
      changesSidebarTabSizes: {},
      artifactsSidebarScope: 'session',
      setArtifactsSidebarScope: (scope) => set({ artifactsSidebarScope: scope }),
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
      projectSessionInitialDirectory: undefined,
      openCommandPalette: () => { labelInteraction('palette-open'); set({ paletteOpen: true, paletteMode: 'command' }); },
      openSearchPalette: () => set({ paletteOpen: true, paletteMode: 'search' }),
      openProjectPalette: () => set({ paletteOpen: true, paletteMode: 'project' }),
      openProjectSessionPalette: (initialDirectory) => set({
        paletteOpen: true,
        paletteMode: 'project-session',
        projectSessionInitialDirectory: initialDirectory,
      }),
      openPalette: (mode: PaletteMode) => {
        labelInteraction('palette-open');
        set({ paletteOpen: true, paletteMode: mode, projectSessionInitialDirectory: undefined });
      },
      closePalette: () => set({
        paletteOpen: false,
        paletteCommand: null,
        projectSessionInitialDirectory: undefined,
      }),

      paletteCommand: null,
      dispatchCommand: (cmd: PaletteCommand) => set({ paletteCommand: cmd }),

      worktreeFormOpen: false,
      worktreeFormGen: 0,
      worktreeFormProject: undefined,
      worktreeFormBranch: undefined,
      worktreeFormParentSessionId: undefined,
      worktreeFormRemoteId: undefined,
      openWorktreeForm: (opts) => set((s) => ({
        worktreeFormOpen: true,
        worktreeFormGen: s.worktreeFormGen + 1,
        worktreeFormProject: opts?.projectDir,
        worktreeFormBranch: opts?.branch,
        worktreeFormParentSessionId: opts?.parentSessionId,
        worktreeFormRemoteId: opts?.remoteId,
        // Close the palette if it happened to be open — the modal
        // takes over the focus.
        paletteOpen: false,
      })),
      closeWorktreeForm: () => set({
        worktreeFormOpen: false,
        worktreeFormProject: undefined,
        worktreeFormBranch: undefined,
        worktreeFormParentSessionId: undefined,
        worktreeFormRemoteId: undefined,
      }),
    }),
    uiStorePersistence,
  ),
);
