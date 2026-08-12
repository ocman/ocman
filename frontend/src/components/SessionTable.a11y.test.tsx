// @vitest-environment jsdom
//
// Keyboard reachability of session rows. The row-wide onClick is a
// mouse-only affordance, so the primary cell has to carry a real link
// that can be tabbed to, announced as a link, and activated with Enter.
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Session } from '../lib/api';

const mocks = vi.hoisted(() => ({
  apiState: {
    archiveSession: vi.fn(async () => ({ ok: true })),
    archiveProject: vi.fn(async () => ({ ok: true })),
  },
}));

vi.mock('../lib/apiStore', () => ({
  useApiStore: (selector: (state: typeof mocks.apiState) => unknown) => selector(mocks.apiState),
}));

import { GroupedSessionTable, SessionTable } from './SessionTable';

function makeSession(over: Partial<Session> = {}): Session {
  return {
    id: 'ses_1',
    platform: 'opencode',
    title: 'Fix the login bug',
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

function Location() {
  return <div data-testid="location">{useLocation().pathname}</div>;
}

describe('session rows are keyboard reachable', () => {
  beforeEach(() => vi.clearAllMocks());

  it('exposes the flat table row as a link and opens it with Enter', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <SessionTable sessions={[makeSession()]} showProject={false} includeArchived />
        <Location />
      </MemoryRouter>,
    );

    const link = screen.getByRole('link', { name: /Fix the login bug/ });
    expect(link).toHaveAttribute('href', '/session/ses_1');

    await user.tab();
    expect(link).toHaveFocus();

    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/ses_1');
  });

  it('exposes the grouped table row as a link and opens it with Enter', async () => {
    const user = userEvent.setup();
    render(
      <MemoryRouter>
        <GroupedSessionTable
          sessions={[makeSession({ id: 'ses_2', title: 'Refactor auth' })]}
          includeArchived
          collapsedProjects={new Set<string>()}
          toggleCollapsedProject={() => {}}
        />
        <Location />
      </MemoryRouter>,
    );

    const link = screen.getByRole('link', { name: /Refactor auth/ });
    expect(link).toHaveAttribute('href', '/session/ses_2');

    link.focus();
    await user.keyboard('{Enter}');
    expect(screen.getByTestId('location')).toHaveTextContent('/session/ses_2');
  });
});
