// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SessionInfoSidebar } from './SessionInfoSidebar';
import type { Session } from '../lib/api';

const useGitInfo = vi.hoisted(() => vi.fn(() => ({ infos: {}, loading: false, error: null })));

vi.mock('../lib/useCapabilities', () => ({
  usePlatformCapabilities: () => ({ sessionInfo: false }),
}));
vi.mock('../lib/useSessionInfo', () => ({
  useSessionInfo: () => ({ data: null, loading: false, error: null, refresh: vi.fn() }),
}));
vi.mock('../lib/useGitInfo', () => ({
  useGitInfo,
}));

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 's', platform: 'opencode', projectId: 'p', title: 't', directory: '/tmp',
    timeCreated: 0, timeUpdated: 0, summaryAdditions: null, summaryDeletions: null,
    summaryFiles: null, shareUrl: null, messageCount: 0, durationMs: 0,
    activeDurationMs: 0, totalInputTokens: 0, totalOutputTokens: 0, totalCost: 0,
    status: 'done', liveConnection: false, pendingPermission: false,
    pendingQuestion: false, archived: false, seen: true, pinned: false,
    pinnedAt: 0, seenTimeUpdated: 0, unreadCount: 0, ...overrides,
  };
}

function renderSidebar(session: Session) {
  return render(
    <MemoryRouter>
      <SessionInfoSidebar sessionId={session.id} platformId="opencode" session={session} />
    </MemoryRouter>,
  );
}

describe('SessionInfoSidebar parent link', () => {
  it('loads git information from the session owner', () => {
    renderSidebar(makeSession({ directory: '/remote/repo', remoteId: 'box' }));
    expect(useGitInfo).toHaveBeenCalledWith(['/remote/repo'], 'box');
  });

  it('links to the parent session when parentId is set', () => {
    renderSidebar(makeSession({ parentId: 'parent 1' }));
    const link = screen.getByRole('link', { name: 'View parent session' });
    expect(link).toHaveAttribute('href', '/session/parent%201');
  });

  it('renders no parent link for a top-level session', () => {
    renderSidebar(makeSession());
    expect(screen.queryByRole('link', { name: 'View parent session' })).toBeNull();
  });
});
