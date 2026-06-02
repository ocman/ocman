// @vitest-environment jsdom

import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { MessageBookmarksPane } from './MessageBookmarksPane';
import { messageBookmarkKey, type MessageBookmark, type MessageBookmarkGroup } from '../lib/messageBookmarks';

function bookmark(overrides: Partial<MessageBookmark> = {}): MessageBookmark {
  return {
    id: 'msg_1',
    sessionId: 'sess_1',
    role: 'User',
    preview: 'Bookmarked text',
    timeCreated: 1,
    directory: '/tmp/proj',
    projectDirectory: '/tmp/proj',
    sessionTitle: 'Session one',
    ...overrides,
  };
}

function group(overrides: Partial<MessageBookmarkGroup> = {}): MessageBookmarkGroup {
  return {
    projectDirectory: '/tmp/proj',
    label: 'proj',
    current: true,
    bookmarks: [bookmark()],
    ...overrides,
  };
}

describe('MessageBookmarksPane', () => {
  it('shows an empty state when no bookmarks exist', () => {
    render(
      <MessageBookmarksPane groups={[]} selectedKey={null} onRemove={vi.fn()} onScrollToMessage={vi.fn()} />,
    );

    expect(screen.getByText('No bookmarked messages yet.')).toBeInTheDocument();
  });

  it('renders grouped bookmarks with the current project marker', () => {
    render(
      <MessageBookmarksPane
        groups={[
          group({
            label: 'current project',
            current: true,
            bookmarks: [bookmark({ id: 'msg_current', preview: 'current bookmark' })],
          }),
          group({
            projectDirectory: '/tmp/other',
            label: 'other project',
            current: false,
            bookmarks: [bookmark({ id: 'msg_other', sessionId: 'sess_2', preview: 'other bookmark' })],
          }),
        ]}
        selectedKey={null}
        onRemove={vi.fn()}
        onScrollToMessage={vi.fn()}
      />,
    );

    expect(screen.getByText('current project')).toBeInTheDocument();
    expect(screen.getByText('Current')).toBeInTheDocument();
    expect(screen.getByText('other project')).toBeInTheDocument();
    const previews = screen.getAllByText(/bookmark$/);
    expect(previews[0]).toHaveTextContent('current bookmark');
    expect(previews[1]).toHaveTextContent('other bookmark');
  });

  it('renders the full row text and relies on CSS for ten-line clamping', () => {
    const preview = Array.from({ length: 12 }, (_, i) => `line ${i + 1}`).join('\n');
    const { container } = render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [bookmark({ preview })] })]}
        selectedKey={null}
        onRemove={vi.fn()}
        onScrollToMessage={vi.fn()}
      />,
    );

    const previewEl = container.querySelector('.oc-bookmark-row-preview');
    expect(previewEl).toHaveTextContent('line 1');
    expect(previewEl).toHaveTextContent('line 10');
    expect(previewEl).toHaveTextContent('line 12');
  });

  it('clicking a row scrolls directly without rendering an in-tab preview action', () => {
    const onScrollToMessage = vi.fn();
    const target = bookmark({ id: 'msg_target', preview: 'go here' });
    render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [target] })]}
        selectedKey={messageBookmarkKey(target)}
        onRemove={vi.fn()}
        onScrollToMessage={onScrollToMessage}
      />,
    );

    expect(screen.queryByText('Scroll to message')).not.toBeInTheDocument();
    const row = screen.getByText('go here').closest('.oc-bookmark-row');
    expect(row).not.toBeNull();
    expect(row).toHaveClass('active');
    fireEvent.click(row as HTMLElement);

    expect(onScrollToMessage).toHaveBeenCalledWith(target);
  });

  it('supports keyboard row activation', () => {
    const onScrollToMessage = vi.fn();
    const target = bookmark({ id: 'msg_key', preview: 'keyboard target' });
    render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [target] })]}
        selectedKey={null}
        onRemove={vi.fn()}
        onScrollToMessage={onScrollToMessage}
      />,
    );

    const row = screen.getByText('keyboard target').closest('.oc-bookmark-row') as HTMLElement;
    fireEvent.keyDown(row, { key: 'Enter' });
    fireEvent.keyDown(row, { key: ' ' });

    expect(onScrollToMessage).toHaveBeenCalledTimes(2);
    expect(onScrollToMessage).toHaveBeenLastCalledWith(target);
  });

  it('removes a bookmark from the hover delete button without scrolling', () => {
    const onRemove = vi.fn();
    const onScrollToMessage = vi.fn();
    const target = bookmark({ id: 'msg_remove', preview: 'remove me' });
    render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [target] })]}
        selectedKey={null}
        onRemove={onRemove}
        onScrollToMessage={onScrollToMessage}
      />,
    );

    const row = screen.getByText('remove me').closest('.oc-bookmark-row') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Remove bookmark' }));

    expect(onRemove).toHaveBeenCalledWith(target);
    expect(onScrollToMessage).not.toHaveBeenCalled();
  });
});
