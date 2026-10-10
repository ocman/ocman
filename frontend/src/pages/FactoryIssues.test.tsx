// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { useAddFactoryIssueComment, useCancelFactoryIssueTransfer, useFactoryGraphIssues, useFactoryIssueComments, useTransferFactoryIssue, useWorkEpics } from '../lib/queries';
import { FactoryIssues, IssueDrawer } from './FactoryIssues';

vi.mock('../lib/queries', () => ({
  useWorkEpics: vi.fn(),
  useFactoryGraphIssues: vi.fn(),
	useFactoryIssueComments: vi.fn(),
	useAddFactoryIssueComment: vi.fn(),
	useTransferFactoryIssue: vi.fn(),
	useCancelFactoryIssueTransfer: vi.fn(),
}));

beforeEach(() => {
	vi.clearAllMocks();
	vi.mocked(useTransferFactoryIssue).mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
	vi.mocked(useCancelFactoryIssueTransfer).mockReturnValue({ mutate: vi.fn(), isPending: false, isError: false } as never);
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
			{ id: 'fac-1', epicId: 'epic-1', project: '/other', kind: 'task', title: 'Prepare issue data', status: 'open', createdAt: 1_700_000_000_000 },
		{ id: 'fac-42', epicId: 'epic-1', project: '/repo', kind: 'implementation', title: 'Add issue drawer', status: 'closed', description: 'Show the full ticket.', conclusion: 'Rendered and tested.', prUrl: 'https://forge.example/pulls/1', parentId: 'fac-1', blockers: [{ id: 'fac-1', epicId: 'epic-1', type: 'merge_gated', reason: 'Waiting for merge', outcome: 'open' }] },
    ],
    isLoading: false,
    isError: false,
    refetch: vi.fn(),
  }] as never);
});

it('inspects proposed tasks without querying or adding comments for uncreated issues', () => {
  render(<MemoryRouter><IssueDrawer preview issue={{ id: 'task-key', epicId: 'epic-1', project: '/repo', kind: 'implementation', title: 'Proposed task', description: 'What this task should do.', status: 'open', dependsOn: [{ id: 'plan-key', type: 'blocks' }] }} onClose={() => {}} /></MemoryRouter>);
  expect(screen.getByRole('dialog')).toHaveTextContent('What this task should do.');
  expect(screen.getByRole('dialog')).toHaveTextContent('Blocked byplan-key');
  expect(screen.queryByRole('link', { name: 'plan-key' })).not.toBeInTheDocument();
  expect(screen.queryByLabelText('Issue comments')).not.toBeInTheDocument();
  expect(useFactoryIssueComments).not.toHaveBeenCalled();
  expect(useAddFactoryIssueComment).not.toHaveBeenCalled();
});

it('shows a ticket list and opens issue details in a drawer', async () => {
  const user = userEvent.setup();
  render(<MemoryRouter initialEntries={['/factory/issues']}><Routes><Route path="/factory/issues/:issueId?" element={<FactoryIssues />} /></Routes></MemoryRouter>);

	expect(screen.getByRole('region', { name: 'Open issues' })).toBeInTheDocument();
	expect(screen.getByLabelText('task issue')).toBeInTheDocument();
	expect(screen.getByRole('link', { name: '#fac-1' })).toHaveAttribute('href', '/factory/epics/epic-1');
	expect(screen.getByTestId('cell-project')).toHaveAttribute('href', '/project/%2Fother');
	expect(screen.getByRole('link', { name: 'Ship Factory' })).toHaveAttribute('href', '/factory/epics/epic-1');
	expect(screen.getByTitle('/other')).toBeInTheDocument();
	expect(screen.getByText(/created/).closest('time')).toHaveAttribute('datetime', '2023-11-14T22:13:20.000Z');
	expect(screen.queryByText('#fac-42')).not.toBeInTheDocument();
	expect(screen.getByText('1 shown · 1 closed hidden')).toBeInTheDocument();
	 expect(screen.queryByText('Show the full ticket.')).not.toBeInTheDocument();
	await user.type(screen.getByLabelText('Find issues'), 'prida');
	expect(screen.getByText('Prepare issue data')).toBeInTheDocument();
	await user.clear(screen.getByLabelText('Find issues'));
	await user.selectOptions(screen.getByLabelText('Issue status'), 'all');
	expect(screen.getByRole('region', { name: 'Closed issues' })).toBeInTheDocument();
	expect(screen.getByText('#fac-42')).toBeInTheDocument();
	expect(screen.getByText('2 shown · 1 closed')).toBeInTheDocument();
	await user.selectOptions(screen.getByLabelText('Issue status'), 'closed');
	expect(screen.queryByText('#fac-1')).not.toBeInTheDocument();
	await user.selectOptions(screen.getByLabelText('Issue type'), 'implementation');
	expect(screen.queryByText('#fac-1')).not.toBeInTheDocument();

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
	expect(drawer).toHaveTextContent('merge gate on fac-1: Waiting for merge');
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
}, 10_000);

