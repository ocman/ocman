import { expect, it, vi } from 'vitest';
import { fetchJSON } from './api.requests';
import { resources } from './resources';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn().mockResolvedValue({ processes: [], history: [] }) }));

it('requests this machine explicitly and forwards cancellation', async () => {
  const signal = new AbortController().signal;
  await resources(signal);
  expect(fetchJSON).toHaveBeenCalledWith('/api/resources?remoteId=local', signal);
});
