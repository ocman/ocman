// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  _resetPerfMonitorForTests,
  countRender,
  installPerfMonitor,
  labelInteraction,
  markSessionSwitchRendered,
  markSessionSwitchStart,
  markSseCommit,
  markSseFlush,
  onProfilerRender,
  perfEnabled,
  perfLabel,
  recordSample,
  summarize,
  summary,
  timeSseBatch,
} from './perfMonitor';
import { _resetForTests as resetRing, record, snapshot } from './perfRing';

type ObserverCb = (list: { getEntries: () => unknown[] }) => void;
const observers = new Map<string, ObserverCb>();

class FakeObserver {
  cb: ObserverCb;
  constructor(cb: ObserverCb) { this.cb = cb; }
  observe(opts: { type: string }) { observers.set(opts.type, this.cb); }
  disconnect() {}
}
const feed = (type: string, entries: unknown[]) => observers.get(type)!({ getEntries: () => entries });

beforeEach(() => {
  observers.clear();
  resetRing();
  delete window.__ocmanPerf;
  localStorage.clear();
  window.history.replaceState({}, '', '/');
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
  _resetPerfMonitorForTests();
});

describe('perfEnabled', () => {
  it('is off by default and every recorder is a no-op', () => {
    _resetPerfMonitorForTests();
    expect(perfEnabled()).toBe(false);
    recordSample('x', 'y', 1);
    expect(timeSseBatch(3, () => 42)).toBe(42);
    markSseFlush();
    markSseCommit();
    expect(summary().metrics).toEqual({});
  });

  it('turns on with ?debug or the localStorage flag', () => {
    window.history.replaceState({}, '', '/?debug');
    _resetPerfMonitorForTests();
    expect(perfEnabled()).toBe(true);
    window.history.replaceState({}, '', '/');
    localStorage.setItem('ocman:perf', '1');
    _resetPerfMonitorForTests();
    expect(perfEnabled()).toBe(true);
  });
});

describe('recorders', () => {
  beforeEach(() => _resetPerfMonitorForTests(true));

  it('summarizes percentiles', () => {
    expect(summarize([5, 1, 3, 2, 4])).toEqual({ count: 5, p50: 3, p95: 5, max: 5, sum: 15 });
    expect(summarize([])).toEqual({ count: 0, p50: 0, p95: 0, max: 0, sum: 0 });
  });

  it('times SSE batches and closes the flush→commit span once', () => {
    const out = timeSseBatch(4, () => ({ m: 2, p: 7 }), (o) => [o.m, o.p]);
    expect(out).toEqual({ m: 2, p: 7 });
    markSseFlush();
    markSseFlush(); // a second flush before the commit keeps the first start
    markSseCommit();
    markSseCommit(); // nothing pending
    const m = summary().metrics;
    expect(m['sse.batch'].events.sum).toBe(4);
    expect(m['sse.batch'].messages.p50).toBe(2);
    expect(m['sse.batch'].parts.p50).toBe(7);
    expect(m['sse.commit']['flush→commit'].count).toBe(1);
  });

  it('measures a session switch on the next frame, tagged by cache state', () => {
    const frames: FrameRequestCallback[] = [];
    vi.stubGlobal('requestAnimationFrame', (cb: FrameRequestCallback) => { frames.push(cb); return 1; });
    markSessionSwitchStart('a', true);
    markSessionSwitchRendered('b'); // other session: ignored
    markSessionSwitchRendered('a');
    markSessionSwitchRendered('a'); // already consumed
    expect(frames).toHaveLength(1);
    frames[0](0);
    markSessionSwitchStart('c', false);
    markSessionSwitchRendered('c');
    frames[1](0);
    const m = summary().metrics.switch;
    expect(m.hit.count).toBe(1);
    expect(m.miss.count).toBe(1);
  });

  it('counts profiler commits and renders, and caps series length', () => {
    onProfilerRender('Thread', 'update', 3);
    countRender('SessionDetail');
    for (let i = 0; i < 2100; i++) recordSample('cap', 'x', i);
    const s = summary();
    expect(s.metrics.render.Thread.sum).toBe(3);
    expect(s.counters['commits|Thread|update']).toBe(1);
    expect(s.counters['renders|SessionDetail']).toBe(1);
    expect(s.metrics.cap.x.count).toBe(2000);
  });

  it('labels from the nearest data-perf ancestor', () => {
    document.body.innerHTML = '<div data-perf="session-row"><span id="t">x</span></div>';
    const span = document.getElementById('t')!;
    expect(perfLabel(span)).toBe('session-row');
    expect(perfLabel(span.firstChild)).toBe('session-row');
    expect(perfLabel(document.body)).toBe('other');
    expect(perfLabel(null)).toBe('other');
  });
});

