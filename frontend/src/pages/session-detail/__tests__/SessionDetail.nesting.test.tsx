// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { act, fireEvent, screen, waitFor, within } from '@testing-library/react';
import { makeSession, makeSessionDetail, renderSessionPage } from './harness';
import { useUiStore } from '../../../lib/uiStore';
import { __handleSessionChangedForTests } from '../../../lib/useGlobalEvents';
import { visibleSidebarSessions } from '../../../lib/sidebarHelpers';

beforeEach(() => {
  useUiStore.getState().setSidebarView('recent');
  Element.prototype.scrollIntoView = vi.fn();
  Element.prototype.scrollTo = vi.fn();
});

it('keeps a selected child beneath its creator after a named-connection live update', async () => {
  const parent = makeSession({ id: 'parent', platform: 'cnx_work', title: 'Parent task' });
  const child = makeSession({ id: 'child', platform: 'cnx_work', title: 'Child task',
    creator: { platform: parent.platform, sessionId: parent.id, createdAt: 1 } });
  const getSession = vi.fn(async (id: string) => makeSessionDetail(id === child.id ? child : parent));
  renderSessionPage({ sessionId: parent.id, sessions: [child, parent], apiOverrides: { session: getSession } });
  const sidebar = within(screen.getByTestId('session-sidebar'));
  const childRow = () => sidebar.getByText(child.title).closest('[aria-selected]')!;
  await waitFor(() => expect(childRow()).toHaveClass('session-sidebar-item-child'));
  fireEvent.click(childRow());
  await waitFor(() => expect(childRow()).toHaveAttribute('aria-selected', 'true'));
  // A read acknowledgement publishes a replacement compact sidebar row.
  const updated = { ...child, seen: true, seenTimeUpdated: child.timeUpdated };
  act(() => __handleSessionChangedForTests(JSON.stringify({
    sessionID: child.id, platform: child.platform, session: updated, patch: updated,
  })));
  await waitFor(() => {
    expect(childRow()).toHaveClass('session-sidebar-item-child');
    expect(childRow()).toHaveAttribute('aria-selected', 'true');
    expect(visibleSidebarSessions.current?.map(row => row.id)).toEqual([parent.id, child.id]);
  });
  expect(within(childRow() as HTMLElement).getByText('└─')).toHaveStyle({ '--depth': '1' });
});
