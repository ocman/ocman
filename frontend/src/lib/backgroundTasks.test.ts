import { describe, expect, it, vi } from 'vitest';
import { backgroundTask, backgroundTasks } from './backgroundTasks';
import { fetchJSON } from './api.requests';

vi.mock('./api.requests', async (original) => ({
  ...await original<typeof import('./api.requests')>(), fetchJSON: vi.fn().mockResolvedValue([]),
}));

describe('background task requests', () => {
  it('uses bounded reads, optional filters and encoded identities with cancellation', async () => {
    const signal = new AbortController().signal;
    await backgroundTasks('', signal);
    expect(fetchJSON).toHaveBeenLastCalledWith('/api/background-tasks?limit=200', signal);
    await backgroundTasks('failed', signal);
    expect(fetchJSON).toHaveBeenLastCalledWith('/api/background-tasks?status=failed&limit=200', signal);
    await backgroundTask('task/id', signal);
    expect(fetchJSON).toHaveBeenLastCalledWith('/api/background-tasks/task%2Fid', signal);
  });
});
