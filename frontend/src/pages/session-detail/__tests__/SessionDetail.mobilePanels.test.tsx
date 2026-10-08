// @vitest-environment jsdom
//
// Phone overlay panels (#mobile layout): the sessions drawer and the
// details overlay are toggled from controls portalled into the header.
// The controls only *display* on <=768px via
// CSS, but their behaviour (state, Escape, auto-close on selection,
// seeding a pane into a collapsed right panel) is viewport-independent
// and testable in jsdom.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, renderHook, screen, waitFor, within } from '@testing-library/react';
import { useShortcutDispatcher } from '../../../lib/shortcutRegistry';
import userEvent from '@testing-library/user-event';
import { flushPromises, makeSession, makeSessionDetail, renderSessionPage } from './harness';
import { useUiStore } from '../../../lib/uiStore';
import type { SessionInfo, FileChange } from '../../../lib/api';
import * as gitInfoHook from '../../../lib/useGitInfo';
import { visibleSidebarSessions } from '../../../lib/sidebarHelpers';

let slot: HTMLDivElement;
let navigationSlot: HTMLSpanElement;
let titleSlot: HTMLSpanElement;

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn() as unknown as typeof Element.prototype.scrollIntoView;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = vi.fn();
  // The toggles portal into the header slot normally rendered by
  // <Header> in App.tsx; the harness renders SessionDetail alone.
  slot = document.createElement('div');
  slot.id = 'header-actions-slot';
  navigationSlot = document.createElement('span');
  navigationSlot.id = 'header-navigation-slot';
  titleSlot = document.createElement('span');
  titleSlot.id = 'header-mobile-title-slot';
  document.body.appendChild(slot);
  document.body.appendChild(navigationSlot);
  document.body.appendChild(titleSlot);
});

afterEach(() => {
  slot.remove();
  navigationSlot.remove();
  titleSlot.remove();
  vi.restoreAllMocks();
});

