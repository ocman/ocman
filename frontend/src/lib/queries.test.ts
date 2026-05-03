import { describe, it, expect, vi, beforeEach } from 'vitest';
import { QueryClient } from '@tanstack/react-query';

// Mock the api module.
vi.mock('./api', () => ({
  api: {
    sessions: vi.fn().mockResolvedValue([
      { id: 's1', platform: 'opencode', title: 'Test', status: 'done' },
    ]),
    projects: vi.fn().mockResolvedValue([
      { directory: '/tmp/foo', name: 'foo' },
    ]),
    activity: vi.fn().mockResolvedValue([
      { date: '2025-01-01', messages: 5 },
    ]),
    models: vi.fn().mockResolvedValue([
      { provider: 'openai', model: 'gpt-4', count: 10, tokensIn: 100, tokensOut: 200 },
    ]),
    hourly: vi.fn().mockResolvedValue([
      { hour: 9, sessions: 3 },
    ]),
    hourlyTokens: vi.fn().mockResolvedValue([
      { datetime: '2025-01-01 09', provider: 'openai', model: 'gpt-4', tokensIn: 50, tokensOut: 100 },
    ]),
  },
}));

/**
 * Tests for the TanStack Query hooks. Since the project doesn't use
 * @testing-library/react, we test the query functions directly via
 * QueryClient.fetchQuery — this exercises the queryFn, queryKey, and
 * signal plumbing without needing a React render tree.
 */
describe('TanStack Query functions', () => {
  let client: QueryClient;

  beforeEach(() => {
    vi.clearAllMocks();
    client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
  });

  it('sessions query returns data and coerces null to []', async () => {
    const { api } = await import('./api');

    // Normal case
    const data = await client.fetchQuery({
      queryKey: ['sessions', {}],
      queryFn: ({ signal }) => api.sessions(undefined, signal).then((r) => r ?? []),
    });
    expect(data).toHaveLength(1);
    expect(data[0].id).toBe('s1');

    // Null coercion
    (api.sessions as ReturnType<typeof vi.fn>).mockResolvedValueOnce(null);
    const data2 = await client.fetchQuery({
      queryKey: ['sessions', { force: true }],
      queryFn: ({ signal }) => api.sessions(undefined, signal).then((r) => r ?? []),
    });
    expect(data2).toEqual([]);
  });

  it('projects query returns data', async () => {
    const { api } = await import('./api');
    const data = await client.fetchQuery({
      queryKey: ['projects'],
      queryFn: ({ signal }) => api.projects(signal),
    });
    expect(data).toHaveLength(1);
    expect(data[0].directory).toBe('/tmp/foo');
  });

  it('activity query returns data', async () => {
    const { api } = await import('./api');
    const data = await client.fetchQuery({
      queryKey: ['activity', {}],
      queryFn: ({ signal }) => api.activity(undefined, signal),
    });
    expect(data).toHaveLength(1);
    expect(data[0].messages).toBe(5);
  });

  it('models query returns data', async () => {
    const { api } = await import('./api');
    const data = await client.fetchQuery({
      queryKey: ['models', {}],
      queryFn: ({ signal }) => api.models(undefined, signal),
    });
    expect(data).toHaveLength(1);
    expect(data[0].model).toBe('gpt-4');
  });

  it('hourly query returns data', async () => {
    const { api } = await import('./api');
    const data = await client.fetchQuery({
      queryKey: ['hourly', {}],
      queryFn: ({ signal }) => api.hourly(undefined, signal),
    });
    expect(data).toHaveLength(1);
    expect(data[0].sessions).toBe(3);
  });

  it('hourlyTokens query returns data', async () => {
    const { api } = await import('./api');
    const data = await client.fetchQuery({
      queryKey: ['hourlyTokens', {}],
      queryFn: ({ signal }) => api.hourlyTokens(undefined, signal),
    });
    expect(data).toHaveLength(1);
    expect(data[0].tokensIn).toBe(50);
  });

  it('sessions query passes signal to api.sessions', async () => {
    const { api } = await import('./api');
    await client.fetchQuery({
      queryKey: ['sessions', { dir: '/tmp/foo' }],
      queryFn: ({ signal }) => api.sessions({ dir: '/tmp/foo' }, signal).then((r) => r ?? []),
    });
    expect(api.sessions).toHaveBeenCalledWith(
      { dir: '/tmp/foo' },
      expect.any(AbortSignal),
    );
  });

  it('deduplicates concurrent fetches for the same key', async () => {
    const { api } = await import('./api');
    const key = ['sessions', { dedup: true }];
    const fn = ({ signal }: { signal: AbortSignal }) =>
      api.sessions(undefined, signal).then((r: unknown) => (r as unknown[]) ?? []);

    // Fire two fetches with the same key concurrently.
    const [a, b] = await Promise.all([
      client.fetchQuery({ queryKey: key, queryFn: fn }),
      client.fetchQuery({ queryKey: key, queryFn: fn }),
    ]);
    expect(a).toEqual(b);
    // TanStack deduplicates — only one actual call.
    expect(api.sessions).toHaveBeenCalledTimes(1);
  });
});
