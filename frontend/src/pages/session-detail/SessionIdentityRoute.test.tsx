// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useParams } from 'react-router-dom';
import { SessionIdentityRoute } from './SessionIdentityRoute';

const mocks = vi.hoisted(() => ({ resolve: vi.fn(), ensure: vi.fn(), migrate: vi.fn(), capabilities: { ownedSessionIds: true } as { ownedSessionIds?: boolean } }));
vi.mock('../../lib/sessionIdentity', () => ({ resolveLegacySession: mocks.resolve }));
vi.mock('../../lib/sessionIdentityState', () => ({ migrateSessionBrowserState: mocks.migrate, ensureSessionBrowserState: mocks.ensure }));
vi.mock('../../lib/useCapabilities', () => ({ useCapabilities: () => mocks.capabilities }));

function RouteView() {
  const { id } = useParams();
  const location = useLocation();
  return <SessionIdentityRoute id={id}><div data-testid="conversation">{location.pathname}{location.search}{location.hash}</div></SessionIdentityRoute>;
}

function open(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/session/:id" element={<RouteView />} /></Routes></MemoryRouter>);
}

beforeEach(() => { mocks.resolve.mockReset(); mocks.ensure.mockReset().mockResolvedValue(undefined); mocks.migrate.mockReset().mockResolvedValue(undefined); mocks.capabilities = { ownedSessionIds: true }; });

describe('SessionIdentityRoute', () => {
  it('canonicalizes legacy links before mounting the conversation, preserving query and message anchors', async () => {
    let finish!: (identity: unknown) => void;
    mocks.resolve.mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    open('/session/native?platform=test&peek=1#message');
    expect(screen.queryByTestId('conversation')).toBeNull();
    expect(screen.getByRole('status')).toHaveTextContent('Resolving session');
    finish({ id: 'ocm_owned', platform: 'test', nativeId: 'native', ownerId: 'owner' });
    await waitFor(() => expect(screen.getByTestId('conversation')).toHaveTextContent('/session/ocm_owned?platform=test&peek=1#message'));
    expect(mocks.resolve).toHaveBeenCalledWith('native', 'test', expect.any(AbortSignal));
  });

  it('never resolves an Ocman ID as a native ID', () => {
    open('/session/ocm_missing?platform=test');
    expect(screen.getByTestId('conversation')).toBeInTheDocument();
    expect(mocks.resolve).not.toHaveBeenCalled();
    expect(mocks.ensure).toHaveBeenCalledWith('ocm_missing');
  });

  it('retries shared canonical migration without unmounting the conversation', async () => {
    mocks.ensure.mockRejectedValueOnce(new Error('Owner offline')).mockResolvedValueOnce(undefined);
    open('/session/ocm_missing?platform=test');
    expect(await screen.findByRole('alert')).toHaveTextContent('Owner offline');
    const conversation = screen.getByTestId('conversation');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(mocks.ensure).toHaveBeenCalledTimes(2));
    expect(screen.getByTestId('conversation')).toBe(conversation);
    expect(mocks.resolve).not.toHaveBeenCalled();
  });

  it('shows resolution errors and retries instead of opening a native fallback', async () => {
    mocks.resolve.mockRejectedValueOnce(new Error('Owner offline'));
    mocks.resolve.mockResolvedValueOnce({ id: 'ocm_owned', platform: 'test' });
    open('/session/native?platform=test');
    expect(await screen.findByRole('alert')).toHaveTextContent('Owner offline');
    expect(screen.queryByTestId('conversation')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.getByTestId('conversation')).toHaveTextContent('/session/ocm_owned'));
  });

  it('supports older servers without the capability and uncreated drafts', () => {
    mocks.capabilities = {};
    const page = open('/session/native');
    expect(screen.getByTestId('conversation')).toBeInTheDocument();
    page.unmount();
    mocks.capabilities = { ownedSessionIds: true };
    open('/session/new?dir=/repo');
    expect(screen.getByTestId('conversation')).toBeInTheDocument();
    expect(mocks.resolve).not.toHaveBeenCalled();
  });
});
