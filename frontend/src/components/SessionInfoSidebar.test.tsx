// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SessionInfoSidebar } from './SessionInfoSidebar';
import type { Session } from '../lib/api';
import { __handleSessionChangedForTests } from '../lib/useGlobalEvents';

const useGitInfo = vi.hoisted(() => vi.fn(() => ({ infos: {}, loading: false, error: null })));
const sessionInfoResult = vi.hoisted(() => ({ data: null as null | Record<string, unknown>, loading: false, error: null, refresh: vi.fn() }));
const useSessionInfo = vi.hoisted(() => vi.fn(() => sessionInfoResult));

vi.mock('../lib/useCapabilities', () => ({
  usePlatformCapabilities: () => ({ sessionInfo: false }),
}));
vi.mock('../lib/useSessionInfo', () => ({
  useSessionInfo,
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

describe('SessionInfoSidebar commits', () => {
  it('renders captured commits with recorded branch semantics', () => {
    sessionInfoResult.data = {
      sessionId: 's', supported: false,
      context: { tokens: 0, cost: 0, estCost: 0 },
      tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      messages: { user: 0, assistant: 0 }, mcpServers: [], lspServers: [],
      commitCaptureSupported: true,
      commits: [
        { order: 1, sha: 'abcdef123', branch: 'feature/x', subject: 'Add capture' },
        { order: 2, sha: '123456789', branch: null, subject: 'Detached work' },
      ],
    };
    renderSidebar(makeSession());
    expect(screen.getByRole('heading', { name: 'Commits' })).toBeInTheDocument();
    expect(screen.getByText('abcdef1')).toBeInTheDocument();
    expect(screen.getByText('feature/x')).toBeInTheDocument();
    expect(screen.getByText('Detached HEAD')).toBeInTheDocument();
    expect(screen.getByText('Detached work')).toBeInTheDocument();
    sessionInfoResult.data = null;
  });

  it('routes equal remote session IDs through their compound platform', () => {
    render(
      <MemoryRouter>
        <SessionInfoSidebar sessionId="same" platformId="r-owner-two:opencode" session={makeSession({ id: 'same' })} />
      </MemoryRouter>,
    );
    expect(useSessionInfo).toHaveBeenCalledWith('same', expect.objectContaining({
      platformId: 'r-owner-two:opencode',
    }));
  });

  it('does not claim capture support for an older remote payload', () => {
    sessionInfoResult.data = {
      sessionId: 's', supported: false,
      context: { tokens: 0, cost: 0, estCost: 0 },
      tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      messages: { user: 0, assistant: 0 }, mcpServers: [], lspServers: [],
    };
    renderSidebar(makeSession({ platform: 'r-old:opencode' }));
    expect(screen.queryByRole('heading', { name: 'Commits' })).toBeNull();
    sessionInfoResult.data = null;
  });

  it('refetches info after a matching persisted-change notification', () => {
    sessionInfoResult.refresh.mockClear();
    renderSidebar(makeSession());
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 'other' }));
    expect(sessionInfoResult.refresh).not.toHaveBeenCalled();
    __handleSessionChangedForTests(JSON.stringify({ sessionID: 's' }));
    expect(sessionInfoResult.refresh).toHaveBeenCalledOnce();
  });
});
