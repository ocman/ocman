// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, renderHook } from '@testing-library/react';
import { useRef } from 'react';
import { useHistoryPagination } from './useHistoryPagination';

const observers: FakeIntersectionObserver[] = [];
const resizeObservers: FakeResizeObserver[] = [];
class FakeResizeObserver {
  observe = vi.fn();
  disconnect = vi.fn();
  callback: () => void;
  constructor(callback: () => void) {
    this.callback = callback;
    resizeObservers.push(this);
  }
  emit() { this.callback(); }
}
class FakeIntersectionObserver {
  target: Element | null = null;
  disconnect = vi.fn();
  callback: IntersectionObserverCallback;
  options: IntersectionObserverInit;
  constructor(callback: IntersectionObserverCallback, options: IntersectionObserverInit) {
    this.callback = callback;
    this.options = options;
    observers.push(this);
  }
  observe(target: Element) { this.target = target; }
  emit(isIntersecting: boolean) {
    this.callback([{ target: this.target, isIntersecting } as IntersectionObserverEntry], this as unknown as IntersectionObserver);
  }
}

beforeEach(() => {
  observers.length = 0;
  resizeObservers.length = 0;
  vi.stubGlobal('IntersectionObserver', FakeIntersectionObserver);
  vi.stubGlobal('ResizeObserver', FakeResizeObserver);
});
afterEach(() => vi.unstubAllGlobals());

function setup(height = 0, contentHeight = 0) {
  const viewport = document.createElement('div');
  Object.defineProperty(viewport, 'clientHeight', { configurable: true, value: height });
  Object.defineProperty(viewport, 'scrollHeight', { configurable: true, value: contentHeight });
  const oldest = document.createElement('div');
  oldest.dataset.messageId = 'oldest';
  viewport.append(oldest);
  const viewportRef = { current: viewport };
  const options = { oldestMessageId: 'page-1', hasMore: true, loadingMore: false, onLoadMore: vi.fn() };
  const result = renderHook((props) => useHistoryPagination(viewportRef, props), { initialProps: options });
  return { ...result, options, viewport, oldest };
}