describe('SessionDetail — phone overlay panels', () => {
  it('keeps branch-only search and archive candidates when the mobile drawer closes', async () => {
    vi.stubGlobal('innerWidth', 390);
    useUiStore.setState({ sidebarView: 'recent' });
    vi.spyOn(gitInfoHook, 'useGitInfo').mockImplementation((dirs) => ({
      infos: dirs?.includes('/branch') ? { '/branch': { branch: 'feature-only' } as never } : {}, loading: false, error: null,
    }));
    const current = makeSession({ id: 'sess_1', directory: '/current', title: 'Current', timeCreated: 300, timeUpdated: 300 });
    const next = makeSession({ id: 'branch-session', directory: '/branch', title: 'Matching branch', timeCreated: 200, timeUpdated: 200 });
    const other = makeSession({ id: 'other-session', directory: '/other', title: 'Other', timeCreated: 100, timeUpdated: 100 });
    try {
      const { api } = renderSessionPage({ sessionId: current.id, detail: makeSessionDetail(current), sessions: [current, next, other] });
      await screen.findByRole('textbox');
      fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
      await screen.findByText('Matching branch');
      fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'feature-only' } });
      await waitFor(() => expect(visibleSidebarSessions.current?.map(row => row.id)).toEqual([current.id, next.id]));
      fireEvent.keyDown(window, { key: 'Escape' });
      expect(visibleSidebarSessions.current?.map(row => row.id)).toEqual([current.id, next.id]);
      const input = screen.getByRole('textbox');
      await userEvent.type(input, '/archive');
      fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
      await waitFor(() => expect(vi.mocked(api.session).mock.calls.some(([id]) => id === next.id)).toBe(true));
    } finally { vi.unstubAllGlobals(); }
  });
  it.each(['archive', 'next'] as const)('reports a forced current-session fallback failure for %s', async (action) => {
    vi.stubGlobal('innerWidth', 390);
    try {
      const { store } = renderSessionPage({ sessionId: 'sess_1', sessions: [], storeOverrides: { getSession: vi.fn().mockRejectedValue(new Error('Fallback unavailable')) } });
      const input = await screen.findByRole('textbox');
      if (action === 'archive') {
        await userEvent.type(input, '/archive');
        fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
      } else {
        const dispatcher = renderHook(() => useShortcutDispatcher());
        input.blur(); fireEvent.keyDown(window, { key: 'j', code: 'KeyJ', altKey: true }); dispatcher.unmount();
      }
      expect(await screen.findByText('Could not load session navigation: Fallback unavailable')).toBeInTheDocument();
      expect(store.archiveSession).not.toHaveBeenCalled();
    } finally { vi.unstubAllGlobals(); }
  });
  it.each(['archive', 'next'] as const)('reports a failed cold navigation read for %s without redirecting', async (action) => {
    vi.stubGlobal('innerWidth', 390);
    try {
      const { store } = renderSessionPage({ sessionId: 'sess_1', storeOverrides: { getSessions: vi.fn().mockRejectedValue(new Error('offline')) } });
      const input = await screen.findByRole('textbox');
      if (action === 'archive') {
        await userEvent.type(input, '/archive');
        fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
      } else {
        const dispatcher = renderHook(() => useShortcutDispatcher());
        input.blur();
        fireEvent.keyDown(window, { key: 'j', code: 'KeyJ', altKey: true });
        dispatcher.unmount();
      }
      expect(await screen.findByText('Could not load session navigation: offline')).toBeInTheDocument();
      expect(store.archiveSession).not.toHaveBeenCalled();
    } finally { vi.unstubAllGlobals(); }
  });
  it.each(['archive', 'next'] as const)('loads cold-mobile navigation candidates on demand for %s', async (action) => {
    vi.stubGlobal('innerWidth', 390);
    useUiStore.setState({ sidebarView: 'recent' });
    try {
      const current = makeSession({ id: 'sess_1', title: 'Current', timeCreated: 200, timeUpdated: 200 });
      const next = makeSession({ id: 'sess_next', title: 'Next', timeCreated: 100, timeUpdated: 100 });
      const { store, api } = renderSessionPage({ sessionId: current.id, detail: makeSessionDetail(current), sessions: [current, next] });
      const input = await screen.findByRole('textbox');
      expect(store.getSessions).not.toHaveBeenCalled();
      if (action === 'archive') {
        await userEvent.type(input, '/archive');
        fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
      } else {
        const dispatcher = renderHook(() => useShortcutDispatcher());
        input.blur();
        fireEvent.keyDown(window, { key: 'j', code: 'KeyJ', altKey: true });
        dispatcher.unmount();
      }
      await waitFor(() => expect(vi.mocked(api.session).mock.calls.some(([id]) => id === next.id)).toBe(true));
      expect(store.getSessions).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
    } finally { vi.unstubAllGlobals(); }
  });
  it('loads Move destinations independently of the closed mobile sidebar', async () => {
    vi.stubGlobal('innerWidth', 390);
    try {
      const projects = vi.fn().mockResolvedValue([{ directory: '/destination', remoteId: 'local', name: 'Destination', archived: false }]);
      renderSessionPage({ sessionId: 'sess_1', apiOverrides: { projects } });
      const input = await screen.findByRole('textbox');
      await userEvent.type(input, '/move');
      fireEvent.click(screen.getByRole('button', { name: 'Send message' }));
      await screen.findByPlaceholderText('Search directories');
      expect(await screen.findByText('/destination')).toBeInTheDocument();
      expect(projects).toHaveBeenCalledTimes(1);
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
    } finally { vi.unstubAllGlobals(); }
  });

  it('retains open desktop pane controls across document hide and resume', async () => {
    let hidden = false;
    const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    useUiStore.setState({ changesSidebarOpenTabs: ['session'] });
    renderSessionPage({ sessionId: 'sess_1' });
    const fullscreen = await screen.findByRole('button', { name: 'Fullscreen' });
    act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
    expect(screen.getByRole('button', { name: 'Fullscreen' })).toBe(fullscreen);
    act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
    expect(screen.getByRole('button', { name: 'Fullscreen' })).toBe(fullscreen);
    visibility.mockRestore();
  });

  it('keeps the fullscreen diff and selected file across document hide/resume', async () => {
    let hidden = false;
    const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    useUiStore.setState({ changesSidebarOpenTabs: ['session'] });
    const files: FileChange[] = ['first.ts', 'second.ts'].map((path) => ({ path, displayPath: path, additions: 1, deletions: 0, editCount: 1, firstEditAt: 0, lastEditAt: 0, before: '', after: 'hello\n', edits: [] }));
    const { result } = renderSessionPage({ sessionId: 'sess_1', sessionChanges: { sessionId: 'sess_1', supported: true, totalAdditions: 2, totalDeletions: 0, filesChanged: 2, files } });
    try {
      const fullscreen = await screen.findByRole('button', { name: 'Fullscreen' });
      await waitFor(() => expect(fullscreen).toBeEnabled());
      await userEvent.click(fullscreen);
      const dialog = screen.getByRole('dialog', { name: 'Session changes' });
      const tree = within(within(dialog).getByTestId('changed-files-tree').shadowRoot as unknown as HTMLElement);
      const second = tree.getByRole('treeitem', { name: 'second.ts' });
      await userEvent.click(second);
      expect(second).toHaveAttribute('aria-selected', 'true');
      act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
      expect(screen.getByRole('dialog', { name: 'Session changes' })).toBe(dialog);
      act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
      expect(screen.getByRole('dialog', { name: 'Session changes' })).toBe(dialog);
      expect(second).toHaveAttribute('aria-selected', 'true');
    } finally { result.unmount(); visibility.mockRestore(); }
  });
  it('loads the session list only after the mobile sidebar opens', async () => {
    vi.stubGlobal('innerWidth', 390);
    try {
      const { store } = renderSessionPage({ sessionId: 'sess_1' });
      await flushPromises();
      expect(store.getSessions).not.toHaveBeenCalled();
      fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
      await waitFor(() => expect(store.getSessions).toHaveBeenCalledTimes(1));
      fireEvent.keyDown(window, { key: 'Escape' });
      fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
      await waitFor(() => expect(store.getSessions).toHaveBeenCalledTimes(2));
    } finally {
      vi.unstubAllGlobals();
    }
  });
  it('toggles the sessions drawer and closes it on Escape', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    const layout = screen.getByTestId('session-layout');
    const toggle = screen.getByTestId('mobile-sessions-toggle');
    expect(layout.className).not.toContain('mobile-sidebar-open');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');
    expect(toggle).toHaveTextContent('Sessions');

    fireEvent.click(toggle);
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');
    expect(screen.getByTestId('mobile-sessions-toggle')).toHaveAttribute('aria-expanded', 'true');
    expect(screen.getByTestId('mobile-sessions-toggle')).toHaveTextContent('Done');
    expect(titleSlot).toHaveTextContent('Sessions');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
  });

  it('clicking the same toggle again closes the drawer', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');
    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
  });

  it('the two panels are mutually exclusive', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    const layout = screen.getByTestId('session-layout');
    expect(layout.className).toContain('mobile-sidebar-open');
    expect(layout.className).not.toContain('mobile-details-open');
    expect(screen.queryByTestId('mobile-details-toggle')).not.toBeInTheDocument();
  });

  it('opening details with a collapsed right panel seeds the info pane', async () => {
    useUiStore.setState({ changesSidebarOpenTabs: [] });
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-details-open');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['info']);
  });

  it('opening details with panes already open keeps them', async () => {
    useUiStore.setState({ changesSidebarOpenTabs: ['session'] });
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['session']);
  });

  it('closes details after jumping to a commit source', async () => {
    useUiStore.setState({ changesSidebarOpenTabs: ['info'] });
    const session = makeSession();
    const detail = makeSessionDetail(session, {
      messages: [{ id: 'message-1', sessionId: session.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'part-1', messageId: 'message-1', sessionId: session.id, data: { type: 'tool', tool: 'bash', callID: 'call-1', state: { status: 'completed' } } }],
    });
    const sessionInfo: SessionInfo = {
      sessionId: session.id, supported: false,
      context: { tokens: 0, cost: 0, estCost: 0 },
      tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      messages: { user: 0, assistant: 0 }, mcpServers: [], lspServers: [],
      commitCaptureSupported: true,
      commits: [{ order: 1, sha: 'abc1234', branch: 'main', subject: 'Close details', sourceMessageId: 'message-1', toolPartId: 'part-1', toolCallId: 'call-1', observedAt: 1 }],
    };
    renderSessionPage({ detail, sessionInfo });
    await screen.findByTestId('assistant-thread');
    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(screen.getByTestId('session-layout')).toHaveClass('mobile-details-open');

    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Close details' }));

    expect(screen.getByTestId('session-layout')).not.toHaveClass('mobile-details-open');
  });

  it('closes an open overlay on an external route change (palette, redirect)', async () => {
    const other = makeSession({ id: 'sess_2', title: 'Other session' });
    const active = makeSession({ id: 'sess_1' });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(active),
      sessions: [active, other],
    });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-details-open');

    // Navigate WITHOUT going through the drawer — e.g. the command
    // palette or the closed-session-reopen shortcut.
    handle.navigate('/session/sess_2');

    await waitFor(() => {
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-details-open');
    });
  });

  it('selecting a session from the drawer closes it', async () => {
    const other = makeSession({ id: 'sess_2', title: 'Other session' });
    const active = makeSession({ id: 'sess_1' });
    renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(active),
      sessions: [active, other],
    });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');

    const row = await screen.findByText('Other session');
    fireEvent.click(row);

    await waitFor(() => {
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
    });
  });
});
