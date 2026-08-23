// @vitest-environment jsdom

import { useEffect } from 'react';
import { render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';

vi.mock('./SessionChangesSidebar', () => ({
  ChangesRefreshButton: () => null,
  SessionChangesSidebar: ({ onSummaryChange, onFullscreen }: {
    onSummaryChange?: (summary: { files: number; additions: number; deletions: number }) => void;
    onFullscreen?: (open: () => void) => void;
  }) => {
    useEffect(() => {
      onSummaryChange?.({ files: 0, additions: 0, deletions: 0 });
      onFullscreen?.(() => {});
    }, [onSummaryChange, onFullscreen]);
    return null;
  },
}));
vi.mock('../lib/useBeadsStatus', () => ({
  useBeadsStatus: () => ({ data: undefined, error: null, isFetching: false, refetch: vi.fn() }),
}));
vi.mock('../lib/useUpstreams', () => ({
  useUpstreams: () => ({ upstreams: [] }),
}));

beforeEach(() => {
  useUiStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  });
  useUiStore.setState({
    changesSidebarOpenTabs: ['session'],
    changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'beads'],
    changesSidebarTabSizes: {},
  });
});

it('disables the embedded fullscreen button when there are no files', async () => {
  render(
    <RightPanel
      sessionId="s1"
      platformId="opencode"
      directory="/repo"
      messageBookmarkGroups={[]}
      selectedMessageBookmarkKey={null}
      onRemoveMessageBookmark={vi.fn()}
      onScrollToMessageBookmark={vi.fn()}
    />,
  );

  expect(await screen.findByRole('button', { name: 'Fullscreen' })).toBeDisabled();
});