describe('useHistoryPagination', () => {
  it('fills an underfilled viewport page by page without waiting for intersection events', () => {
    const { viewport, rerender, options } = setup(800, 200);
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    rerender({ ...options, loadingMore: true });
    rerender({ ...options, oldestMessageId: 'page-2' });
    expect(options.onLoadMore).toHaveBeenCalledTimes(2);
    Object.defineProperty(viewport, 'scrollHeight', { value: 1000 });
    rerender({ ...options, oldestMessageId: 'page-3' });
    expect(options.onLoadMore).toHaveBeenCalledTimes(2);
  });

  it('rechecks after resizing without duplicating requests or retrying a failed page', () => {
    const { viewport, options, rerender, unmount } = setup(400, 600);
    expect(options.onLoadMore).not.toHaveBeenCalled();
    expect(resizeObservers[0].observe).toHaveBeenCalledWith(viewport);
    Object.defineProperty(viewport, 'clientHeight', { value: 800 });
    act(() => resizeObservers[0].emit());
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    act(() => { resizeObservers[0].emit(); observers[0].emit(true); });
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    rerender({ ...options, loadingMore: true });
    act(() => resizeObservers[0].emit());
    rerender(options);
    act(() => resizeObservers[1].emit());
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    unmount();
    act(() => resizeObservers[1].emit());
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    expect(resizeObservers[1].disconnect).toHaveBeenCalled();
  });

  it('loads when content exactly fills the viewport, then stops when history is exhausted', () => {
    const { options, rerender } = setup(600, 600);
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    rerender({ ...options, oldestMessageId: 'page-2', hasMore: false });
    act(() => resizeObservers[0].emit());
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    expect(resizeObservers[0].disconnect).toHaveBeenCalled();
  });

  it('waits for a hidden viewport to acquire a height', () => {
    const { options, viewport } = setup();
    act(() => resizeObservers[0].emit());
    expect(options.onLoadMore).not.toHaveBeenCalled();
    Object.defineProperty(viewport, 'clientHeight', { value: 600 });
    act(() => resizeObservers[0].emit());
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('still fills initially when ResizeObserver is unavailable', () => {
    vi.stubGlobal('ResizeObserver', undefined);
    const { options, unmount } = setup(600, 200);
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    unmount();
  });

  it('observes the oldest rendered message inside the conversation viewport', () => {
    const { viewport, oldest, options } = setup();
    expect(observers[0].target).toBe(oldest);
    expect(observers[0].options).toEqual({ root: viewport });
    act(() => observers[0].emit(false));
    expect(options.onLoadMore).not.toHaveBeenCalled();
    act(() => observers[0].emit(true));
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('continues through hidden pages while the same rendered message stays visible', () => {
    const { rerender, options } = setup();
    act(() => observers[0].emit(true));
    rerender({ ...options, loadingMore: true });
    expect(observers[0].disconnect).toHaveBeenCalled();
    expect(observers).toHaveLength(1);
    rerender({ ...options, oldestMessageId: 'page-2' });
    act(() => observers[1].emit(true));
    rerender({ ...options, oldestMessageId: 'page-3' });
    act(() => observers[2].emit(true));
    expect(options.onLoadMore).toHaveBeenCalledTimes(3);
    rerender({ ...options, oldestMessageId: 'first-message', hasMore: false });
    expect(observers[2].disconnect).toHaveBeenCalled();
    expect(observers).toHaveLength(3);
  });

  it('stops fetching when the oldest message leaves the viewport', () => {
    const { rerender, options } = setup();
    act(() => observers[0].emit(true));
    rerender({ ...options, oldestMessageId: 'page-2' });
    act(() => observers[1].emit(false));
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    act(() => observers[1].emit(true));
    expect(options.onLoadMore).toHaveBeenCalledTimes(2);
  });

  it('does not repeat requests without progress, but allows retry after leaving and re-entering', () => {
    const { rerender, options } = setup();
    act(() => { observers[0].emit(true); observers[0].emit(true); });
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    rerender({ ...options, loadingMore: true });
    rerender(options);
    act(() => observers[1].emit(true));
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
    act(() => { observers[1].emit(false); observers[1].emit(true); });
    expect(options.onLoadMore).toHaveBeenCalledTimes(2);
  });

  it('ignores queued callbacks after cleanup and switches to the newly prepended message', () => {
    const { rerender, options, viewport, unmount } = setup();
    const earlier = document.createElement('div');
    earlier.dataset.messageId = 'earlier';
    viewport.prepend(earlier);
    rerender({ ...options, oldestMessageId: 'earlier' });
    act(() => observers[0].emit(true));
    expect(options.onLoadMore).not.toHaveBeenCalled();
    expect(observers[1].target).toBe(earlier);
    unmount();
    act(() => observers[1].emit(true));
    expect(options.onLoadMore).not.toHaveBeenCalled();
    expect(observers[1].disconnect).toHaveBeenCalled();
  });

  it('uses the history start marker when every loaded message is hidden', () => {
    const onLoadMore = vi.fn();
    function HiddenThread() {
      const viewportRef = useRef<HTMLDivElement>(null);
      const startRef = useHistoryPagination(viewportRef, { hasMore: true, onLoadMore });
      return <div ref={viewportRef}><div ref={startRef} data-testid="start" /></div>;
    }
    const { getByTestId } = render(<HiddenThread />);
    expect(observers[0].target).toBe(getByTestId('start'));
    act(() => observers[0].emit(true));
    expect(onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('can identify the oldest message from the DOM when no explicit key is provided', () => {
    const { options, viewport, unmount } = setup();
    unmount();
    renderHook(() => useHistoryPagination({ current: viewport }, { hasMore: true, onLoadMore: options.onLoadMore }));
    act(() => observers[1].emit(true));
    expect(options.onLoadMore).toHaveBeenCalledTimes(1);
  });

  it('does not observe an absent viewport or a viewport without messages or a marker', () => {
    renderHook(() => useHistoryPagination({ current: null }, { hasMore: true, onLoadMore: vi.fn() }));
    renderHook(() => useHistoryPagination({ current: document.createElement('div') }, { hasMore: true, onLoadMore: vi.fn() }));
    expect(observers).toHaveLength(0);
  });

  it.each([
    { hasMore: false, loadingMore: false, onLoadMore: vi.fn() },
    { hasMore: true, loadingMore: true, onLoadMore: vi.fn() },
    { hasMore: true, loadingMore: false, onLoadMore: undefined },
  ])('does not observe when pagination is unavailable: %j', (options) => {
    renderHook(() => useHistoryPagination({ current: document.createElement('div') }, options));
    expect(observers).toHaveLength(0);
  });
});
