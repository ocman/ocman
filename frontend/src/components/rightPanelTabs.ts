import type { ChangesSidebarTab } from '../lib/uiStore';

export const TAB_LABELS: Record<string, string> = {
  info: 'Session info',
  session: 'Session changes',
  'working-tree': 'Working tree',
  bookmarks: 'Bookmarks',
  upstream: 'PRs & Issues',
  artifacts: 'Artifacts',
};

// Info = info-circle icon (context / MCP / LSP overview).
// Session = pencil icon. Working tree = git branch icon. Upstream =
// inbox icon (incoming PRs / issues from the upstream forge —
// Bootstrap Icons doesn't ship a pull-request glyph, so inbox is
// the closest semantic neighbour). Same icon family used everywhere
// else in the app.
export const TAB_ICONS: Record<string, string> = {
  info: 'bi-info-circle',
  session: 'bi-pencil-square',
  'working-tree': 'bi-git',
  bookmarks: 'bi-bookmarks',
  upstream: 'bi-inbox',
  artifacts: 'bi-box-seam',
};

// Default strip order, used as a fallback when the persisted order
// is missing entries (e.g. a new tab was added in a later ocman
// version). The user's drag-reordered list lives in the ui store.
export const DEFAULT_TAB_ORDER: ChangesSidebarTab[] = [
  'info',
  'session',
  'working-tree',
  'bookmarks',
  'upstream',
  'artifacts',
];

// Minimum height fraction a single pane is allowed to occupy. Stops
// the user dragging a pane down to zero (where it would become
// unrecoverable without keyboard support).
export const MIN_PANE_FRACTION = 0.1;

// reconcileTabOrder returns a complete ordering of every known tab,
// starting from the user's persisted order and appending any tabs
// that have been introduced since (or removing any that no longer
// exist). The result always contains exactly DEFAULT_TAB_ORDER's
// entries, in the user's preferred sequence where specified.
export function reconcileTabOrder(persisted: ChangesSidebarTab[], pluginTabs: ChangesSidebarTab[] = []): ChangesSidebarTab[] {
  const available = [...DEFAULT_TAB_ORDER, ...pluginTabs];
  const known = new Set<ChangesSidebarTab>(available);
  const seen = new Set<ChangesSidebarTab>();
  const result: ChangesSidebarTab[] = [];
  for (const t of persisted) {
    if (known.has(t) && !seen.has(t)) {
      result.push(t);
      seen.add(t);
    }
  }
  for (const t of available) {
    if (!seen.has(t)) result.push(t);
  }
  return result;
}

// normaliseSizes returns one fraction per openTab in order. Stored
// values are honoured when present (after clamping to MIN_PANE_FRACTION);
// remaining tabs get an even share of whatever's left so the result
// always sums to 1.
export function normaliseSizes(
  openTabs: ChangesSidebarTab[],
  sizes: Partial<Record<ChangesSidebarTab, number>>,
): number[] {
  const n = openTabs.length;
  if (n === 0) return [];
  if (n === 1) return [1];

  const result: number[] = [];
  let assigned = 0;
  let unassignedCount = 0;
  for (const t of openTabs) {
    const v = sizes[t];
    if (typeof v === 'number' && v > 0) {
      const clamped = Math.max(MIN_PANE_FRACTION, v);
      result.push(clamped);
      assigned += clamped;
    } else {
      result.push(-1); // placeholder
      unassignedCount++;
    }
  }
  if (unassignedCount === 0) {
    // All explicit — rescale so they sum to 1.
    return result.map((x) => x / assigned);
  }
  const remainder = Math.max(0, 1 - assigned);
  const share = unassignedCount > 0 ? remainder / unassignedCount : 0;
  return result.map((x) => (x === -1 ? Math.max(MIN_PANE_FRACTION, share) : x));
}
