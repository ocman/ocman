import { describe, expect, it } from 'vitest';
import { uiStorePersistence } from './uiStoreConfig';
import { mergeVisibleTabOrder, reconcileTabOrder } from '../components/rightPanelTabs';

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
});
