// @vitest-environment jsdom
import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { renderWithQuery as render } from '../test/renderWithQuery';
import type { Session } from '../lib/api';
import { SessionTable } from './SessionTable';
import { useIsPhone } from '../lib/useIsPhone';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: vi.fn(() => false) }));
vi.mock('../lib/useGitInfo', () => ({ useGitInfo: () => ({ infos: {} }) }));

const session = (over: Partial<Session>) => ({
  id: 'ses_1', platform: 'opencode', title: 'One prompt', directory: '/src/foo', status: 'done', seen: true,
  messageCount: 1, durationMs: 1_245_000, timeCreated: 1000, timeUpdated: 1000, ...over,
} as Session);

it('pluralises activity counts and names what is counted', () => {
  render(<MemoryRouter><SessionTable showProject={false} sessions={[
    session({}), session({ id: 'ses_2', title: 'Two prompts', messageCount: 2, durationMs: 4000 }),
  ]} /></MemoryRouter>);
  expect(screen.getByTitle('1 prompt')).toHaveTextContent('1');
  expect(screen.getByTitle('2 prompts')).toHaveTextContent('2');
  expect(screen.getByText('20m 45s')).toBeInTheDocument();
  expect(screen.queryByText(/msgs/)).not.toBeInTheDocument();
});

it('shows separate right-aligned prompt, duration and cost cells with details on hover', () => {
  render(<MemoryRouter><SessionTable showProject={false} sessions={[session({ messageCount: 1204, totalEffectiveCost: 12.468 })]} /></MemoryRouter>);
  expect(screen.getByTitle('1,204 prompts')).toHaveTextContent('1,204');
  expect(screen.getByText('$12.47')).toHaveClass('list-numeric');
  expect(screen.getByText('20m 45s').getAttribute('title')).toContain('20m 45s elapsed');
});

it('keeps an activity summary in the stacked phone row and hides it while totals load', () => {
  vi.mocked(useIsPhone).mockReturnValue(true);
  const { rerender } = render(<MemoryRouter><SessionTable showProject sessions={[session({ totalCost: 0.004 })]} /></MemoryRouter>);
  expect(document.querySelector('.phone-session-activity')).toHaveTextContent('1 prompt · 20m 45s · <$0.01');
  rerender(<MemoryRouter><SessionTable showProject sessions={[session({})]} activityPendingKeys={new Set(['opencode:ses_1'])} /></MemoryRouter>);
  expect(document.querySelector('.phone-session-activity')).toBeNull();
  vi.mocked(useIsPhone).mockReturnValue(false);
});
