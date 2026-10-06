import { describe, expect, it } from 'vitest';
import { uiStorePersistence } from './uiStoreConfig';
import { mergeVisibleTabOrder, normaliseSizes, reconcileTabOrder } from '../components/rightPanelTabs';

describe('plugin pane layout', () => {
  const pluginTab = 'plugin:org.example.tree/items' as const;
  it('preserves opaque plugin preferences and their sizing and order', () => {
    expect(uiStorePersistence.migrate?.({
      changesSidebarOpenTabs: [pluginTab],
      changesSidebarTabOrder: [pluginTab, 'session'],
      changesSidebarTabSizes: { [pluginTab]: 0.7, session: 0.3 },
    }, 6)).toEqual({
      changesSidebarOpenTabs: [pluginTab],
      changesSidebarTabOrder: [pluginTab, 'session'],
      changesSidebarTabSizes: { [pluginTab]: 0.7, session: 0.3 },
    });
  });
  it('preserves an enabled plugin position and hides unknown or disabled panes', () => {
    const order = reconcileTabOrder([pluginTab, 'session', pluginTab], [pluginTab]);
    expect(order.slice(0, 2)).toEqual([pluginTab, 'session']);
    expect(order.filter((tab) => tab === pluginTab)).toHaveLength(1);
    expect(reconcileTabOrder(order)).not.toContain(pluginTab);
  });
  it('keeps another owner’s hidden plugin position while reordering visible tabs', () => {
    const hidden = 'plugin:org.example.other/items' as const;
    const persisted = [hidden, 'session', pluginTab, 'info'] as const;
    const reordered = mergeVisibleTabOrder([...persisted], ['info', pluginTab, 'session']);
    expect(reordered).toEqual([hidden, 'info', pluginTab, 'session']);
    expect(reconcileTabOrder(reordered, [hidden]).slice(0, 2)).toEqual([hidden, 'info']);
  });
  it('appends newly visible tabs and drops duplicate stored entries', () => {
    expect(mergeVisibleTabOrder([], ['info', pluginTab])).toEqual(['info', pluginTab]);
    expect(mergeVisibleTabOrder(['session', 'session', pluginTab], ['info', 'session'])).toEqual(['info', pluginTab, 'session']);
  });
  it('assigns plugin-pane fractions from stored and default sizes', () => {
    expect(normaliseSizes([], {})).toEqual([]);
    expect(normaliseSizes([pluginTab], {})).toEqual([1]);
    expect(normaliseSizes(['session', pluginTab], {})).toEqual([0.5, 0.5]);
    const mixed = normaliseSizes(['session', pluginTab, 'info'], { session: 0.8, info: 0.1 });
    expect(mixed[0]).toBeCloseTo(0.8);
    expect(mixed[1]).toBeCloseTo(0.1);
    expect(mixed[2]).toBeCloseTo(0.1);
    expect(normaliseSizes(['session', pluginTab], { session: 1.2, [pluginTab]: 0.8 })).toEqual([0.6, 0.4]);
    expect(normaliseSizes(['session', pluginTab], { session: 0.01, [pluginTab]: 0.04 })).toEqual([0.5, 0.5]);
  });
  it('retains the older thread-layout migration after splitting the store', () => {
    expect(uiStorePersistence.migrate?.({
      changesSidebarOpenTabs: ['thread'], changesSidebarTabSizes: { thread: 0.7, info: 0.3 },
    }, 0)).toEqual({
      changesSidebarOpenTabs: ['session'], changesSidebarTabSizes: { session: 0.7, info: 0.3 },
      changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream'],
    });
    expect(uiStorePersistence.migrate?.(null, 0)).toBeNull();
    expect(uiStorePersistence.migrate?.({}, 0)).toEqual({ changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream'] });
  });
});
