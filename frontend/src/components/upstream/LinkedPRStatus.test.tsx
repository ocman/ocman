// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { LinkedPRStatus } from './LinkedPRStatus';
import { usePRChecks } from '../../lib/usePRChecks';
import { prWorkflowApi, type PRLink } from '../../lib/prWorkflowApi';

vi.mock('../../lib/usePRChecks', async (original) => ({ ...await original<typeof import('../../lib/usePRChecks')>(), usePRChecks: vi.fn() }));
vi.mock('../../lib/prWorkflowApi', () => ({ prWorkflowApi: { checks: vi.fn() } }));
const link = { platform: 'r-box:opencode', sessionId: 's1', remote: { host: 'code.example.com', repo: 'other/repo', remote: '' }, remoteId: 'box', directory: '/repo', number: 42, watching: true, pr: { status: 'open', headSha: 'abcdef' } } as PRLink;
beforeEach(() => { vi.mocked(usePRChecks).mockReturnValue({ state: 'failure', checks: [], loaded: true, loading: false, error: false }); });
it.each(['open', 'draft', 'merged', 'closed'] as const)('uses the shared %s badge and a colored check indicator', (status) => {
  render(<LinkedPRStatus link={{ ...link, pr: { ...link.pr, status } }} />);
  expect(screen.getByText(status)).toHaveClass(`oc-upstream-status-${status}`);
  expect(screen.getByRole('img', { name: 'Checks failed' })).toHaveClass('oc-upstream-ci-dot-failure');
  expect(screen.getByText('Watching', { exact: true })).toHaveClass('watching');
});
it('reuses the owner-qualified shared check query and supports missing SHAs', () => {
  const { rerender } = render(<LinkedPRStatus link={link} />);
  const load = vi.mocked(usePRChecks).mock.calls.at(-1)![3];
  const controller = new AbortController(); load(controller.signal, true);
  expect(prWorkflowApi.checks).toHaveBeenCalledWith(link, controller.signal, true);
  rerender(<LinkedPRStatus link={{ ...link, watching: false, pr: { ...link.pr, headSha: undefined } }} />);
  expect(vi.mocked(usePRChecks).mock.calls.at(-1)![2]).toBe(false);
  expect(screen.getByText('Not watching')).not.toHaveClass('watching');
});
it('does not turn an unreadable check result into a red failure', () => {
  vi.mocked(usePRChecks).mockReturnValue({ state: 'failure', checks: [], loaded: true, loading: false, error: true }); render(<LinkedPRStatus link={link} />);
  expect(screen.getByRole('img', { name: 'Could not load checks' })).toHaveClass('oc-upstream-ci-dot-unknown');
});
it('names loading checks without implying they passed', () => {
  vi.mocked(usePRChecks).mockReturnValue({ state: 'unknown', checks: [], loaded: false, loading: true, error: false }); render(<LinkedPRStatus link={link} />);
  expect(screen.getByRole('img', { name: 'Loading checks…' })).toBeInTheDocument();
});
it('offers merge only for an open, mergeable PR with green checks and disables duplicate submissions', () => {
  vi.mocked(usePRChecks).mockReturnValue({ state: 'success', checks: [], loaded: true, loading: false, error: false });
  const onMerge = vi.fn();
  const ready = { ...link, pr: { ...link.pr, mergeable: true } };
  const { rerender } = render(<LinkedPRStatus link={ready} onMerge={onMerge} />);
  fireEvent.click(screen.getByRole('button', { name: 'Merge' }));
  expect(onMerge).toHaveBeenCalledOnce();
  rerender(<LinkedPRStatus link={ready} busy onMerge={onMerge} />);
  expect(screen.getByRole('button', { name: 'Merge' })).toBeDisabled();
  for (const pr of [{ ...ready.pr, status: 'draft' as const }, { ...ready.pr, status: 'closed' as const }, { ...ready.pr, status: 'merged' as const }, { ...ready.pr, mergeable: false }, { ...ready.pr, mergeable: undefined }, { ...ready.pr, headSha: undefined }]) {
    rerender(<LinkedPRStatus link={{ ...ready, pr }} onMerge={onMerge} />);
    expect(screen.queryByRole('button', { name: 'Merge' })).not.toBeInTheDocument();
  }
});
it.each([
  { state: 'failure' as const, loaded: true, error: false },
  { state: 'pending' as const, loaded: true, error: false },
  { state: 'unknown' as const, loaded: true, error: false },
  { state: 'success' as const, loaded: false, error: false },
  { state: 'success' as const, loaded: true, error: true },
])('hides merge for unverified checks %j', (checks) => {
  vi.mocked(usePRChecks).mockReturnValue({ ...checks, checks: [], loading: false });
  render(<LinkedPRStatus link={{ ...link, pr: { ...link.pr, mergeable: true } }} onMerge={vi.fn()} />);
  expect(screen.queryByRole('button', { name: 'Merge' })).not.toBeInTheDocument();
});
