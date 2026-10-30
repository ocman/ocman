// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
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
  expect(screen.getByRole('img', { name: 'Some checks failed' })).toHaveClass('oc-upstream-ci-dot-failure');
  expect(screen.getByText('Watching in background')).toHaveClass('watching');
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
