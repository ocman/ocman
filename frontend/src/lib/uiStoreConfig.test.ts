import { describe, expect, it } from 'vitest';
import { uiStorePersistence } from './uiStoreConfig';
import { reconcileTabOrder } from '../components/rightPanelTabs';

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
});
