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

  it('renders the full preview text and relies on CSS for line clamping', () => {
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
    expect(previewEl).toHaveTextContent('line 5');
    expect(previewEl).toHaveTextContent('line 12');
  });

  it('clicking a row toggles its expanded state without scrolling', () => {
    const onScrollToMessage = vi.fn();
    const target = bookmark({ id: 'msg_target', preview: 'expand me' });
    render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [target] })]}
        selectedKey={messageBookmarkKey(target)}
        onRemove={vi.fn()}
        onScrollToMessage={onScrollToMessage}
      />,
    );

    const row = screen.getByText('expand me').closest('.oc-bookmark-row') as HTMLElement;
    expect(row).toHaveClass('active');
    expect(row).not.toHaveClass('expanded');
    expect(row).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(row);
    expect(row).toHaveClass('expanded');
    expect(row).toHaveAttribute('aria-expanded', 'true');

    fireEvent.click(row);
    expect(row).not.toHaveClass('expanded');
    expect(onScrollToMessage).not.toHaveBeenCalled();
  });

  it('toggles expansion via keyboard activation', () => {
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
    expect(row).toHaveClass('expanded');
    fireEvent.keyDown(row, { key: ' ' });
    expect(row).not.toHaveClass('expanded');
    expect(onScrollToMessage).not.toHaveBeenCalled();
  });

  it('navigates to the bookmark via the inline "Go to" button without toggling the row', () => {
    const onScrollToMessage = vi.fn();
    const target = bookmark({ id: 'msg_goto', preview: 'jump here' });
    render(
      <MessageBookmarksPane
        groups={[group({ bookmarks: [target] })]}
        selectedKey={null}
        onRemove={vi.fn()}
        onScrollToMessage={onScrollToMessage}
      />,
    );

    const row = screen.getByText('jump here').closest('.oc-bookmark-row') as HTMLElement;
    fireEvent.click(within(row).getByRole('button', { name: 'Go to bookmark' }));

    expect(onScrollToMessage).toHaveBeenCalledWith(target);
    expect(row).not.toHaveClass('expanded');
  });

  it('removes a bookmark from the inline delete button without scrolling or toggling', () => {
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
    expect(row).not.toHaveClass('expanded');
  });
});
