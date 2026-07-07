// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SessionSidebar, type SidebarProjectGroup } from './SessionSidebar';
import type { GitInfo, Session } from '../../lib/api';

vi.mock('../../components/BackendStats', () => ({
  BackendStats: () => null,
}));
vi.mock('../../components/SidebarResizer', () => ({
  SidebarResizer: () => null,
}));
const multiHost = vi.fn(() => false);
vi.mock('../../lib/useCapabilities', async (importOriginal) => ({
  ...(await importOriginal<typeof import('../../lib/useCapabilities')>()),
  useMultiHost: () => multiHost(),
}));

function session(overrides: Partial<Session> = {}): Session {
  return {
    id: 's', platform: 'opencode', projectId: 'p', title: 'Fix thing', directory: '/repo',
    timeCreated: 0, timeUpdated: 1, summaryAdditions: null, summaryDeletions: null,
    summaryFiles: null, shareUrl: null, messageCount: 0, durationMs: 0,
    activeDurationMs: 0, totalInputTokens: 0, totalOutputTokens: 0, totalCost: 0,
    status: 'done', liveConnection: false, pendingPermission: false,
    pendingQuestion: false, archived: false, seen: true, pinned: false,
    pinnedAt: 0, seenTimeUpdated: 0, unreadCount: 0, ...overrides,
  };
}

function gitInfo(branch: string): GitInfo {
  return { branch, ahead: 0, behind: 0, dirty: false };
}

function renderSidebar(
  group: SidebarProjectGroup,
  infos: Record<string, GitInfo>,
  onNewSessionInDirectory: (directory: string, remoteId?: string, platform?: string) => void = vi.fn(),
) {
  return render(
    <SessionSidebar
      activeId="s"
      sidebarWidth={300}
      showArchivedRecent={false}
      setShowArchivedRecent={vi.fn()}
      loadingRecentSessions={false}
      recentSessions={group.sessions}
      sidebarProjectGroups={[group]}
      onReorderProjects={vi.fn()}
      archivingSessionIds={new Set()}
      collapsedProjectSet={new Set()}
      toggleCollapsedProject={vi.fn()}
      siblingGitInfos={infos}
      optimisticStatus="done"
      debugMode={false}
      pendingTmuxSession={null}
      pickerPos={null}
      pickerRef={{ current: null }}
      tmux={{
        available: false,
        isLocal: true,
        sessions: [],
        clients: [],
        switchSession: vi.fn(),
        findSession: vi.fn(),
        launchOpencode: vi.fn(),
      }}
      onNavigateToSession={vi.fn()}
      onArchiveSession={vi.fn()}
      onPinSession={vi.fn()}
      onClientSelect={vi.fn()}
      onNewSessionInDirectory={onNewSessionInDirectory}
      onArchiveProject={vi.fn()}
    />,
  );
}

describe('SessionSidebar', () => {
  it('shows the git branch on the session row, not the group header', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session()],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, { '/repo': gitInfo('main') });

    expect(screen.getByTitle('Current branch: main')).toHaveTextContent('main');
  });

  it('shows the host badge for a session-less remote project group', () => {
    multiHost.mockReturnValue(true);
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
      remoteId: 'abc',
      remoteName: 'Box',
    };

    renderSidebar(group, {});

    expect(screen.getByText('Box')).toBeInTheDocument();
    multiHost.mockReturnValue(false);
  });

  it('forwards the group host + platform when adding a session to a remote project', () => {
    // Regression: the "+" used to pass only the directory, so a remote
    // project group launched on the local hub instead of the remote.
    const onAdd = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/home/dries/repo',
      sessions: [],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
      remoteId: 'abc',
      remoteName: 'Box',
      platform: 'r-abc:opencode',
    };

    renderSidebar(group, {}, onAdd);

    screen.getByRole('button', { name: 'New session in dries/repo' }).click();

    expect(onAdd).toHaveBeenCalledWith('/home/dries/repo', 'abc', 'r-abc:opencode');
  });
});
