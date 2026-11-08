import { expect, it, vi } from 'vitest';
import { fetchJSON } from './api.requests';
import { resources } from './resources';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn().mockResolvedValue({ processes: [], history: [] }) }));

it('requests the selected machine explicitly and forwards cancellation', async () => {
  const signal = new AbortController().signal;
  await resources('local', signal);
  expect(fetchJSON).toHaveBeenCalledWith('/api/resources?remoteId=local', signal);
  await resources('build box/1', signal);
  expect(fetchJSON).toHaveBeenCalledWith('/api/resources?remoteId=build%20box%2F1', signal);
});
