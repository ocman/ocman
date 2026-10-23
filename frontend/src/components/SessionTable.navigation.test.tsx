// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import { useIsPhone } from '../lib/useIsPhone';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: vi.fn(() => false) }));

const mocks = vi.hoisted(() => ({
  apiState: { archiveSession: vi.fn(async () => ({ ok: true })) },
}));
vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: typeof mocks.apiState) => unknown) => selector(mocks.apiState),
}));
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
