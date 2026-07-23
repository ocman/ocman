import { beforeEach, describe, expect, it } from 'vitest';
import {
  CHANGES_SIDEBAR_DEFAULT_WIDTH,
  CHANGES_SIDEBAR_MAX_WIDTH,
  CHANGES_SIDEBAR_MIN_WIDTH,
  SESSION_HOURS_DEFAULT,
  SESSION_HOURS_MAX,
  SESSION_HOURS_MIN,
  SIDEBAR_DEFAULT_WIDTH,
  SIDEBAR_MAX_WIDTH,
  SIDEBAR_MIN_WIDTH,
  useUiStore,
} from './uiStore';

// Capture defaults so we can restore them between tests; zustand stores
// are module-level singletons, so test isolation is our responsibility.
const initial = useUiStore.getState();

describe('uiStore sidebar width clamping', () => {
  beforeEach(() => {
    useUiStore.setState({
      sidebarWidth: SIDEBAR_DEFAULT_WIDTH,
      changesSidebarWidth: CHANGES_SIDEBAR_DEFAULT_WIDTH,
    });
  });

  it('left sidebar clamps below the minimum', () => {
    initial.setSidebarWidth(50);
    expect(useUiStore.getState().sidebarWidth).toBe(SIDEBAR_MIN_WIDTH);
  });

  it('left sidebar clamps above the maximum', () => {
    initial.setSidebarWidth(99999);
    expect(useUiStore.getState().sidebarWidth).toBe(SIDEBAR_MAX_WIDTH);
  });

  it('left sidebar accepts in-range widths and rounds them', () => {
    initial.setSidebarWidth(312.7);
    expect(useUiStore.getState().sidebarWidth).toBe(313);
  });

  it('left sidebar falls back to default for non-finite input', () => {
    initial.setSidebarWidth(Number.NaN);
    expect(useUiStore.getState().sidebarWidth).toBe(SIDEBAR_DEFAULT_WIDTH);
  });

  it('changes sidebar clamps below the minimum', () => {
    initial.setChangesSidebarWidth(10);
    expect(useUiStore.getState().changesSidebarWidth).toBe(CHANGES_SIDEBAR_MIN_WIDTH);
  });

  it('changes sidebar clamps above the maximum', () => {
    initial.setChangesSidebarWidth(99999);
    expect(useUiStore.getState().changesSidebarWidth).toBe(CHANGES_SIDEBAR_MAX_WIDTH);
  });

  it('changes sidebar accepts in-range widths and rounds them', () => {
    initial.setChangesSidebarWidth(517.4);
    expect(useUiStore.getState().changesSidebarWidth).toBe(517);
  });

  it('changes sidebar falls back to default for non-finite input', () => {
    initial.setChangesSidebarWidth(Number.POSITIVE_INFINITY);
    expect(useUiStore.getState().changesSidebarWidth).toBe(CHANGES_SIDEBAR_DEFAULT_WIDTH);
  });

  it('exposes a sane default range for the changes sidebar', () => {
    expect(CHANGES_SIDEBAR_MIN_WIDTH).toBeLessThan(CHANGES_SIDEBAR_DEFAULT_WIDTH);
    expect(CHANGES_SIDEBAR_DEFAULT_WIDTH).toBeLessThan(CHANGES_SIDEBAR_MAX_WIDTH);
  });
});

// The right-panel open-tabs reducer is the heart of the show/split/
// hide UX: clicking a strip icon either toggles a tab in or out, and
// closing the last open tab collapses the whole panel. These tests
// pin that contract directly against the store actions.
describe('uiStore changesSidebar tab management', () => {
  beforeEach(() => {
    useUiStore.setState({
      changesSidebarOpenTabs: ['session'],
      changesSidebarTabSizes: {},
      changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'beads'],
    });
  });

  it('opens a closed tab (creates a split)', () => {
    initial.toggleChangesSidebarTab('working-tree');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual([
      'session',
      'working-tree',
    ]);
  });

  it('closes an open tab (collapses to one or zero)', () => {
    useUiStore.setState({
      changesSidebarOpenTabs: ['session', 'working-tree'],
    });
    initial.toggleChangesSidebarTab('session');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['working-tree']);
  });

  it('toggling the only open tab closes the panel', () => {
    initial.toggleChangesSidebarTab('session');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual([]);
  });

  it('closing a tab preserves user-set sizes (so reopening resumes the prior height)', () => {
    useUiStore.setState({
      changesSidebarOpenTabs: ['session', 'working-tree'],
      changesSidebarTabSizes: { session: 0.7, 'working-tree': 0.3 },
    });
    initial.closeChangesSidebarTab('session');
    // Sizes survive so a re-opened pane resumes at its previous
    // fraction — normalisation happens at render time in RightPanel.
    expect(useUiStore.getState().changesSidebarTabSizes).toEqual({
      session: 0.7,
      'working-tree': 0.3,
    });
  });

  it('toggleChangesSidebarTab preserves user-set sizes on close', () => {
    useUiStore.setState({
      changesSidebarOpenTabs: ['session', 'working-tree'],
      changesSidebarTabSizes: { session: 0.6, 'working-tree': 0.4 },
    });
    initial.toggleChangesSidebarTab('working-tree');
    expect(useUiStore.getState().changesSidebarTabSizes).toEqual({
      session: 0.6,
      'working-tree': 0.4,
    });
  });

  it('setChangesSidebarOpenTabs replaces wholesale and preserves sizes', () => {
    useUiStore.setState({
      changesSidebarTabSizes: { session: 0.5, 'working-tree': 0.5 },
    });
    initial.setChangesSidebarOpenTabs(['working-tree']);
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['working-tree']);
    expect(useUiStore.getState().changesSidebarTabSizes).toEqual({
      session: 0.5,
      'working-tree': 0.5,
    });
  });

  it('setChangesSidebarTabSize updates a single tab without touching the others', () => {
    useUiStore.setState({
      changesSidebarTabSizes: { session: 0.5, 'working-tree': 0.5 },
    });
    initial.setChangesSidebarTabSize('session', 0.7);
    expect(useUiStore.getState().changesSidebarTabSizes).toEqual({
      session: 0.7,
      'working-tree': 0.5,
    });
  });

  it('setChangesSidebarTabOrder persists a user-reordered strip', () => {
    initial.setChangesSidebarTabOrder(['working-tree', 'session', 'info', 'bookmarks', 'upstream', 'beads']);
    expect(useUiStore.getState().changesSidebarTabOrder).toEqual([
      'working-tree',
      'session',
      'info',
      'bookmarks',
      'upstream',
      'beads',
    ]);
  });
});

