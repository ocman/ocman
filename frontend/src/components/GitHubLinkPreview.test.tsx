// @vitest-environment jsdom

import { render, screen, act } from '@testing-library/react';
import { expect, it, vi, afterEach } from 'vitest';

// IntersectionObserver isn't implemented by jsdom; capture the callbacks so
// the test can drive "card entered the viewport".
type IOEntries = { isIntersecting: boolean }[];
const ioCallbacks: ((entries: IOEntries) => void)[] = [];

class FakeIntersectionObserver {
  constructor(cb: (entries: IOEntries) => void) {
    ioCallbacks.push(cb);
  }
  observe() { /* noop */ }
  unobserve() { /* noop */ }
  disconnect() { /* noop */ }
}

afterEach(() => {
  ioCallbacks.length = 0;
  vi.useRealTimers();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function jsonResponse(body: unknown, ok = true, status = 200) {
  return { ok, status, json: async () => body } as Response;
}

// flush lets pending promise chains settle inside act().
async function flush() {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
}

it('issues one backend request per refresh cycle for N cards of the same URL', async () => {
  vi.useFakeTimers();
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  vi.resetModules();

  let previewOk = true;
  const fetchMock = vi.fn().mockImplementation((url: string) => {
    if (url.startsWith('/api/integrations/status')) {
      return Promise.resolve(jsonResponse({ forgejo: { available: false, hosts: [] } }));
    }
    if (!previewOk) return Promise.resolve(jsonResponse({}, false, 502));
    return Promise.resolve(jsonResponse({ title: 'Shared PR', state: 'open' }));
  });
  vi.stubGlobal('fetch', fetchMock);

  const previewCalls = () =>
    fetchMock.mock.calls.filter(([u]) => String(u).includes('/preview')).length;

  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  const text = 'look at https://github.com/o/r/pull/1 please';

  render(
    <>
      <LinkPreviewStrip text={text} />
      <LinkPreviewStrip text={text} />
      <LinkPreviewStrip text={text} />
    </>,
  );
  await flush();

  expect(screen.getAllByTestId('gh-preview-card')).toHaveLength(3);
  expect(previewCalls()).toBe(1);

  // Entering the viewport refreshes immediately but must not duplicate the
  // mount request.
  await act(async () => {
    for (const cb of ioCallbacks) cb([{ isIntersecting: true }]);
  });
  await flush();
  expect(previewCalls()).toBe(1);

  // One 5 s cadence tick across all three cards = one backend request.
  await act(async () => {
    vi.advanceTimersByTime(5_000);
  });
  await flush();
  expect(previewCalls()).toBe(2);

  // A failed cycle keeps the previously successful card rendered...
  previewOk = false;
  await act(async () => {
    vi.advanceTimersByTime(5_000);
  });
  await flush();
  expect(previewCalls()).toBe(3);
  expect(screen.getAllByTestId('gh-preview-card')).toHaveLength(3);
  expect(screen.getAllByText('#1 Shared PR')).toHaveLength(3);

  // ...and the next cycle recovers without a reload.
  previewOk = true;
  await act(async () => {
    vi.advanceTimersByTime(5_000);
  });
  await flush();
  expect(previewCalls()).toBe(4);
  expect(screen.getAllByText('#1 Shared PR')).toHaveLength(3);
});

it('renders one card for URLs that identify the same resource', async () => {
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  vi.resetModules();
  vi.stubGlobal('fetch', vi.fn().mockImplementation((url: string) => {
    if (url.startsWith('/api/integrations/status')) {
      return Promise.resolve(jsonResponse({ forgejo: { available: false, hosts: [] } }));
    }
    return Promise.resolve(jsonResponse({ title: 'Shared PR', state: 'open' }));
  }));

  const { LinkPreviewStrip } = await import('./GitHubLinkPreview');
  render(<LinkPreviewStrip text={'https://github.com/o/r/pull/7 https://github.com/o/r/pull/7/files'} />);
  await flush();

  expect(screen.getAllByTestId('gh-preview-card')).toHaveLength(1);
});
