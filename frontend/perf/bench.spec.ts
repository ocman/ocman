/**
 * Interaction bench: `pnpm perf` (see docs/other/profiling.md).
 *
 * Loads the built bundle with every /api route mocked, replaces
 * EventSource with an in-page fake that replays fixture frames at a fixed
 * rate, throttles the CPU 4x via CDP, and records the app's own perf
 * monitor (`window.__ocmanPerf.metrics()`) per scenario.
 *
 * Results: ../tmp/perf/<PERF_LABEL>.json; compare two runs with
 * `node perf/compare.mjs a.json b.json`.
 */
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { test, expect, type Page } from '../e2e/fixtures';
import { SIDEBAR, buildSession, streamFrames, taskSnapshot, type Fixture } from './fixtures';

const CPU_RATE = Number(process.env.PERF_CPU ?? 4);
const LABEL = process.env.PERF_LABEL ?? 'run';
const FRAME_MS = 20;
const SUBAGENT = 'ses_benchchild';
const id = (i: number) => SIDEBAR[i].id;
const CPU_PROFILE = process.env.PERF_CPUPROFILE === '1';
const OUT_DIR = resolve(process.cwd(), '../tmp/perf');
const results: Record<string, unknown> = {};

// In-page fake EventSource + frame player. Frames emit on wall-clock
// schedule; a late timer emits every overdue frame at once, like a
// network buffer would.
const INIT = () => {
  localStorage.setItem('ocman:perf', '1');
  const sources: FakeES[] = [];
  class FakeES extends EventTarget {
    url: string; readyState = 0; withCredentials = false;
    onopen: ((e: Event) => void) | null = null;
    onmessage: ((e: MessageEvent) => void) | null = null;
    onerror: ((e: Event) => void) | null = null;
    constructor(url: string) {
      super();
      this.url = url;
      sources.push(this);
      setTimeout(() => { this.readyState = 1; this.onopen?.(new Event('open')); }, 0);
    }
    close() { this.readyState = 2; }
  }
  (window as unknown as { EventSource: unknown }).EventSource = FakeES;
  let lastLongFrameEnd = 0;
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) lastLongFrameEnd = Math.max(lastLongFrameEnd, e.startTime + e.duration);
  }).observe({ type: 'long-animation-frame' });
  (window as unknown as { __bench: unknown }).__bench = {
    /** Resolves once no long frame has ended for `quietMs`. */
    idle(quietMs: number) {
      return new Promise<void>((done) => {
        const check = () => (performance.now() - lastLongFrameEnd > quietMs ? done() : setTimeout(check, 100));
        setTimeout(check, 100);
      });
    },
    play(sessionId: string, frames: [string, string][][], intervalMs: number) {
      return new Promise<void>((done) => {
        const start = performance.now();
        let next = 0;
        const tick = () => {
          const due = Math.floor((performance.now() - start) / intervalMs);
          for (; next <= due && next < frames.length; next++) {
            for (const [channel, data] of frames[next]) {
              for (const es of sources) {
                if (es.readyState !== 1 || !es.url.includes(`/api/session/${sessionId}/events`)) continue;
                const ev = new MessageEvent(channel, { data });
                if (channel === 'message') es.onmessage?.(ev); else es.dispatchEvent(ev);
              }
            }
          }
          if (next >= frames.length) done(); else setTimeout(tick, intervalMs / 2);
        };
        tick();
      });
    },
  };
};

const fixtures = new Map<string, Fixture>();
function fixtureFor(sessionId: string): Fixture {
  let f = fixtures.get(sessionId);
  if (!f) {
    const row = SIDEBAR.find((s) => s.id === sessionId);
    f = buildSession(sessionId, row?.title ?? sessionId, sessionId === id(1) ? { runningTask: SUBAGENT } : {});
    fixtures.set(sessionId, f);
  }
  return f;
}

async function installBenchRoutes(page: Page) {
  const json = (body: unknown, headers: Record<string, string> = {}) => ({ status: 200, contentType: 'application/json', headers, body: JSON.stringify(body) });
  await page.route(/\/api\/sessions(\?.*)?$/, (route) => route.fulfill(json(SIDEBAR)));
  let taskStep = 0;
  await page.route('/api/session/**', async (route) => {
    const url = new URL(route.request().url());
    const [, , , sid, sub] = url.pathname.split('/');
    if (sub === 'tasks') return route.fulfill(json({ tasks: { [SUBAGENT]: taskSnapshot(SUBAGENT, Math.min(taskStep++, 20)) } }));
    if (sub) return route.fallback();
    const f = fixtureFor(decodeURIComponent(sid));
    // Simulated server latency so a cache miss pays a realistic fetch.
    await new Promise((r) => setTimeout(r, 80));
    return route.fulfill(json({ session: f.session, messages: f.messages, parts: f.parts, totalMessages: f.messages.length, defaultAgent: 'build', defaultModel: 'anthropic/claude' }, { 'Server-Timing': 'db;dur=42, encode;dur=6' }));
  });
}

async function metrics(page: Page, scenario: string) {
  await settle(page); // finish trailing work, let PerformanceObservers deliver
  if (CPU_PROFILE) {
    const { profile } = await cdp.send('Profiler.stop');
    mkdirSync(OUT_DIR, { recursive: true });
    writeFileSync(resolve(OUT_DIR, `${LABEL}-${scenario}.cpuprofile`), JSON.stringify(profile));
  }
  // API calls per endpoint during the scenario, from the perfRing.
  return page.evaluate(() => {
    const perf = (window as unknown as { __ocmanPerf: { metrics: () => object; summary: () => { pathTemplate: string; count: number }[] } }).__ocmanPerf;
    return { ...perf.metrics(), api: Object.fromEntries(perf.summary().map((r) => [r.pathTemplate, r.count])) };
  });
}

