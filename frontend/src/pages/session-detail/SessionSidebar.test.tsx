// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { DndContext } from '@dnd-kit/core';
import { useUiStore } from '../../lib/uiStore';
import { SessionSidebar, type SidebarProjectGroup } from './SessionSidebar';
import type { GitInfo, Session } from '../../lib/api';
import { useWorkEpics } from '../../lib/queries';
import { visibleSidebarSessions } from '../../lib/sidebarHelpers';
import { MemoryRouter } from 'react-router-dom';
import { rememberConversationDraft, useNewConversationDrafts } from '../../lib/newConversationDrafts';

vi.mock('../../components/BackendStats', () => ({
  BackendStats: () => null,
}));
vi.mock('../../components/SidebarResizer', () => ({
  SidebarResizer: () => null,
}));
vi.mock('@dnd-kit/core', async (importOriginal) => {
  const original = await importOriginal<typeof import('@dnd-kit/core')>();
  return { ...original, DndContext: vi.fn((props: React.ComponentProps<typeof original.DndContext>) => <original.DndContext {...props} />) };
});
vi.mock('../../lib/queries', () => ({
  useWorkEpics: vi.fn(),
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
  groups: SidebarProjectGroup[] = [group],
  onReorderProjects: (keys: string[]) => void = vi.fn(),
) {
  return render(
    <MemoryRouter><SessionSidebar
      activeId="s"
      sidebarWidth={300}
      sidebarView={sidebarView}
      setSidebarView={setSidebarView}
      showArchivedRecent={false}
      setShowArchivedRecent={setShowArchivedRecent}
      loadingRecentSessions={false}
      recentSessions={group.sessions}
      sidebarProjectGroups={groups}
      onReorderProjects={onReorderProjects}
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
    /></MemoryRouter>,
  );
}

describe('SessionSidebar', () => {
  beforeEach(() => {
    localStorage.clear();
    useNewConversationDrafts.setState({ drafts: [] });
    useUiStore.setState({ projectOrder: [] });
    vi.mocked(useWorkEpics).mockReturnValue({ data: [] } as never);
  });

  it('persists reordering draft-only projects and restores their saved order', () => {
    rememberConversationDraft({ draftId: 'draft', directory: '/draft-only', title: 'Draft-only task' });
    const group: SidebarProjectGroup = { directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' } };
    const reorder = vi.fn((keys: string[]) => useUiStore.getState().setProjectOrder(keys));
    const view = renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'projects', vi.fn(), vi.fn(), [group], reorder);
    const drag = vi.mocked(DndContext).mock.calls.at(-1)![0].onDragEnd!;
    act(() => drag({ active: { id: '/draft-only' }, over: { id: '/repo' } } as Parameters<typeof drag>[0]));
    expect(reorder).toHaveBeenCalledWith(['/repo', '/draft-only']);
    view.unmount();
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), 'projects', vi.fn(), vi.fn(), [group], reorder);
    const draft = screen.getByText('Draft-only task');
    const saved = screen.getByText('Fix thing');
    expect(saved.compareDocumentPosition(draft) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    const drop = vi.mocked(DndContext).mock.calls.at(-1)![0].onDragEnd!;
    act(() => drop({ active: { id: '/repo' }, over: { id: '/draft-only' } } as Parameters<typeof drop>[0]));
    expect(reorder).toHaveBeenLastCalledWith(['/draft-only', '/repo']);
  });

  it.each(['recent', 'projects'] as const)('includes drafts in the %s session list without a drafts header', (view) => {
    rememberConversationDraft({ draftId: 'draft', directory: '/repo', title: 'Prepared task' });
    const group: SidebarProjectGroup = { directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' } };
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), view);
    expect(screen.queryByText('Drafts')).not.toBeInTheDocument();
    const draft = screen.getByText('Prepared task').closest('.session-sidebar-item')!;
    const saved = screen.getByText('Fix thing').closest('.session-sidebar-item')!;
    expect(draft.compareDocumentPosition(saved) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    if (view === 'projects') expect(draft.closest('.session-sidebar-group')).toBe(saved.closest('.session-sidebar-group'));
    expect(screen.getByRole('button', { name: 'Discard draft' })).toHaveClass('session-sidebar-archive-btn');
  });

  it('groups draft-only projects by owner and includes them in the project filter', () => {
    rememberConversationDraft({ draftId: 'local', directory: '/repo', title: 'Local draft' });
    rememberConversationDraft({ draftId: 'remote', directory: '/repo', remoteId: 'box', title: 'Remote draft' });
    const group: SidebarProjectGroup = { directory: '/repo', sessions: [], lastUpdated: 1, aggregate: { kind: 'none' } };
    renderSidebar(group, {});
    expect(screen.getByText('Local draft').closest('.session-sidebar-group')).not.toBe(screen.getByText('Remote draft').closest('.session-sidebar-group'));
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.change(screen.getByRole('combobox', { name: 'Project' }), { target: { value: '/repo' } });
    expect(screen.getByText('Local draft')).toBeInTheDocument();
    expect(screen.queryByText('Remote draft')).not.toBeInTheDocument();
  });

  it.each(['recent', 'projects'] as const)('keeps matching worktree drafts in their project during %s search', (view) => {
    rememberConversationDraft({ draftId: 'draft', directory: '/src/.worktrees/repo/task', title: 'Prepared task' });
    rememberConversationDraft({ draftId: 'other', directory: '/other', title: 'Other draft' });
    const group: SidebarProjectGroup = { key: 'git:repo', directory: '/src/repo', sessions: [session({ directory: '/src/repo' })], lastUpdated: 1, aggregate: { kind: 'none' } };
    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), view);
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'Prepared' } });
    expect(screen.getByText('Prepared task')).toBeInTheDocument();
    expect(screen.queryByText('Other draft')).not.toBeInTheDocument();
    if (view === 'projects') expect(screen.getByText('Prepared task').closest('.session-sidebar-group')?.textContent).toContain('repo');
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: '/src/repo' } });
    expect(screen.getByText('Prepared task')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'missing' } });
    expect(screen.queryByText('Prepared task')).not.toBeInTheDocument();
  });

  it.each(['recent', 'projects'] as const)('filters %s sessions, including pinned and active rows, by project', (view) => {
    const selected = session({ id: 'chosen', title: 'Selected worktree', directory: '/worktree', platform: 'r-other:opencode' });
    const pinned = session({ id: 'pinned', title: 'Other pinned', pinned: true });
    const groups: SidebarProjectGroup[] = [
      { key: 'git:selected', directory: '/selected', sessions: [selected], lastUpdated: 1, aggregate: { kind: 'none' } },
      { directory: '/repo', sessions: [session(), pinned], lastUpdated: 1, aggregate: { kind: 'none' } },
      { directory: '__pinned__', sessions: [pinned], isPinned: true, lastUpdated: 1, aggregate: { kind: 'none' } },
    ];
    renderSidebar({ ...groups[0], sessions: [session(), pinned, selected] }, {}, vi.fn(), vi.fn(), vi.fn(), view, vi.fn(), vi.fn(), groups);
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    const selector = screen.getByRole('combobox', { name: 'Project' });
    expect(screen.queryByRole('option', { name: '__pinned__' })).not.toBeInTheDocument();
    fireEvent.change(selector, { target: { value: 'git:selected' } });
    expect(screen.getByText('Selected worktree')).toBeInTheDocument();
    expect(screen.queryByText('Fix thing')).not.toBeInTheDocument();
    expect(screen.queryByText('Other pinned')).not.toBeInTheDocument();
    expect(visibleSidebarSessions.current?.map((s) => s.id)).toEqual(['chosen']);
    fireEvent.change(selector, { target: { value: '' } });
    expect(screen.getAllByText('Fix thing').length).toBeGreaterThan(0);
    expect(screen.getAllByText('Other pinned').length).toBeGreaterThan(0);
  });

  it('keeps same-path checkout buttons and branch labels scoped to their owner', () => {
    const onNew = vi.fn();
    renderSidebar({ key: 'git:shared', directory: '/repo', remoteId: 'local', lastUpdated: 1,
      aggregate: { kind: 'none' }, sessions: [session(), session({ id: 'remote', remoteId: 'other', platform: 'r-other:opencode' })],
    }, {
      '["local","/repo"]': gitInfo('local-main'),
      '["other","/repo"]': gitInfo('remote-main'),
    }, onNew);
    fireEvent.click(screen.getByRole('button', { name: 'New session on remote-main' }));
    expect(onNew).toHaveBeenLastCalledWith('/repo', 'other', 'r-other:opencode');
    fireEvent.click(screen.getByRole('button', { name: 'New session in /repo' }));
    expect(onNew).toHaveBeenLastCalledWith('/repo', 'local', undefined);
  });

  it('restores filter selections after the sidebar remounts', () => {
    const group: SidebarProjectGroup = {
      directory: '/repo', sessions: [session()], lastUpdated: 1, aggregate: { kind: 'none' },
    };
    const first = renderSidebar(group, {});
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    for (const name of ['Show children', 'Show factory', 'Show routines']) {
      fireEvent.click(screen.getByRole('checkbox', { name }));
    }
    first.unmount();
    renderSidebar(group, {});
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    expect(screen.getByRole('checkbox', { name: 'Show children' })).not.toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show factory' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: 'Show routines' })).toBeChecked();
  });

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

  describe.each(['projects', 'recent'] as const)('always-visible sessions in the %s view', (sidebarView) => {
    it.each(['children', 'factory', 'factory descendants', 'routines', 'search'] as const)('keeps opened and pinned sessions despite the %s filter', (filter) => {
      const overrides: Partial<Session> = filter === 'children' ? { parentId: 'parent' }
        : filter === 'factory descendants' ? { parentId: 'factory' }
        : filter === 'routines' ? { routineId: 'rt' } : {};
      const rows = [
        session({ ...overrides, title: 'Opened task' }),
        session({ ...overrides, id: 'pin', title: 'Pinned task', pinned: true, pinnedAt: 1 }),
        session({ ...overrides, id: 'other', title: 'Hidden task' }),
      ];
      if (filter === 'factory') {
        vi.mocked(useWorkEpics).mockReturnValue({
          data: [{ attempts: rows.map(({ id, platform }) => ({ session: { id, platform } })) }],
        } as never);
      } else if (filter === 'factory descendants') {
        vi.mocked(useWorkEpics).mockReturnValue({
          data: [{ attempts: [{ session: { id: 'factory', platform: 'opencode' } }] }],
        } as never);
      }
      renderSidebar({ directory: '/repo', sessions: rows, lastUpdated: 1, aggregate: { kind: 'none' } },
        {}, vi.fn(), vi.fn(), vi.fn(), sidebarView);
      if (filter === 'children') {
        fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
        fireEvent.click(screen.getByRole('checkbox', { name: 'Show children' }));
      } else if (filter === 'search') {
        fireEvent.change(screen.getByRole('searchbox', { name: 'Search sessions' }), { target: { value: 'no-match' } });
      }
      expect(screen.getByText('Opened task')).toBeInTheDocument();
      expect(screen.getAllByText('Pinned task').length).toBeGreaterThan(0);
      expect(screen.queryByText('Hidden task')).not.toBeInTheDocument();
      expect(visibleSidebarSessions.current?.map((row) => row.id)).toEqual(['pin', 's']);
    });
  });

  it.each(['projects', 'recent'] as const)('hides Factory sessions until enabled in the %s view', (sidebarView) => {
    vi.mocked(useWorkEpics).mockReturnValue({
      data: [{ attempts: [{ session: { platform: 'opencode', id: 'factory' } }] }],
    } as never);
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [
        session(),
        session({ id: 'grandchild', title: 'Factory grandchild', parentId: 'child' }),
        session({ id: 'child', title: 'Factory child', parentId: 'factory' }),
        session({ id: 'factory', title: 'Factory task' }),
        session({ id: 'normal-child', title: 'Normal child', parentId: 's' }),
        session({ id: 'remote-child', title: 'Remote child', parentId: 'factory', platform: 'r-box:opencode' }),
      ],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), sidebarView);

    expect(screen.queryByText('Factory task')).not.toBeInTheDocument();
    expect(screen.queryByText('Factory child')).not.toBeInTheDocument();
    expect(screen.queryByText('Factory grandchild')).not.toBeInTheDocument();
    expect(screen.getByText('Normal child')).toBeInTheDocument();
    expect(screen.getByText('Remote child')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    const showFactory = screen.getByRole('checkbox', { name: 'Show factory' });
    expect(showFactory).not.toBeChecked();

    fireEvent.click(showFactory);
    expect(screen.getByText('Factory task')).toBeInTheDocument();
    expect(screen.getByText('Factory child')).toBeInTheDocument();
    expect(screen.getByText('Factory grandchild')).toBeInTheDocument();

    fireEvent.click(showFactory);
    expect(screen.queryByText('Factory child')).not.toBeInTheDocument();
    expect(screen.queryByText('Factory grandchild')).not.toBeInTheDocument();
  });

  it.each(['projects', 'recent'] as const)('hides routine sessions until enabled in the %s view', (sidebarView) => {
    vi.mocked(useWorkEpics).mockReturnValue({ data: [] } as never);
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [
        session(),
        session({ id: 'routine', title: 'Nightly check', routineId: 'rt' }),
        session({ id: 'routine-child', title: 'Routine child', parentId: 'routine' }),
      ],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), sidebarView);

    expect(screen.queryByText('Nightly check')).not.toBeInTheDocument();
    expect(screen.queryByText('Routine child')).not.toBeInTheDocument();
    expect(screen.getByText('Fix thing')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show routines' }));
    expect(screen.getByText('Nightly check')).toBeInTheDocument();
    expect(screen.getByText('Routine child')).toBeInTheDocument();
  });

  it.each(['projects', 'recent'] as const)('publishes only filtered rows, in order, as archive candidates in the %s view', (sidebarView) => {
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [
        session({ id: 'a', title: 'Alpha' }),
        session({ id: 'routine', title: 'Nightly check', routineId: 'rt' }),
        session({ id: 'b', title: 'Beta' }),
      ],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    const { unmount } = renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), sidebarView);
    expect(visibleSidebarSessions.current?.map((s) => s.id)).toEqual(['a', 'b']);

    fireEvent.click(screen.getByRole('button', { name: 'Filter sessions' }));
    fireEvent.click(screen.getByRole('checkbox', { name: 'Show routines' }));
    expect(visibleSidebarSessions.current?.map((s) => s.id)).toEqual(['a', 'routine', 'b']);

    unmount();
    expect(visibleSidebarSessions.current).toBeNull();
  });

  it('publishes a pinned session once although it also sits in its project group', () => {
    const pinned = session({ id: 'p', pinned: true, pinnedAt: 1 });
    renderSidebar({ directory: '/repo', sessions: [pinned, session({ id: 'o' })], lastUpdated: 1, aggregate: { kind: 'none' } }, {});
    expect(visibleSidebarSessions.current?.map((s) => s.id)).toEqual(['p', 'o']);
  });

  it('leaves collapsed project groups out of the archive candidates', () => {
    render(
      <MemoryRouter><SessionSidebar
        activeId="a" sidebarWidth={300} sidebarView="projects" setSidebarView={vi.fn()}
        showArchivedRecent={false} setShowArchivedRecent={vi.fn()} loadingRecentSessions={false}
        recentSessions={[session({ id: 'a' }), session({ id: 'b', directory: '/other' })]}
        sidebarProjectGroups={[
          { directory: '/repo', sessions: [session({ id: 'a' })], lastUpdated: 2, aggregate: { kind: 'none' } },
          { directory: '/other', sessions: [session({ id: 'b', directory: '/other' })], lastUpdated: 1, aggregate: { kind: 'none' } },
        ]}
        onReorderProjects={vi.fn()} archivingSessionIds={new Set()} collapsedProjectSet={new Set(['/other'])}
        toggleCollapsedProject={vi.fn()} siblingGitInfos={{}} activeDisplayStatus="done" debugMode={false}
        pendingTmuxSession={null} pickerPos={null} pickerRef={{ current: null }}
        tmux={{ available: false, isLocal: true, sessions: [], clients: [], switchSession: vi.fn(), findSession: vi.fn(), launchOpencode: vi.fn() }}
        onNavigateToSession={vi.fn()} onArchiveSession={vi.fn()} onPinSession={vi.fn()} onNewSession={vi.fn()}
        onClientSelect={vi.fn()} onNewSessionInDirectory={vi.fn()} onArchiveProject={vi.fn()}
      /></MemoryRouter>,
    );
    expect(visibleSidebarSessions.current?.map((s) => s.id)).toEqual(['a']);
  });

  it.each(['projects', 'recent'] as const)('hides Factory descendants when the attempt is absent in the %s view', (sidebarView) => {
    vi.mocked(useWorkEpics).mockReturnValue({
      data: [{ attempts: [{ session: { platform: 'opencode', id: 'factory' } }] }],
    } as never);
    const group: SidebarProjectGroup = {
      directory: '/repo',
      sessions: [
        session(),
        session({ id: 'grandchild', title: 'Factory grandchild', parentId: 'child' }),
        session({ id: 'child', title: 'Factory child', parentId: 'factory' }),
      ],
      lastUpdated: 1,
      aggregate: { kind: 'none' },
    };

    renderSidebar(group, {}, vi.fn(), vi.fn(), vi.fn(), sidebarView);

    expect(screen.queryByText('Factory child')).not.toBeInTheDocument();
    expect(screen.queryByText('Factory grandchild')).not.toBeInTheDocument();
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
