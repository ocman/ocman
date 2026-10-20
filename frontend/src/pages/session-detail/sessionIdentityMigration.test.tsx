// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { act, render, renderHook, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { useEffect } from 'react';
import { SessionIdentityRoute } from './SessionIdentityRoute';
import { lookupSessionIdentity } from '../../lib/sessionIdentity';
import { useMessageBookmarks } from './useMessageBookmarks';
import { getFirstSubmission, reconcileFirstSubmission, useFirstSubmission } from './firstSubmission';
import { migrateSessionBrowserState, resetSessionIdentityStateForTests } from '../../lib/sessionIdentityState';
import { transact } from '../../lib/draftDb';

const identity = { id: 'ocm_migrated', nativeId: 'native', platform: 'test', ownerId: 'owner' };
vi.mock('../../lib/sessionIdentity', () => ({ lookupSessionIdentity: vi.fn(async () => ({ id: 'ocm_migrated', nativeId: 'native', platform: 'test', ownerId: 'owner' })) }));
vi.mock('../../lib/useCapabilities', () => ({ useCapabilities: () => ({ ownedSessionIds: true }) }));

beforeEach(() => {
  localStorage.clear(); resetSessionIdentityStateForTests();
  useFirstSubmission.setState({ entries: {}, ready: {} });
  vi.mocked(lookupSessionIdentity).mockClear();
});

it('shares canonical identity resolution between the route and first-delivery readiness', async () => {
  function Conversation() {
    useEffect(() => { void reconcileFirstSubmission(identity.id); }, []);
    return <div>conversation</div>;
  }
  render(<MemoryRouter initialEntries={['/session/ocm_migrated?platform=test']}><SessionIdentityRoute id={identity.id}><Conversation /></SessionIdentityRoute></MemoryRouter>);
  await waitFor(() => expect(useFirstSubmission.getState().ready[identity.id]).toBe(true));
  expect(lookupSessionIdentity).toHaveBeenCalledTimes(1);
});

it('refreshes mounted bookmark state after canonical-route adoption without remounting', async () => {
  localStorage.setItem('ocman:message-bookmarks:native', JSON.stringify([{ id: 'message', sessionId: 'native', preview: 'saved', role: 'User', timeCreated: 1 }]));
  const { result } = renderHook(() => useMessageBookmarks({ id: identity.id, session: null, messages: [], parts: [], recentSessions: [] }));
  expect(result.current.bookmarkedMessageIds.has('message')).toBe(false);
  await act(() => migrateSessionBrowserState(identity));
  await waitFor(() => expect(result.current.bookmarkedMessageIds.has('message')).toBe(true));
  expect(result.current.messageBookmarkGroups.flatMap((group) => group.bookmarks).map((b) => b.sessionId)).toEqual([identity.id]);
});

it('does not treat a canonical deep link as a fresh first-delivery scope', async () => {
  await transact(['starts'], 'readwrite', (tx) => tx.put('starts', 'first-delivery:native', {
    version: 3, text: 'uncertain prompt', attemptId: 'original-attempt', deliveryState: 'interrupted', deliveryOwner: 'old-tab',
  }));
  await reconcileFirstSubmission(identity.id);
  expect(useFirstSubmission.getState().ready[identity.id]).toBe(true);
  expect(getFirstSubmission(identity.id)).toMatchObject({ text: 'uncertain prompt', pending: true });
  expect(getFirstSubmission(identity.id)?.execute).toBeUndefined();
});
