// @vitest-environment jsdom
import { render, screen, fireEvent } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';
import { useIsPhone } from '../lib/useIsPhone';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: vi.fn(() => true) }));
vi.mock('../lib/useUpstreams', () => ({ useUpstreams: () => ({ upstreams: [] }) }));
vi.mock('../lib/pluginPanes', async (original) => ({
  ...await original<typeof import('../lib/pluginPanes')>(),
  usePluginPanes: () => ({ data: [] }),
}));
vi.mock('./RightPanelPane', () => ({ Pane: ({ tab }: { tab: string }) => <div data-testid="pane">{tab}</div> }));
vi.mock('./PhoneSessionTools', () => ({ PhoneSessionTools: () => null }));

beforeEach(() => {
  vi.mocked(useIsPhone).mockReturnValue(true);
  useUiStore.persist.setOptions({ storage: { getItem: () => null, setItem() {}, removeItem() {} } });
  useUiStore.setState({ changesSidebarOpenTabs: ['info', 'session'], changesSidebarTabOrder: [], changesSidebarTabSizes: {} });
});

function panel(onCloseMobilePanel?: () => void) {
  return <RightPanel sessionId="s1" messageBookmarkGroups={[]} selectedMessageBookmarkKey={null}
    platformId={undefined} directory={undefined} onRemoveMessageBookmark={vi.fn()}
    onScrollToMessageBookmark={vi.fn()} onCloseMobilePanel={onCloseMobilePanel} />;
}

it('renders only one mobile pane and preserves desktop splits on resize', () => {
  const view = render(panel());
  expect(screen.getAllByTestId('pane')).toHaveLength(1);
  expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['info', 'session']);
  vi.mocked(useIsPhone).mockReturnValue(false);
  view.rerender(panel());
  expect(screen.getAllByTestId('pane')).toHaveLength(2);
});

it('replaces the active pane and dismisses mobile details when it is toggled off', () => {
  const close = vi.fn();
  render(panel(close));
  fireEvent.click(screen.getByRole('tab', { name: 'Bookmarks' }));
  expect(screen.getAllByTestId('pane')).toHaveLength(1);
  expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['bookmarks']);
  expect(close).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('tab', { name: 'Bookmarks' }));
  expect(screen.queryByTestId('pane')).not.toBeInTheDocument();
  expect(close).toHaveBeenCalledOnce();
});

it('keeps desktop toggle behavior independent of overlay dismissal', () => {
  vi.mocked(useIsPhone).mockReturnValue(false);
  const close = vi.fn();
  render(panel(close));
  fireEvent.click(screen.getByRole('tab', { name: 'Bookmarks' }));
  expect(screen.getAllByTestId('pane')).toHaveLength(3);
  fireEvent.click(screen.getByRole('tab', { name: 'Bookmarks' }));
  expect(screen.getAllByTestId('pane')).toHaveLength(2);
  expect(close).not.toHaveBeenCalled();
});

it('can close a pane without an overlay callback', () => {
  render(panel());
  fireEvent.click(screen.getByRole('tab', { name: 'Session info' }));
  expect(useUiStore.getState().changesSidebarOpenTabs).toEqual([]);
});
