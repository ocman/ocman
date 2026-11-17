// @vitest-environment jsdom
import { screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { renderWithQuery as render } from '../test/renderWithQuery';
import type { Session } from '../lib/api';
import { SessionTable } from './SessionTable';

vi.mock('../lib/useIsPhone', () => ({ useIsPhone: () => false }));

const session = (over: Partial<Session>) => ({
  id: 'ses_1', platform: 'opencode', title: 'One prompt', directory: '/src/foo', status: 'done', seen: true,
  messageCount: 1, durationMs: 1_245_000, timeCreated: 1000, timeUpdated: 1000, ...over,
} as Session);

it('pluralises activity counts and names what is counted', () => {
  render(<MemoryRouter><SessionTable showProject={false} sessions={[
    session({}), session({ id: 'ses_2', title: 'Two prompts', messageCount: 2, durationMs: 4000 }),
  ]} /></MemoryRouter>);
  expect(screen.getByText(/^1 prompt\b/)).toHaveTextContent('1 prompt · 20m 45s');
  expect(screen.getByText(/^2 prompts/)).toHaveTextContent('2 prompts · 4s');
  expect(screen.queryByText(/msgs/)).not.toBeInTheDocument();
});
