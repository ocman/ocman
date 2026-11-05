import { expect, it, vi } from 'vitest';
import { fetchJSON, postJSON } from './api';
import { plugins } from './plugins';

vi.mock('./api', () => ({ fetchJSON: vi.fn(), postJSON: vi.fn() }));

it('qualifies and encodes every plugin operation with the owner', () => {
  const signal = new AbortController().signal;
  plugins.list('remote/a', signal);
  expect(fetchJSON).toHaveBeenLastCalledWith('/api/plugins?ownerId=remote%2Fa', signal);
  plugins.discovery('remote/a', signal);
  expect(fetchJSON).toHaveBeenLastCalledWith('/api/plugins/discovery?ownerId=remote%2Fa', signal);
  plugins.rescan('remote/a');
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/rescan?ownerId=remote%2Fa', {});
  plugins.mutate('remote/a', 'id/b', 'disable');
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/id%2Fb/disable?ownerId=remote%2Fa', {});
  plugins.mutate('remote/a', 'id/b', 'configuration', { secrets: { token: 'write-only' } });
  expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/id%2Fb/configuration?ownerId=remote%2Fa', { secrets: { token: 'write-only' } });
  plugins.stderr('remote/a', 'id/b');
  expect(fetchJSON).toHaveBeenLastCalledWith('/api/plugins/id%2Fb/stderr?ownerId=remote%2Fa');
});

it('routes agent tool reports and confirmation decisions to their owner', () => {
  const signal = new AbortController().signal;
  plugins.agentTools('remote/a', 'id/b', signal);
  expect(fetchJSON).toHaveBeenLastCalledWith('/api/plugins/id%2Fb/tools?ownerId=remote%2Fa', signal);
  for (const decision of ['approve', 'deny'] as const) {
    plugins.decideToolConfirmation('remote/a', 'id/b', 'tc_A', decision);
    expect(postJSON).toHaveBeenLastCalledWith('/api/plugins/id%2Fb/tools/confirm?ownerId=remote%2Fa', { values: { confirmationId: 'tc_A', decision } });
  }
});
