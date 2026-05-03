import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useNotifyStore, __resetForTests } from './useNotifyData';

// Mock the api module so we don't make real HTTP requests.
vi.mock('./api', () => ({
  api: {
    sessionsNotify: vi.fn().mockResolvedValue([
      { id: 's1', status: 'waiting', seen: false },
      { id: 's2', status: 'error', seen: false, pendingPermission: true },
    ]),
  },
}));

// Provide a minimal document stub for the visibility API used by the store.
const originalDocument = globalThis.document;
beforeEach(() => {
  // @ts-expect-error -- minimal stub for tests
  globalThis.document = {
    hidden: false,
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  };
});
afterEach(() => {
  globalThis.document = originalDocument;
});

describe('useNotifyStore', () => {
  beforeEach(() => {
    __resetForTests();
    vi.useFakeTimers();
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('starts with refCount 0 and null data', () => {
    const state = useNotifyStore.getState();
    expect(state.refCount).toBe(0);
    expect(state.data).toBeNull();
  });

  it('increments refCount on subscribe', () => {
    useNotifyStore.getState().subscribe();
    expect(useNotifyStore.getState().refCount).toBe(1);
    useNotifyStore.getState().subscribe();
    expect(useNotifyStore.getState().refCount).toBe(2);
    // Cleanup
    useNotifyStore.getState().unsubscribe();
    useNotifyStore.getState().unsubscribe();
  });

  it('decrements refCount on unsubscribe, never below 0', () => {
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().subscribe();
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(1);
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(0);
    // Extra unsubscribe should not go negative
    useNotifyStore.getState().unsubscribe();
    expect(useNotifyStore.getState().refCount).toBe(0);
  });

  it('fetches data after first subscribe', async () => {
    useNotifyStore.getState().subscribe();
    // Let the async fetch resolve
    await vi.advanceTimersByTimeAsync(0);
    const state = useNotifyStore.getState();
    expect(state.data).not.toBeNull();
    expect(state.data).toHaveLength(2);
    expect(state.lastFetched).toBeGreaterThan(0);
    // Cleanup
    useNotifyStore.getState().unsubscribe();
  });

  it('recheck triggers a fetch when consumers are active', async () => {
    useNotifyStore.getState().subscribe();
    await vi.advanceTimersByTimeAsync(0);
    const firstFetch = useNotifyStore.getState().lastFetched;

    // Advance a bit so lastFetched changes
    vi.advanceTimersByTime(100);
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().lastFetched).toBeGreaterThanOrEqual(firstFetch);

    // Cleanup
    useNotifyStore.getState().unsubscribe();
  });

  it('recheck is a no-op when no consumers are active', async () => {
    useNotifyStore.getState().recheck();
    await vi.advanceTimersByTimeAsync(0);
    expect(useNotifyStore.getState().data).toBeNull();
  });
});