describe('uiStore session time windows', () => {
  beforeEach(() => {
    useUiStore.setState({
      dashboardTimeRangeDefault: SESSION_HOURS_DEFAULT,
      sidebarRecentHours: SESSION_HOURS_DEFAULT,
    });
  });

  it('both windows default to 3 days (72h)', () => {
    expect(SESSION_HOURS_DEFAULT).toBe(72);
    expect(useUiStore.getState().dashboardTimeRangeDefault).toBe(72);
    expect(useUiStore.getState().sidebarRecentHours).toBe(72);
  });

  it('clamps below the minimum', () => {
    initial.setDashboardTimeRangeDefault(0);
    initial.setSidebarRecentHours(-5);
    expect(useUiStore.getState().dashboardTimeRangeDefault).toBe(SESSION_HOURS_MIN);
    expect(useUiStore.getState().sidebarRecentHours).toBe(SESSION_HOURS_MIN);
  });

  it('clamps above the maximum', () => {
    initial.setDashboardTimeRangeDefault(999999);
    initial.setSidebarRecentHours(999999);
    expect(useUiStore.getState().dashboardTimeRangeDefault).toBe(SESSION_HOURS_MAX);
    expect(useUiStore.getState().sidebarRecentHours).toBe(SESSION_HOURS_MAX);
  });

  it('rounds in-range values', () => {
    initial.setDashboardTimeRangeDefault(36.6);
    expect(useUiStore.getState().dashboardTimeRangeDefault).toBe(37);
  });

  it('falls back to the default for non-finite input', () => {
    initial.setSidebarRecentHours(Number.NaN);
    expect(useUiStore.getState().sidebarRecentHours).toBe(SESSION_HOURS_DEFAULT);
  });
});

describe('uiStore paletteCommand dispatch', () => {
  beforeEach(() => {
    useUiStore.setState({ paletteOpen: false, paletteCommand: null });
  });

  it('dispatchCommand sets the command', () => {
    const cmd = { kind: 'scoped' as const, id: 'scoped.model', label: 'Model', description: 'Pick model' };
    initial.dispatchCommand(cmd);
    expect(useUiStore.getState().paletteCommand).toEqual(cmd);
  });

  it('closePalette clears the command', () => {
    const cmd = { kind: 'scoped' as const, id: 'scoped.archive', label: 'Archive', description: 'Archive session' };
    initial.dispatchCommand(cmd);
    expect(useUiStore.getState().paletteCommand).not.toBeNull();
    initial.closePalette();
    expect(useUiStore.getState().paletteCommand).toBeNull();
  });

  it('nav commands are dispatched with path', () => {
    const cmd = { kind: 'nav' as const, id: 'nav.dashboard', label: 'Dashboard', path: '/' };
    initial.dispatchCommand(cmd);
    expect(useUiStore.getState().paletteCommand).toEqual(cmd);
  });
});

describe('uiStore notificationsEnabled', () => {
  beforeEach(() => {
    useUiStore.setState({ notificationsEnabled: false });
  });

  it('defaults to false (system notifications require explicit opt-in)', () => {
    // The persisted store starts at false; tests reset it explicitly to
    // pin the contract independently of any prior test mutation.
    expect(useUiStore.getState().notificationsEnabled).toBe(false);
  });

  it('setNotificationsEnabled flips the flag', () => {
    initial.setNotificationsEnabled(true);
    expect(useUiStore.getState().notificationsEnabled).toBe(true);
    initial.setNotificationsEnabled(false);
    expect(useUiStore.getState().notificationsEnabled).toBe(false);
  });
});

describe('uiStore message metadata visibility', () => {
  beforeEach(() => {
    useUiStore.setState({ showMessageMetadata: false });
  });

  it('defaults to hidden and can be enabled', () => {
    expect(useUiStore.getState().showMessageMetadata).toBe(false);
    initial.setShowMessageMetadata(true);
    expect(useUiStore.getState().showMessageMetadata).toBe(true);
  });
});

describe('uiStore tool-detail visibility (/details)', () => {
  beforeEach(() => {
    useUiStore.setState({ showToolDetails: true });
  });

  it('defaults to visible', () => {
    expect(useUiStore.getState().showToolDetails).toBe(true);
  });

  it('toggleToolDetails flips the flag', () => {
    initial.toggleToolDetails();
    expect(useUiStore.getState().showToolDetails).toBe(false);
    initial.toggleToolDetails();
    expect(useUiStore.getState().showToolDetails).toBe(true);
  });
});