describe('installPerfMonitor', () => {
  it('only installs the API ring handle when disabled', () => {
    _resetPerfMonitorForTests(false);
    vi.stubGlobal('PerformanceObserver', FakeObserver);
    installPerfMonitor();
    expect(window.__ocmanPerf?.summary).toBeTypeOf('function');
    expect(window.__ocmanPerf?.metrics).toBeUndefined();
    expect(observers.size).toBe(0);
  });

  it('records interactions, long frames and server timing, and beacons on hide', () => {
    _resetPerfMonitorForTests(true);
    vi.stubGlobal('PerformanceObserver', FakeObserver);
    vi.spyOn(performance, 'now').mockReturnValue(105);
    installPerfMonitor();
    installPerfMonitor(); // idempotent
    expect(window.__ocmanPerf?.metrics).toBeTypeOf('function');

    document.body.innerHTML = '<button data-perf="tool-call" id="b">x</button>';
    const target = document.getElementById('b');
    labelInteraction('palette-open');
    feed('event', [
      { name: 'pointerdown', interactionId: 1, duration: 40, target, processingStart: 0, processingEnd: 1 },
      { name: 'click', interactionId: 1, duration: 80, target, processingStart: 0, processingEnd: 1 },
      { name: 'keydown', interactionId: 2, duration: 30, target: null, processingStart: 100, processingEnd: 110 },
      { name: 'mousemove', interactionId: 0, duration: 99, target, processingStart: 0, processingEnd: 1 },
    ]);
    feed('long-animation-frame', [
      { duration: 120, blockingDuration: 70, scripts: [
        { invoker: 'a', sourceURL: 'https://x/app.js', sourceFunctionName: 'f', duration: 10 },
        { invoker: 'b', sourceURL: 'https://x/app.js', sourceFunctionName: '', duration: 90 },
      ] },
      { duration: 60, blockingDuration: 10 },
    ]);
    record({ pathTemplate: '/api/session/:id', method: 'GET', status: 200, durationMs: 50, startedAt: 10 });
    feed('resource', [
      { name: 'http://localhost/api/session/ses_abcdefabcdefabcd', startTime: 12, serverTiming: [{ name: 'db', duration: 40, description: '' }] },
      { name: 'http://localhost/assets/a.js', startTime: 12, serverTiming: [{ name: 'x', duration: 1, description: '' }] },
      { name: 'http://localhost/api/other', startTime: 12, serverTiming: [] },
    ]);

    const s = summary();
    expect(s.metrics.interaction['pointer:tool-call']).toMatchObject({ count: 1, max: 80 });
    expect(s.metrics.interaction['keyboard:palette-open']).toMatchObject({ count: 1 });
    expect(s.metrics.interaction.all.count).toBe(2);
    expect(s.metrics.loaf.blocking.sum).toBe(80);
    expect(s.topLoafScripts[0]).toEqual({ script: 'b @ anon (app.js)', ms: 90 });
    expect(snapshot()[0].serverTiming).toEqual([{ name: 'db', durationMs: 40 }]);

    const beacon = vi.fn(() => true);
    vi.stubGlobal('navigator', { sendBeacon: beacon });
    Object.defineProperty(document, 'visibilityState', { configurable: true, value: 'hidden' });
    document.dispatchEvent(new Event('visibilitychange'));
    expect(beacon).toHaveBeenCalledWith('/api/debug/log', expect.any(Blob));
    expect(summary().metrics).toEqual({}); // reset after a beacon
    document.dispatchEvent(new Event('visibilitychange')); // nothing to send
    expect(beacon).toHaveBeenCalledTimes(1);
  });
});
