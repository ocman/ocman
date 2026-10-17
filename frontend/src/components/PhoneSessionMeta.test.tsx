// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';
import { PhoneSessionMeta } from './PhoneSessionMeta';

vi.mock('../lib/useCapabilities', () => ({ useMultiHost: () => true }));

const session = { directory: '/repos/ocman', status: 'busy', timeUpdated: Date.now(), remoteId: 'local' } as Session;

describe('phone session context', () => {
  it.each([
    ['busy', 'Working'], ['waiting', 'Waiting'], ['done', 'Completed'],
    ['error', 'Error'], ['interrupted', 'Interrupted'], ['limited', 'Rate limited'],
  ] as const)('labels %s', (status, expected) => {
    const { container } = render(<PhoneSessionMeta session={{ ...session, status }} />);
    expect(screen.getByText(expected)).toBeInTheDocument();
    expect(screen.getByText('ocman')).toBeInTheDocument();
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
