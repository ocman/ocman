// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
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
  onArchiveSession: (e: React.MouseEvent, s: Session) => void = vi.fn(),
  setShowArchivedRecent: (updater: (current: boolean) => boolean) => void = vi.fn(),
  sidebarView: 'recent' | 'projects' = 'projects',
  setSidebarView: (view: 'recent' | 'projects') => void = vi.fn(),
  onNewSession: () => void = vi.fn(),
) {
  return render(
    <SessionSidebar
      activeId="s"
      sidebarWidth={300}
      sidebarView={sidebarView}
      setSidebarView={setSidebarView}
      showArchivedRecent={false}
      setShowArchivedRecent={setShowArchivedRecent}
      loadingRecentSessions={false}
      recentSessions={group.sessions}
      sidebarProjectGroups={[group]}
      onReorderProjects={vi.fn()}
      archivingSessionIds={new Set()}
      collapsedProjectSet={new Set()}
      toggleCollapsedProject={vi.fn()}
      siblingGitInfos={infos}
      activeDisplayStatus="done"
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
      onArchiveSession={onArchiveSession}
      onPinSession={vi.fn()}
      onNewSession={onNewSession}
      onClientSelect={vi.fn()}
      onNewSessionInDirectory={onNewSessionInDirectory}
      onArchiveProject={vi.fn()}
    />,
  );
}

