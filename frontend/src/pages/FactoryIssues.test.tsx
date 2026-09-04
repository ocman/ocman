// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { useAddFactoryIssueComment, useFactoryGraphIssues, useFactoryIssueComments, useWorkEpics } from '../lib/queries';
import { FactoryIssues } from './FactoryIssues';

vi.mock('../lib/queries', () => ({
  useWorkEpics: vi.fn(),
  useFactoryGraphIssues: vi.fn(),
	useFactoryIssueComments: vi.fn(),
	useAddFactoryIssueComment: vi.fn(),
}));

beforeEach(() => {
	vi.mocked(useFactoryIssueComments).mockReturnValue({ data: [{ id: 1, issueId: 'fac-42', actor: 'mcp', body: 'Ready for review.', createdAt: 1000 }], isLoading: false, isError: false } as never);
	vi.mocked(useAddFactoryIssueComment).mockReturnValue({ mutate: vi.fn((_body, options) => options?.onSuccess?.(undefined as never, '' as never, undefined as never, undefined)), isPending: false, isError: false } as never);
  vi.mocked(useWorkEpics).mockReturnValue({
    data: [{ id: 'epic-1', goal: 'Ship Factory', status: 'open', initialProject: '/repo' }],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  } as never);
  vi.mocked(useFactoryGraphIssues).mockReturnValue([{
    data: [
      { id: 'fac-1', epicId: 'epic-1', kind: 'task', title: 'Prepare issue data', status: 'open' },
		{ id: 'fac-42', epicId: 'epic-1', kind: 'implementation', title: 'Add issue drawer', status: 'closed', description: 'Show the full ticket.', conclusion: 'Rendered and tested.', prUrl: 'https://forge.example/pulls/1', parentId: 'fac-1', blockers: [{ id: 'fac-1', epicId: 'epic-1', reason: '', outcome: '' }] },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }] as never);
});

it('shows a ticket list and opens issue details in a drawer', async () => {
  const user = userEvent.setup();
  render(<MemoryRouter initialEntries={['/factory/issues']}><Routes><Route path="/factory/issues/:issueId?" element={<FactoryIssues />} /></Routes></MemoryRouter>);

  expect(screen.getByText('fac-42')).toBeInTheDocument();
	expect(screen.getByText('closed')).toBeInTheDocument();
  expect(screen.queryByText('Show the full ticket.')).not.toBeInTheDocument();

  await user.click(screen.getByRole('button', { name: 'Open issue fac-42' }));

  const drawer = screen.getByRole('dialog', { name: 'Issue fac-42' });
  expect(drawer).toHaveTextContent('Add issue drawer');
	expect(drawer).toHaveTextContent('Show the full ticket.');
	expect(drawer).toHaveTextContent('ConclusionRendered and tested.');
	expect(screen.getByRole('link', { name: 'https://forge.example/pulls/1' })).toHaveAttribute('href', 'https://forge.example/pulls/1');
	expect(drawer).toHaveTextContent('mcp');
	expect(drawer).toHaveTextContent('Ready for review.');
  expect(drawer).toHaveTextContent('Parentfac-1');
  expect(drawer).toHaveTextContent('Blocked by');
  const blocker = screen.getByRole('link', { name: 'fac-1' });
	expect(blocker).toHaveAttribute('href', '/factory/issues/fac-1');
	await user.type(screen.getByLabelText('Add comment'), 'Looks good');
	await user.click(screen.getByRole('button', { name: 'Add comment' }));
	expect(vi.mocked(useAddFactoryIssueComment).mock.results[0].value.mutate).toHaveBeenCalledWith('Looks good', expect.anything());
	expect(screen.getByRole('status')).toHaveTextContent('Comment added.');
	expect(screen.getByLabelText('Add comment')).toHaveValue('');

  await user.click(blocker);
	const unblockedDrawer = screen.getByRole('dialog', { name: 'Issue fac-1' });
	expect(unblockedDrawer).toHaveTextContent('Prepare issue data');
	expect(unblockedDrawer).toHaveTextContent('Blocked bynone');
	expect(screen.getByLabelText('Add comment')).toHaveValue('');

  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
