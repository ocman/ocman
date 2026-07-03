// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project, Session } from '../lib/api';

const mocks = vi.hoisted(() => ({
  apiState: {
    archiveSession: vi.fn(async () => ({ ok: true })),
    archiveProject: vi.fn(async () => ({ ok: true })),
  },
}));

vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: typeof mocks.apiState) => unknown) => selector(mocks.apiState),
}));

vi.mock('../lib/useCapabilities', () => ({
  useMultiHost: () => true,
}));

import { GroupedSessionTable } from './SessionTable';

function makeSession(over: Partial<Session>): Session {
  return {
    id: 'ses_1',
    platform: 'opencode',
    title: 'Work',
    directory: '/src/foo',
    status: 'done',
    seen: true,
    messageCount: 3,
    durationMs: 1000,
    timeCreated: 1000,
    timeUpdated: 1000,
    ...over,
  } as Session;
}

function renderGrouped(props: Partial<React.ComponentProps<typeof GroupedSessionTable>>) {
  return render(
    <MemoryRouter>
      <GroupedSessionTable
        sessions={props.sessions ?? []}
        collapsedProjects={new Set<string>()}
        toggleCollapsedProject={() => {}}
        {...props}
      />
    </MemoryRouter>,
  );
}

describe('GroupedSessionTable project archive', () => {
  beforeEach(() => vi.clearAllMocks());

  it('renders an "Add session" placeholder for a known project with no sessions', () => {
    const onAddSession = vi.fn();
    // A real session in one project + a second known project with none:
    // placeholders only render alongside at least one session group.
    const sessions = [makeSession({ id: 's1', directory: '/src/foo' })];
    const projects: Project[] = [
      { directory: '/src/foo', sessionCount: 1, messageCount: 1, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 1000 },
      { directory: '/src/empty', sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 500 },
    ];
    renderGrouped({ sessions, projects, onAddSession });

    const add = screen.getByRole('button', { name: /Add session/i });
    fireEvent.click(add);
    expect(onAddSession).toHaveBeenCalledWith('/src/empty');
  });

  it('suppresses placeholders when showEmptyProjects is false (dashboard)', () => {
    // Dashboard Sessions tab: only projects with a session in the
    // selected time window show; known-but-empty projects don't.
    const sessions = [makeSession({ id: 's1', directory: '/src/foo' })];
    const projects: Project[] = [
      { directory: '/src/foo', sessionCount: 1, messageCount: 1, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 1000 },
      { directory: '/src/empty', sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 500 },
    ];
    renderGrouped({ sessions, projects, onAddSession: vi.fn(), showEmptyProjects: false });

    expect(screen.getByText('Work')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Add session/i })).toBeNull();
  });

  it('shows the empty state (no placeholders) when there are no sessions', () => {
    // Regression: an empty session list must fall through to the plain
    // "No sessions found" state even when projects exist, rather than
    // rendering project placeholders. (Broke dashboard e2e empty-state.)
    const projects: Project[] = [
      { directory: '/src/empty', sessionCount: 0, messageCount: 0, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 500 },
    ];
    renderGrouped({ sessions: [], projects, includeArchived: true, onAddSession: vi.fn() });

    expect(screen.getByText(/No sessions found/)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Add session/i })).toBeNull();
  });

  it('hides archived projects unless includeArchived', () => {
    const sessions = [makeSession({ id: 's1', directory: '/src/foo' })];
    const projects: Project[] = [
      { directory: '/src/foo', sessionCount: 1, messageCount: 1, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 1000, archived: true },
    ];
    const { rerender } = renderGrouped({ sessions, projects, includeArchived: false });
    expect(screen.queryByText('Work')).toBeNull();

    rerender(
      <MemoryRouter>
        <GroupedSessionTable
          sessions={sessions}
          projects={projects}
          includeArchived
          collapsedProjects={new Set<string>()}
          toggleCollapsedProject={() => {}}
        />
      </MemoryRouter>,
    );
    expect(screen.getByText('Work')).toBeTruthy();
  });

  it('archives a project via the header menu', () => {
    const sessions = [makeSession({ id: 's1', directory: '/src/foo' })];
    const { container } = renderGrouped({ sessions });

    // Open the native <details> menu, then click the archive item.
    const details = container.querySelector('details.oc-project-menu') as HTMLDetailsElement;
    details.open = true;
    fireEvent.click(screen.getByText('Archive project'));
    expect(mocks.apiState.archiveProject).toHaveBeenCalledWith('/src/foo', true);
  });

  it('folds worktree sessions to the repo root group', () => {
    const sessions = [
      makeSession({ id: 's1', directory: '/src/foo' }),
      makeSession({ id: 's2', directory: '/src/.worktrees/foo/feat', title: 'Feature' }),
    ];
    renderGrouped({ sessions });
    // Both sessions land under one project group -> count badge "2".
    expect(screen.getByText('2')).toBeTruthy();
  });

  it('prefixes remote project groups with the remote name', () => {
    const sessions = [
      makeSession({ id: 's1', remoteId: 'r1', remoteName: 'Laptop', directory: '/src/foo' }),
    ];
    renderGrouped({ sessions });
    expect(screen.getByText('Laptop')).toBeTruthy();
  });
});
