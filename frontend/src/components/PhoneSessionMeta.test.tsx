// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import { PhoneSessionMeta } from './PhoneSessionMeta';
import { useGitInfo } from '../lib/useGitInfo';

vi.mock('../lib/useGitInfo', () => ({ useGitInfo: vi.fn(() => ({ infos: {}, loading: false, error: null })) }));
vi.mock('../lib/useIsPhone', () => ({ useIsPhone: () => true }));

vi.mock('../lib/useCapabilities', () => ({ useMultiHost: () => true }));

const session = { directory: '/repos/ocman', status: 'busy', timeUpdated: Date.now(), remoteId: 'local' } as Session;

describe('phone session context', () => {
  it('shows the project and owning checkout branch together for worktrees', () => {
    const directory = '/repos/.worktrees/ocman/fix-login';
    vi.mocked(useGitInfo).mockReturnValueOnce({ infos: {
      [directory]: { branch: 'fix/login', dirty: false, ahead: 0, behind: 0 },
    }, loading: false, error: null });
    render(<PhoneSessionMeta session={{ ...session, directory, remoteId: 'build-box' }} />);
    const project = screen.getByText('ocman');
    expect(project.parentElement).toContainElement(screen.getByText('fix/login'));
    expect(screen.getByTitle('Worktree')).toHaveClass('bi-diagram-2');
    expect(useGitInfo).toHaveBeenLastCalledWith([directory], 'build-box', true);
  });

  it('identifies a regular checkout with the branch icon', () => {
    vi.mocked(useGitInfo).mockReturnValueOnce({ infos: {
      [session.directory]: { branch: 'main', dirty: false, ahead: 0, behind: 0 },
    }, loading: false, error: null });
    render(<PhoneSessionMeta session={session} />);
    expect(screen.getByTitle('Branch')).toHaveClass('bi-git');
    expect(screen.queryByTitle('Worktree')).not.toBeInTheDocument();
    expect(screen.getByText('main')).toBeInTheDocument();
  });

  it.each([
    ['busy', 'Working'], ['waiting', 'Waiting'], ['done', 'Completed'],
    ['error', 'Error'], ['interrupted', 'Interrupted'], ['limited', 'Rate limited'],
  ] as const)('does not repeat the %s status icon in text', (status, expected) => {
    const { container } = render(<PhoneSessionMeta session={{ ...session, status }} />);
    expect(screen.queryByText(expected)).not.toBeInTheDocument();
    expect(screen.getByText('ocman')).toBeInTheDocument();
    expect(screen.queryByTitle(/Current branch:/)).not.toBeInTheDocument();
    expect(container.querySelector('time')).toHaveAttribute('datetime', new Date(session.timeUpdated).toISOString());
  });

  it.each([
    [{ pendingPermission: true, pendingQuestion: true }, 'Permission needed'],
    [{ pendingQuestion: true }, 'Question for you'],
  ])('prioritizes pending requests', (pending, expected) => {
    render(<PhoneSessionMeta session={{ ...session, ...pending }} />);
    expect(screen.getByText(expected)).toHaveClass('phone-session-pending');
  });

  it('retains machine identity and handles an unknown project', () => {
    render(<PhoneSessionMeta session={{ ...session, directory: '', remoteId: 'r1', remoteName: 'Build server' }} />);
    expect(screen.getByText('Unknown project')).toBeInTheDocument();
    expect(screen.getByText('Build server')).toBeInTheDocument();
  });
});
