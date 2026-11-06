// @vitest-environment jsdom
import { act, fireEvent, screen } from '@testing-library/react';
import { renderWithQuery as render } from '../test/renderWithQuery';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import { useIsPhone } from '../lib/useIsPhone';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: vi.fn(() => false) }));

const mocks = vi.hoisted(() => ({
  apiState: { archiveSession: vi.fn(async () => ({ ok: true })) },
}));
vi.mock('../lib/api', async () => {
  const real = await vi.importActual<typeof import('../lib/api')>('../lib/api');
  return { ...real, api: { ...real.api, ...mocks.apiState } };
});
vi.mock('../lib/useCapabilities', () => ({ useMultiHost: () => true }));

import { SessionTable } from './SessionTable';

function HistoryControls() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output aria-label="Current path">{location.pathname}</output><button onClick={() => navigate(-1)}>Back</button></>;
}

function makeSession(): Session {
  return {
    id: 'ses_1', platform: 'opencode', title: 'Work', directory: '/src/foo', status: 'done', seen: true,
    messageCount: 3, durationMs: 1000, timeCreated: 1000, timeUpdated: 1000,
  } as Session;
}

describe('SessionTable archive and navigation', () => {
  beforeEach(() => { vi.clearAllMocks(); vi.mocked(useIsPhone).mockReturnValue(false); });
  afterEach(() => vi.unstubAllGlobals());

  it('ignores numeric unread counts from older servers', () => {
    render(<MemoryRouter><SessionTable sessions={[{ ...makeSession(), seen: false, unreadCount: 7 }]} showProject={false} /></MemoryRouter>);
    expect(screen.getByRole('link', { name: /Work/ })).toBeInTheDocument();
    expect(screen.queryByTestId('session-unread-badge')).not.toBeInTheDocument();
  });

  it('keeps whole-row navigation after removing the count badge', () => {
    render(<MemoryRouter><HistoryControls /><SessionTable sessions={[makeSession()]} showProject={false} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('link', { name: /Work/ }).closest('tr')!);
    expect(screen.getByLabelText('Current path')).toHaveTextContent('/session/ses_1');
  });

  it.each([-100, 100])('keeps phone swipe actions without numeric badges: %i', async (distance) => {
    class TouchPointer extends MouseEvent {
      pointerId = 1;
      pointerType = 'touch';
      isPrimary = true;
    }
    vi.stubGlobal('PointerEvent', TouchPointer);
    vi.mocked(useIsPhone).mockReturnValue(true);
    render(<MemoryRouter><SessionTable sessions={[{ ...makeSession(), seen: false, unreadCount: 120 }]} showProject={false} /></MemoryRouter>);
    const row = screen.getByRole('link', { name: /Work/ }).closest('tr')!;
    row.setPointerCapture = vi.fn();
    fireEvent.pointerDown(row, { clientX: 150, clientY: 150 });
    fireEvent.pointerMove(row, { clientX: 150 + distance, clientY: 150 });
    fireEvent.pointerUp(row, { clientX: 150 + distance, clientY: 150 });
    expect(screen.queryByTestId('session-unread-badge')).not.toBeInTheDocument();
    if (distance > 0) expect(mocks.apiState.archiveSession).toHaveBeenCalled();
    else expect(await screen.findByRole('menuitem', { name: 'Custom...' })).toBeInTheDocument();
  });

  it('hides a phone row before the archive response and restores it on failure', async () => {
    vi.mocked(useIsPhone).mockReturnValue(true);
    let fail!: (error: Error) => void;
    mocks.apiState.archiveSession.mockReturnValueOnce(new Promise((_resolve, reject) => { fail = reject; }));
    render(<MemoryRouter><SessionTable sessions={[makeSession()]} showProject={false} /></MemoryRouter>);
    fireEvent.click(screen.getByRole('button', { name: 'Archive session' }));
    expect(screen.queryByRole('link', { name: /Work/ })).not.toBeInTheDocument();
    await act(async () => { fail(new Error('Archive unavailable')); });
    expect(screen.getByRole('link', { name: /Work/ })).toBeInTheDocument();
    expect(screen.getByRole('alert')).toHaveTextContent('Could not archive session');
  });

  it('a session title adds only one history entry', () => {
    render(<MemoryRouter initialEntries={['/sessions']}>
      <HistoryControls />
      <SessionTable sessions={[makeSession()]} showProject={false} />
    </MemoryRouter>);
    fireEvent.click(screen.getByRole('link', { name: /Work/ }));
    expect(screen.getByLabelText('Current path')).toHaveTextContent('/session/ses_1');
    fireEvent.click(screen.getByRole('button', { name: /^Back$/ }));
    expect(screen.getByLabelText('Current path')).toHaveTextContent('/sessions');
  });
});