type Cdp = Awaited<ReturnType<ReturnType<Page['context']>['newCDPSession']>>;
let cdp: Cdp;

/** Settle, reset metrics and, with PERF_CPUPROFILE=1, start a CPU profile. */
async function reset(page: Page) {
  await settle(page);
  if (CPU_PROFILE) { await cdp.send('Profiler.enable'); await cdp.send('Profiler.start'); }
  await page.evaluate(() => {
    const perf = (window as unknown as { __ocmanPerf: { resetMetrics: () => void; clear: () => void } }).__ocmanPerf;
    perf.resetMetrics();
    perf.clear();
  });
}

async function play(page: Page, sessionId: string, frames: unknown) {
  await page.evaluate(([sid, f, ms]) => (window as unknown as { __bench: { play: (a: unknown, b: unknown, c: unknown) => Promise<void> } }).__bench.play(sid, f, ms), [sessionId, frames, FRAME_MS] as const);
}

/** Wait for the main thread to go quiet so scenarios don't overlap. */
async function settle(page: Page) {
  await page.evaluate(() => (window as unknown as { __bench: { idle: (ms: number) => Promise<void> } }).__bench.idle(1000));
}

async function open(page: Page, sessionId: string) {
  await page.goto(`/session/${sessionId}`);
  await expect(page.getByText('Please continue with step 148').first()).toBeAttached({ timeout: 30_000 });
  await settle(page);
}

async function switchCount(page: Page) {
  return page.evaluate(() => {
    const m = (window as unknown as { __ocmanPerf: { metrics: () => { metrics: Record<string, Record<string, { count: number }>> } } }).__ocmanPerf.metrics().metrics.switch ?? {};
    return Object.values(m).reduce((n, s) => n + s.count, 0);
  });
}

test.describe.configure({ mode: 'serial' });
test.setTimeout(600_000);

test('interaction bench', async ({ mockedPage: page }) => {
  await page.addInitScript(INIT);
  await installBenchRoutes(page);
  await page.setViewportSize({ width: 1600, height: 1000 });
  cdp = await page.context().newCDPSession(page);

  await open(page, id(0));
  await cdp.send('Emulation.setCPUThrottlingRate', { rate: CPU_RATE });

  // PERF_SCENARIOS=stream,switch runs a subset (same order).
  const only = process.env.PERF_SCENARIOS?.split(',');
  const scenarios: [string, () => Promise<void>][] = [
    // Stream a long markdown answer.
    ['stream', () => play(page, id(0), streamFrames(id(0)))],
    // Type in the composer while streaming.
    ['typeWhileStreaming', async () => {
      const composer = page.locator('[data-perf="composer"] textarea');
      await composer.click();
      const typing = composer.pressSequentially('the quick brown fox jumps over the lazy dog '.repeat(2), { delay: 60 });
      const frames = streamFrames(id(0), { chars: 5000 }).map((f) => f.map(([c, d]) => [c, d.replace(/_live/g, '_live2')] as [string, string]));
      await Promise.all([play(page, id(0), frames), typing]);
      await composer.fill('');
    }],
    // Expand tool calls / diffs.
    ['expandTools', async () => {
      for (let i = 0; i < 12; i++) {
        const header = page.locator('[data-perf="tool-call"] button[aria-expanded="false"]').last();
        await header.scrollIntoViewIfNeeded();
        await header.click();
        await page.waitForTimeout(250);
      }
    }],
    // Scroll the thread up and back down.
    ['scroll', async () => {
      const box = (await page.getByTestId('session-main').boundingBox())!;
      await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
      for (let i = 0; i < 25; i++) { await page.mouse.wheel(0, -500); await page.waitForTimeout(60); }
      for (let i = 0; i < 25; i++) { await page.mouse.wheel(0, 500); await page.waitForTimeout(60); }
    }],
    // Switch sessions: first visits to rows 2..5 miss the session cache,
    // then 0 ⇄ 5 alternate inside it (hit).
    ['switch', async () => {
      for (const i of [2, 3, 4, 5, 0, 5, 0, 5, 0, 5, 0, 5]) {
        const before = await switchCount(page);
        await page.locator(`[data-session-key$=":opencode:${id(i)}"]`).first().click();
        await page.waitForFunction((n) => {
          const m = (window as unknown as { __ocmanPerf: { metrics: () => { metrics: Record<string, Record<string, { count: number }>> } } }).__ocmanPerf.metrics().metrics.switch ?? {};
          return Object.values(m).reduce((c, s) => c + s.count, 0) > n;
        }, before, { timeout: 60_000 });
        await settle(page);
      }
    }],
    // Sit idle on the session page: background polls only.
    ['idle', () => page.waitForTimeout(30_000)],
    // Stream while a subagent runs (task poll + foreign events).
    ['streamWithSubagent', () => play(page, id(1), streamFrames(id(1), { foreignId: SUBAGENT }))],
  ];
  for (const [name, run] of scenarios) {
    if (only && !only.includes(name)) continue;
    if (name === 'streamWithSubagent') {
      await page.locator(`[data-session-key$=":opencode:${id(1)}"]`).first().click();
      await expect(page.getByText('Please continue with step 148').first()).toBeAttached({ timeout: 60_000 });
    }
    await reset(page);
    await run();
    results[name] = await metrics(page, name);
  }

  const out = resolve(OUT_DIR, `${LABEL}.json`);
  mkdirSync(OUT_DIR, { recursive: true });
  writeFileSync(out, JSON.stringify({ label: LABEL, cpu: CPU_RATE, results }, null, 2));
  console.log(`perf results → ${out}`);
});
