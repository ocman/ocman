// @vitest-environment jsdom
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { UnreadBadge } from './SessionTable';
import type { Session } from '../lib/api';

function makeSession(overrides: Partial<Session> = {}): Session {
  return {
    id: 's1',
    platform: 'opencode',
    projectId: '',
    title: 'Test',
    directory: '/d',
    timeCreated: 0,
    timeUpdated: 0,
    summaryAdditions: null,
    summaryDeletions: null,
    summaryFiles: null,
    shareUrl: null,
    messageCount: 0,
    durationMs: 0,
    activeDurationMs: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCost: 0,
    status: 'done',
    liveConnection: false,
    pendingPermission: false,
    pendingQuestion: false,
    archived: false,
    seen: false,
    pinned: false,
    pinnedAt: 0,
    seenTimeUpdated: 0,
    unreadCount: 0,
    ...overrides,
  };
}

describe('UnreadBadge', () => {
  it('renders nothing when unreadCount is zero', () => {
    const { container } = render(<UnreadBadge session={makeSession({ unreadCount: 0 })} />);
    expect(container.innerHTML).toBe('');
  });

  it('renders nothing when session is fully seen even with non-zero count', () => {
    // Defensive: applySessionState should never produce this combination,
    // but the component still has to guard against it.
    const { container } = render(<UnreadBadge session={makeSession({ unreadCount: 5, seen: true })} />);
    expect(container.innerHTML).toBe('');
  });

  it('renders the unread count', () => {
    render(<UnreadBadge session={makeSession({ unreadCount: 3 })} />);
    const badge = screen.getByTestId('session-unread-badge');
    expect(badge).toHaveTextContent('3');
  });

  it('clamps display to 99+ for large counts', () => {
    render(<UnreadBadge session={makeSession({ unreadCount: 250 })} />);
    expect(screen.getByTestId('session-unread-badge')).toHaveTextContent('99+');
  });

  it('uses singular wording in the tooltip when count is 1', () => {
    render(<UnreadBadge session={makeSession({ unreadCount: 1 })} />);
    const badge = screen.getByTestId('session-unread-badge');
    expect(badge.getAttribute('title')).toContain('1 new message ');
  });

  it('uses plural wording in the tooltip otherwise', () => {
    render(<UnreadBadge session={makeSession({ unreadCount: 5 })} />);
    const badge = screen.getByTestId('session-unread-badge');
    expect(badge.getAttribute('title')).toContain('5 new messages');
  });
});
