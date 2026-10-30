import { beforeEach, expect, it, vi } from 'vitest';
import { fetchJSON, postJSON } from './api';
import { prWorkflowApi, type PRLink } from './prWorkflowApi';

vi.mock('./api', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => vi.clearAllMocks());
it('uses the hub credential routes without putting secrets in URLs', () => {
  prWorkflowApi.connections(); expect(fetchJSON).toHaveBeenCalledWith('/api/source-control');
  prWorkflowApi.recheck(); expect(postJSON).toHaveBeenCalledWith('/api/source-control/refresh', {}, { parseJSON: false });
  prWorkflowApi.saveCredential('code.example.com', 'forgejo', 'secret');
  expect(postJSON).toHaveBeenCalledWith('/api/source-control/credentials', { host: 'code.example.com', provider: 'forgejo', token: 'secret' }, { parseJSON: false });
});
it('qualifies session reads and mutations by platform and handles empty unlink responses', () => {
  prWorkflowApi.links('s/1', 'r-box:opencode'); expect(fetchJSON).toHaveBeenCalledWith('/api/session/s%2F1/pull-requests?platform=r-box%3Aopencode');
  prWorkflowApi.links('s/1', 'r-box:opencode', true); expect(fetchJSON).toHaveBeenLastCalledWith('/api/session/s%2F1/pull-requests?platform=r-box%3Aopencode&refresh=true');
  prWorkflowApi.change('s1', 'r-box:opencode', { action: 'watch', number: 42, host: 'github.com', repo: 'team/repo' });
  expect(postJSON).toHaveBeenLastCalledWith('/api/session/s1/pull-requests?platform=r-box%3Aopencode', expect.objectContaining({ action: 'watch' }), { parseJSON: true });
  prWorkflowApi.change('s1', 'r-box:opencode', { action: 'unlink', number: 42 });
  expect(postJSON).toHaveBeenLastCalledWith(expect.any(String), expect.objectContaining({ action: 'unlink' }), { parseJSON: false });
});
it('prepares feedback only for the selected linked PR', () => {
  const link = { number: 42, remote: { host: 'github.com', repo: 'team/repo' } } as PRLink;
  prWorkflowApi.feedback('s1', 'r-box:opencode', link, 'feedback');
  expect(postJSON).toHaveBeenCalledWith('/api/session/s1/pull-request-feedback?platform=r-box%3Aopencode', { action: 'feedback', number: 42, host: 'github.com', repo: 'team/repo' });
});
it('loads checks by saved repository and session identity, not checkout remote', () => {
  const signal = new AbortController().signal;
  const link = { platform: 'r-box:opencode', sessionId: 's/1', number: 42, remote: { host: 'github.com', repo: 'other/project' }, pr: { headSha: 'abcdef' } } as PRLink;
  for (const refresh of [false, true]) {
    prWorkflowApi.checks(link, signal, refresh);
    expect(fetchJSON).toHaveBeenLastCalledWith(`/api/session/s%2F1/pull-request-checks?platform=r-box%3Aopencode&host=github.com&repo=other%2Fproject&number=42&sha=abcdef${refresh ? '&refresh=true' : ''}`, signal);
  }
  prWorkflowApi.checks({ ...link, pr: { ...link.pr, headSha: undefined } }, signal);
  expect(fetchJSON).toHaveBeenLastCalledWith(expect.stringContaining('sha='), signal);
});
