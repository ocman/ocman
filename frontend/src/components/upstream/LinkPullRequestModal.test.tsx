// @vitest-environment jsdom
import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { renderWithQuery as render } from '../../test/renderWithQuery';
import { beforeEach, expect, it, vi } from 'vitest';
import { prWorkflowApi } from '../../lib/prWorkflowApi';
import { LinkPullRequestModal } from './LinkPullRequestModal';
import { MemoryRouter } from 'react-router-dom';

vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { change: vi.fn() } }));
const upstreams = ['origin', 'fork'].map((remote) => ({ remote, host: 'github.com', type: 'github' as const, repo: `team/${remote}` }));
const onClose = vi.fn();
const onLinked = vi.fn();
const mount = (available = upstreams) => render(<MemoryRouter><LinkPullRequestModal sessionId="s1" platform="r-box:opencode" upstreams={available} onClose={onClose} onLinked={onLinked} /></MemoryRouter>);
beforeEach(() => { vi.resetAllMocks(); vi.mocked(prWorkflowApi.change).mockResolvedValue(undefined); });

it('links the selected upstream and session owner, then notifies the pane', async () => {
  mount();
  expect(screen.getByLabelText('PR number to link')).toHaveFocus();
  expect(screen.getByRole('button', { name: 'Link PR' })).toBeDisabled();
  fireEvent.change(screen.getByLabelText('PR upstream'), { target: { value: 'fork' } });
  fireEvent.change(screen.getByLabelText('PR number to link'), { target: { value: '42' } });
  fireEvent.click(screen.getByRole('button', { name: 'Link PR' }));
  await waitFor(() => expect(onLinked).toHaveBeenCalledOnce());
  expect(prWorkflowApi.change).toHaveBeenCalledWith('s1', 'r-box:opencode', { action: 'link', number: 42, remote: 'fork' });
});

it('keeps errors and input in the modal for retry', async () => {
  vi.mocked(prWorkflowApi.change).mockRejectedValueOnce(new Error('Owner disconnected'));
  mount();
  fireEvent.change(screen.getByLabelText('PR number to link'), { target: { value: '42' } });
  fireEvent.click(screen.getByRole('button', { name: 'Link PR' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Owner disconnected');
  expect(screen.getByLabelText('PR number to link')).toHaveValue(42);
  expect(onLinked).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Link PR' }));
  await waitFor(() => expect(onLinked).toHaveBeenCalledOnce());
});

it('prevents duplicate submissions and dismissal while linking', async () => {
  let resolve!: () => void;
  vi.mocked(prWorkflowApi.change).mockReturnValue(new Promise((done) => { resolve = () => done(undefined); }));
  mount();
  fireEvent.change(screen.getByLabelText('PR number to link'), { target: { value: '42' } });
  fireEvent.click(screen.getByRole('button', { name: 'Link PR' }));
  expect(screen.getByRole('button', { name: 'Linking…' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Cancel' })).toBeDisabled();
  fireEvent.keyDown(window, { key: 'Escape' });
  expect(onClose).not.toHaveBeenCalled();
  await act(async () => resolve());
});

it('explains missing upstreams and allows cancellation without linking', () => {
  mount([]);
  expect(screen.getByText(/No supported upstream/)).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Link PR' })).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(onClose).toHaveBeenCalledOnce();
  expect(prWorkflowApi.change).not.toHaveBeenCalled();
});
