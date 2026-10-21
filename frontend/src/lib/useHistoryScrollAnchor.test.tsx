// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useHistoryScrollAnchor } from './useHistoryScrollAnchor';

const frames = new Map<number, FrameRequestCallback>();
let frameId = 0;
beforeEach(() => {
  frames.clear();
  vi.stubGlobal('requestAnimationFrame', (callback: FrameRequestCallback) => {
    frames.set(++frameId, callback);
    return frameId;
  });
  vi.stubGlobal('cancelAnimationFrame', (id: number) => frames.delete(id));
});
afterEach(() => vi.unstubAllGlobals());

function finishFrame() {
  const pending = [...frames.values()];
  frames.clear();
  act(() => pending.forEach(callback => callback(0)));
}

function setup() {
  const viewport = document.createElement('div');
  let top = 40;
  let bottom = 120;
  const message = document.createElement('div');
  message.dataset.messageId = 'reading';
  viewport.append(message);
  vi.spyOn(viewport, 'getBoundingClientRect').mockImplementation(() => ({ top: 20, bottom: 500 }) as DOMRect);
  vi.spyOn(message, 'getBoundingClientRect').mockImplementation(() => ({
    top: top - viewport.scrollTop, bottom: bottom - viewport.scrollTop,
  }) as DOMRect);
  const onLoadMore = vi.fn();
  const ref = { current: viewport as HTMLElement | null };
  const result = renderHook(({ loadingMore }) => useHistoryScrollAnchor(ref, loadingMore, onLoadMore), {
    initialProps: { loadingMore: false },
  });
  return { ...result, viewport, message, ref, onLoadMore, move: (value: number) => {
    top = value + viewport.scrollTop;
    bottom = top + 80;
  } };
}

describe('useHistoryScrollAnchor', () => {
  it('corrects loading indicator and prepend displacement synchronously', () => {
    const h = setup();
    act(() => h.result.current?.());
    expect(h.onLoadMore).toHaveBeenCalledOnce();
    h.move(96);
    h.rerender({ loadingMore: true });
    expect(h.viewport.scrollTop).toBe(56);
    h.move(340);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(356);
    finishFrame();
    h.move(900);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(356);
  });

  it('does not double compensate when native anchoring already preserved the message', () => {
    const h = setup();
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    h.viewport.scrollTop = 700;
    h.move(40);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(700);
  });

  it('accounts for shrinking content as well as growth', () => {
    const h = setup();
    h.viewport.scrollTop = 100;
    h.move(40);
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    h.move(10);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(70);
  });

  it('honours scrolling while the request is pending and removes its listener afterward', () => {
    const h = setup();
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    h.move(25);
    act(() => h.viewport.dispatchEvent(new Event('scroll')));
    h.move(125);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(100);
    finishFrame();
    act(() => h.viewport.dispatchEvent(new Event('scroll')));
    h.move(300);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(100);
  });

  it('skips offscreen messages when choosing an anchor', () => {
    const h = setup();
    const offscreen = document.createElement('div');
    offscreen.dataset.messageId = 'offscreen';
    vi.spyOn(offscreen, 'getBoundingClientRect').mockReturnValue({ top: -100, bottom: -20 } as DOMRect);
    h.viewport.prepend(offscreen);
    act(() => h.result.current?.());
    h.move(140);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(100);
  });

  it('leaves empty or entirely hidden pages alone', () => {
    const h = setup();
    h.move(600);
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    h.move(900);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(0);
  });

  it('does not restore a removed message', () => {
    const h = setup();
    act(() => h.result.current?.());
    h.message.remove();
    h.move(400);
    h.rerender({ loadingMore: false });
    expect(h.viewport.scrollTop).toBe(0);
  });

  it('tolerates a missing viewport', () => {
    const h = setup();
    h.ref.current = null;
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    expect(h.onLoadMore).toHaveBeenCalledOnce();
  });

  it('does not expose pagination without a loader', () => {
    const { result } = renderHook(() => useHistoryScrollAnchor({ current: null }, false, undefined));
    expect(result.current).toBeUndefined();
  });

  it('restores a later external-runtime commit before releasing the snapshot', async () => {
    const h = setup();
    act(() => h.result.current?.());
    h.rerender({ loadingMore: true });
    h.rerender({ loadingMore: false });
    h.move(240);
    await act(async () => { h.message.append(document.createElement('span')); });
    expect(h.viewport.scrollTop).toBe(200);
    // A library resize correction in the same frame must not win either.
    h.move(90);
    finishFrame();
    expect(h.viewport.scrollTop).toBe(250);
  });
});
