// @vitest-environment jsdom

import { useLayoutEffect } from 'react';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { RightPanel } from './RightPanel';
import { useUiStore } from '../lib/uiStore';

let fileCount = 0;
const openFullscreen = vi.fn();

vi.mock('./SessionChangesSidebar', () => ({
  ChangesRefreshButton: () => null,
  SessionChangesSidebar: ({ sessionId, onSummaryChange, onFullscreen }: {
    sessionId: string;
    onSummaryChange?: (summary: { files: number; additions: number; deletions: number }) => void;
    onFullscreen?: (open: () => void) => void;
  }) => {
    useLayoutEffect(() => {
      onSummaryChange?.({ files: fileCount, additions: 0, deletions: 0 });
      onFullscreen?.(openFullscreen);
    }, [sessionId, onSummaryChange, onFullscreen]);
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
  fileCount = 0;
  openFullscreen.mockReset();
  useUiStore.persist.setOptions({
    storage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  });
  useUiStore.setState({
    changesSidebarOpenTabs: ['session'],
    changesSidebarTabOrder: ['info', 'session', 'working-tree', 'bookmarks', 'upstream', 'beads'],
    changesSidebarTabSizes: {},
  });
});

it('tracks files appearing and disappearing in the embedded fullscreen button', async () => {
  const user = userEvent.setup();
  const { rerender } = render(
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

  const button = await screen.findByRole('button', { name: 'Fullscreen' });
  expect(button).toBeDisabled();

  fileCount = 1;
  rerender(
    <RightPanel
      sessionId="s2"
      platformId="opencode"
      directory="/repo"
      messageBookmarkGroups={[]}
      selectedMessageBookmarkKey={null}
      onRemoveMessageBookmark={vi.fn()}
      onScrollToMessageBookmark={vi.fn()}
    />,
  );

  await waitFor(() => expect(button).toBeEnabled());
  await user.click(button);
  expect(openFullscreen).toHaveBeenCalledOnce();

  fileCount = 0;
  rerender(
    <RightPanel
      sessionId="s3"
      platformId="opencode"
      directory="/repo"
      messageBookmarkGroups={[]}
      selectedMessageBookmarkKey={null}
      onRemoveMessageBookmark={vi.fn()}
      onScrollToMessageBookmark={vi.fn()}
    />,
  );

  await waitFor(() => expect(button).toBeDisabled());
});
