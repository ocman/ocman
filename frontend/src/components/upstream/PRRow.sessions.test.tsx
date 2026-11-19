// @vitest-environment jsdom
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it } from 'vitest';
import { PRRow } from './PRRow';
import type { PR } from '../../lib/upstreamApi';

const pr: PR = {
  number: 42, title: 'Improve setup', body: '', author: 'dries', status: 'open',
  updatedAt: '', labels: null, assignees: null, requestedReviewers: null,
  branch: 'feature', url: 'https://code.example/team/repo/pulls/42',
  host: 'code.example', repo: 'team/repo', crossFork: false,
};

function Location() {
  const location = useLocation();
  return <output aria-label="Location">{location.pathname}{location.search}</output>;
}

function mount(sessions?: PR['sessions']) {
  render(<MemoryRouter><PRRow pr={{ ...pr, sessions }} directory="/repo" remoteId="box" remote="origin" /><Location /></MemoryRouter>);
}

describe('PR sidebar linked sessions', () => {
  it('opens the owner-qualified session without expanding the PR', () => {
    mount([{ platform: 'r-box:claude', sessionId: 'session /one' }]);
    const link = screen.getByRole('link', { name: 'Open session' });
    expect(link).toHaveAttribute('href', '/session/session%20%2Fone?platform=r-box%3Aclaude');
    expect(screen.getByRole('button', { expanded: false })).toBeInTheDocument();
    fireEvent.click(link, { ctrlKey: true });
    expect(screen.getByLabelText('Location').textContent).toBe('/');
    fireEvent.click(link);
    expect(screen.getByLabelText('Location')).toHaveTextContent('/session/session%20%2Fone?platform=r-box%3Aclaude');
    expect(screen.queryByTestId('pr-detail-42')).not.toBeInTheDocument();
  });

  it('shows every associated session when more than one is linked', () => {
    mount([{ platform: 'opencode', sessionId: 'one' }, { platform: 'claude', sessionId: 'two' }]);
    expect(screen.getByRole('link', { name: 'Open session one' })).toHaveAttribute('href', '/session/one?platform=opencode');
    expect(screen.getByRole('link', { name: 'Open session two' })).toHaveAttribute('href', '/session/two?platform=claude');
  });

  it.each([undefined, []])('does not offer a session link without an association: %s', sessions => {
    mount(sessions);
    expect(screen.queryByRole('link', { name: /Open session/ })).not.toBeInTheDocument();
  });
});
