// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { BrowserRouter, Link, Route, Routes } from 'react-router-dom';
import { useSession, type UseSessionOptions } from './useSession';
import { useSessionSeen } from './useSessionSeen';
import { useApiStore } from '../../lib/apiStore';
import type { Session, SessionDetail } from '../../lib/api';

vi.mock('../../lib/useGlobalEvents', () => ({ onSessionChanged: () => () => {}, onSseConnect: () => () => {} }));
vi.mock('../../lib/useFaviconNotify', () => ({ recheckFaviconNotify: () => {} }));

afterEach(() => {
  Reflect.deleteProperty(performance, 'getEntriesByType');
  window.history.replaceState(null, '', '/');
  vi.unstubAllGlobals();
});

it('reopens the original session after a browser reload followed by a route unmount and remount', async () => {
  window.history.replaceState(null, '', '/session/original');
  Object.defineProperty(performance, 'getEntriesByType', {
    configurable: true, value: () => [{ type: 'reload', name: 'http://localhost/session/original' }],
  });
  vi.stubGlobal('EventSource', class { close() {} addEventListener() {} });
  let archived = false;
  const row = { id: 'original', platform: 'opencode', title: 'Original', directory: '/repo', timeUpdated: 42 } as Session;
  const fetchSession = vi.fn<NonNullable<UseSessionOptions['fetchSession']>>(async (_id, _limit, _offset, _signal, _platform, peek) => {
    if (!peek) archived = false;
    return { session: { ...row, archived }, messages: [], parts: [] } as SessionDetail;
  });
  useApiStore.setState({ sessionCache: new Map(), recentSessions: [], markSessionSeen: vi.fn().mockResolvedValue({ ok: true }) });
  function SessionPage() {
    const { session, patchSession } = useSession(row.id, { fetchSession });
    useSessionSeen({ session, patchSession });
    return <div>{session?.title}</div>;
  }
  render(<BrowserRouter>
    <Link to="/inbox">Inbox</Link><Link to="/session/original">Open original</Link>
    <Routes>
      <Route path="/session/original" element={<SessionPage />} />
      <Route path="/inbox" element={<div>Inbox page</div>} />
      <Route path="/session/new" element={<div>New conversation</div>} />
    </Routes>
  </BrowserRouter>);
  await screen.findByText('Original');
  expect(fetchSession.mock.calls[0][5]).toBe(true);
  fireEvent.click(screen.getByRole('link', { name: 'Inbox' }));
  await screen.findByText('Inbox page');
  archived = true;
  await act(async () => fireEvent.click(screen.getByRole('link', { name: 'Open original' })));
  await waitFor(() => expect(fetchSession).toHaveBeenCalledTimes(2));
  expect(fetchSession.mock.calls[1][5]).toBe(false);
  expect(archived).toBe(false);
  expect(window.location.pathname).toBe('/session/original');
});