describe('SessionSidebar', () => {
  it('uses compact relative times', () => {
    const now = Date.now();
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [
        session({ timeUpdated: now }),
        session({ id: 's2', title: 'Older', timeUpdated: now - 86_400_000 }),
      ],
      lastUpdated: now,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {});

    expect(screen.getByText('now')).toBeInTheDocument();
    expect(screen.getByText('1d')).toBeInTheDocument();
  });

  it('renders a flat recent list with project, title, branch, and time', () => {
    const now = Date.now();
    const group: SidebarProjectGroup = {
      directory: '/workspace/repo',
      sessions: [session({ directory: '/workspace/repo', timeUpdated: now })],
      lastUpdated: now,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, { '/workspace/repo': gitInfo('main') }, vi.fn(), vi.fn(), vi.fn(), 'recent');

    const row = screen.getByText('Fix thing').closest('.session-sidebar-item');
    expect(row).toHaveClass('flat');
    expect(row).toHaveTextContent('workspace/repo');
    expect(row).toHaveTextContent('Fix thing');
    expect(row).toHaveTextContent('main');
    expect(row).toHaveTextContent('now');
  });

  it('reuses flat rows for pinned project cards', () => {
    const group: SidebarProjectGroup = {
      directory: '__pinned__',
      sessions: [session({ pinned: true })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
      isPinned: true,
    };

    renderSidebar(group, {});

    expect(screen.getByText('Fix thing').closest('.session-sidebar-item')).toHaveClass('flat');
  });

  it('shows pinned flat rows without a heading and separates normal rows', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session({ pinned: true }), session({ id: 'normal', title: 'Normal session' })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'recent');

    expect(screen.queryByText('Pinned')).not.toBeInTheDocument();
    expect(screen.getByTestId('flat-pinned-divider')).toBeInTheDocument();
    expect(screen.getByText('Normal session')).toBeInTheDocument();
  });

  it('reserves branch space while git info loads', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' },
    };
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'recent');

    expect(screen.getByText('Fix thing').closest('.session-sidebar-item'))
      .toContainElement(document.querySelector('.session-sidebar-git-slot'));
  });

  it('switches between flat and grouped views from the header', () => {
    const setSidebarView = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' },
    };
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'recent', setSidebarView);

    fireEvent.click(screen.getByRole('button', { name: 'Group sessions by project' }));

    expect(setSidebarView).toHaveBeenCalledWith('projects');
  });

  it('opens the new-session project selector from the header', () => {
    const onNewSession = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' },
    };
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'recent', vi.fn(), onNewSession);

    fireEvent.click(screen.getByRole('button', { name: 'New session' }));

    expect(onNewSession).toHaveBeenCalledOnce();
  });

  it('shows the git branch once in the directory sub-header, not per row', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session(), session({ id: 's2', title: 'Other thing' })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, { '/repo': gitInfo('main') });

    expect(screen.getAllByTitle('Current branch: main')).toHaveLength(1);
  });

  it('groups worktree sessions under their own sub-header, main checkout first', () => {
    const wt = '/parent/.worktrees/repo/feat-x';
    const group: SidebarProjectGroup = {
      directory: '/parent/repo',
      sessions: [
        session({ id: 'w1', title: 'Worktree work', directory: wt, timeUpdated: 9 }),
        session({ id: 'm1', title: 'Main work', directory: '/parent/repo', timeUpdated: 1 }),
      ],
      lastUpdated: 9,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {
      '/parent/repo': gitInfo('main'),
      [wt]: gitInfo('feat-x'),
    });

    const headers = [...document.querySelectorAll('.session-sidebar-dir-header')];
    expect(headers.map((h) => h.getAttribute('title'))).toEqual(['/parent/repo', wt]);
    expect(screen.getByTitle('Current branch: feat-x')).toBeInTheDocument();
  });

  it('dir sub-header "+" launches a new session in that directory', () => {
    const wt = '/parent/.worktrees/repo/feat-x';
    const onAdd = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/parent/repo',
      sessions: [session({ id: 'w1', directory: wt })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, { [wt]: gitInfo('feat-x') }, onAdd);

    screen.getByRole('button', { name: 'New session on feat-x' }).click();

    expect(onAdd).toHaveBeenCalledWith(wt, undefined, 'opencode');
  });

  it('falls back to the worktree slug when git info is missing', () => {
    const wt = '/parent/.worktrees/repo/feat-y';
    const group: SidebarProjectGroup = {
      directory: '/parent/repo',
      sessions: [session({ id: 'w2', directory: wt })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {});

    expect(screen.getByTitle(wt)).toHaveTextContent('feat-y');
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

  it('archives a row on middle click but not on right click', () => {
    const onArchive = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session()],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), onArchive);
    const row = screen.getByText('Fix thing').closest('.session-sidebar-item')!;

    const aux = (button: number) =>
      fireEvent(row, new MouseEvent('auxclick', { bubbles: true, button }));

    aux(2);
    expect(onArchive).not.toHaveBeenCalled();

    aux(1);
    expect(onArchive).toHaveBeenCalledTimes(1);
    expect(onArchive.mock.calls[0][1].id).toBe('s');
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

  it('forwards the local platform from the group session when adding to a local project', () => {
    // Regression: a local session-derived group has no `platform` field
    // (that is only set from a remote session), so the "+" forwarded an
    // undefined platform. handleNewSessionInDirectory then fell back to
    // the currently-open session's (possibly remote) platform, launching
    // the new local session on the wrong host.
    const onAdd = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/home/dries/other',
      sessions: [session({ directory: '/home/dries/other', platform: 'opencode' })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, onAdd);

    screen.getByRole('button', { name: 'New session in dries/other' }).click();

    expect(onAdd).toHaveBeenCalledWith('/home/dries/other', undefined, 'opencode');
  });

  it('opens filters with archived off and children on by default', () => {
    const setShowArchived = vi.fn();
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session()],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), vi.fn(), setShowArchived);
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));

    const archived = screen.getByRole('checkbox', { name: 'Show archived' });
    expect(archived).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show children' })).toBeChecked();

    fireEvent.click(archived);
    expect(setShowArchived).toHaveBeenCalledOnce();
    expect(setShowArchived.mock.calls[0][0](false)).toBe(true);
  });

  it('hides child sessions from the filter popup', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session(), session({ id: 'child', title: 'Child task', parentId: 's' })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {});
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show children' }));

    expect(screen.queryByText('Child task')).not.toBeInTheDocument();
    expect(screen.getByText('Fix thing')).toBeInTheDocument();
  });

  it('filters from the persistent fuzzy title search', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [session(), session({ id: 'other', title: 'Unrelated work' })],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {});
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), {
      target: { value: 'fxthg' },
    });

    expect(screen.getByText('Fix thing')).toBeInTheDocument();
    expect(screen.queryByText('Unrelated work')).not.toBeInTheDocument();
  });

  it('searches project paths and branches', () => {
    const group: SidebarProjectGroup = {
      directory: '/workspace/ocman',
      sessions: [
        session({ directory: '/workspace/ocman' }),
        session({ id: 'other', title: 'Other work', directory: '/workspace/elsewhere' }),
      ],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };
    renderSidebar(group, {
      '/workspace/ocman': gitInfo('feature/sidebar-search'),
      '/workspace/elsewhere': gitInfo('main'),
    }, vi.fn(), vi.fn(), vi.fn(), 'recent');
    const search = screen.getByRole('searchbox', { name: 'Search sessions' });

    fireEvent.change(search, { target: { value: 'ocman' } });
    expect(screen.getByText('Fix thing')).toBeInTheDocument();
    expect(screen.queryByText('Other work')).not.toBeInTheDocument();

    fireEvent.change(search, { target: { value: 'sidebar-search' } });
    expect(screen.getByText('Fix thing')).toBeInTheDocument();
    expect(screen.queryByText('Other work')).not.toBeInTheDocument();
  });
});