it('transfers an open ticket to the issue tracker after confirmation', async () => {
	const user = userEvent.setup();
	const mutate = vi.fn();
	vi.mocked(useTransferFactoryIssue).mockReturnValue({ mutate, isPending: false, isError: false } as never);
	const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
	const issue = { id: 'fac-7', epicId: 'epic-1', project: '/repo', kind: 'task', title: 'Cover gap', status: 'open' };
	const { rerender } = render(<MemoryRouter><IssueDrawer issue={issue} onClose={() => {}} /></MemoryRouter>);
	await user.click(screen.getByRole('button', { name: 'Transfer to issue tracker' }));
	expect(mutate).not.toHaveBeenCalled();
	await user.click(screen.getByRole('button', { name: 'Transfer to issue tracker' }));
	expect(mutate).toHaveBeenCalledWith({ epicId: 'epic-1', issueId: 'fac-7' });
	vi.mocked(useTransferFactoryIssue).mockReturnValue({ mutate, isPending: false, isError: true, error: new Error('no supported issue tracker') } as never);
	rerender(<MemoryRouter><IssueDrawer issue={issue} onClose={() => {}} /></MemoryRouter>);
	expect(screen.getByRole('alert')).toHaveTextContent('no supported issue tracker');
	vi.mocked(useTransferFactoryIssue).mockReturnValue({ mutate, data: { url: 'https://forge.example/issues/3' }, isPending: false, isError: false } as never);
	rerender(<MemoryRouter><IssueDrawer issue={issue} onClose={() => {}} /></MemoryRouter>);
	expect(screen.getByRole('link', { name: 'https://forge.example/issues/3' })).toBeInTheDocument();
	rerender(<MemoryRouter><IssueDrawer issue={{ ...issue, status: 'in_progress' }} onClose={() => {}} /></MemoryRouter>);
	expect(screen.queryByRole('button', { name: 'Transfer to issue tracker' })).not.toBeInTheDocument();
	confirm.mockRestore();
});

it('resumes a recorded transfer and cancels an uncertain one after a reload', async () => {
	const user = userEvent.setup();
	const transfer = vi.fn();
	const cancel = vi.fn();
	vi.mocked(useTransferFactoryIssue).mockReturnValue({ mutate: transfer, isPending: false, isError: false } as never);
	vi.mocked(useCancelFactoryIssueTransfer).mockReturnValue({ mutate: cancel, isPending: false, isError: false } as never);
	const confirm = vi.spyOn(window, 'confirm');
	const base = { id: 'fac-8', epicId: 'epic-1', project: '/repo', kind: 'task', title: 'Held', status: 'deferred' };
	const { rerender } = render(<MemoryRouter><IssueDrawer issue={{ ...base, outcomeReason: 'Transferred to https://forge.example/issues/4' }} onClose={() => {}} /></MemoryRouter>);
	await user.click(screen.getByRole('button', { name: 'Resume transfer' }));
	expect(transfer).toHaveBeenCalledWith({ epicId: 'epic-1', issueId: 'fac-8' });
	expect(confirm).not.toHaveBeenCalled();
	rerender(<MemoryRouter><IssueDrawer issue={{ ...base, outcomeReason: 'Transferring to the issue tracker' }} onClose={() => {}} /></MemoryRouter>);
	expect(screen.queryByRole('button', { name: /transfer to issue tracker|resume transfer/i })).not.toBeInTheDocument();
	await user.click(screen.getByRole('button', { name: 'Cancel transfer' }));
	expect(cancel).toHaveBeenCalledWith({ epicId: 'epic-1', issueId: 'fac-8' });
	rerender(<MemoryRouter><IssueDrawer issue={{ ...base, outcomeReason: 'Skip optional project' }} onClose={() => {}} /></MemoryRouter>);
	expect(screen.queryByRole('button', { name: /transfer/i })).not.toBeInTheDocument();
	confirm.mockRestore();
});
