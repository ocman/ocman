import { describe, it, expect, beforeEach } from 'vitest';
import {
  _resetForTests,
  installDevHandle,
  record,
  snapshot,
  summary,
  templatePath,
} from './perfRing';

beforeEach(() => {
  _resetForTests();
});

describe('templatePath', () => {
  it('strips query strings', () => {
    expect(templatePath('/api/sessions?since=123&limit=50')).toBe('/api/sessions');
  });

  it('replaces UUID-ish session segments with :id', () => {
    expect(templatePath('/api/session/01ABCDEF-aaaa-bbbb-cccc-1234567890ab/info')).toBe(
      '/api/session/:id/info',
    );
  });

  it('replaces hex/base64-ish blobs with :id', () => {
    expect(templatePath('/api/session/abc123def456abc123def456/messages')).toBe(
      '/api/session/:id/messages',
    );
  });

  it('replaces percent-encoded directory segments with :path', () => {
    expect(templatePath('/api/git/diff?dir=%2FUsers%2Fdries')).toBe('/api/git/diff');
  });

  it('preserves short alphabetic segments', () => {
    expect(templatePath('/api/sessions/notify')).toBe('/api/sessions/notify');
    expect(templatePath('/api/system/stats')).toBe('/api/system/stats');
  });
});

describe('perfRing storage', () => {
  it('records entries in insertion order until capacity', () => {
    record({ pathTemplate: '/a', method: 'GET', status: 200, durationMs: 10, startedAt: 0 });
    record({ pathTemplate: '/b', method: 'GET', status: 200, durationMs: 20, startedAt: 1 });
    const all = snapshot();
    expect(all.map((e) => e.pathTemplate)).toEqual(['/a', '/b']);
  });

  it('summarises grouped entries with avg/p50/p95/max and errors', () => {
    for (const ms of [10, 20, 30, 40, 50, 60, 70, 80, 90, 100]) {
      record({ pathTemplate: '/api/sessions', method: 'GET', status: 200, durationMs: ms, startedAt: 0 });
    }
    record({ pathTemplate: '/api/sessions', method: 'GET', status: 500, durationMs: 500, startedAt: 0 });
    record({ pathTemplate: '/api/stats', method: 'GET', status: 200, durationMs: 5, startedAt: 0 });

    const rows = summary();
    // Ordered by maxMs desc, so /api/sessions (500ms) is first.
    expect(rows[0].pathTemplate).toBe('/api/sessions');
    expect(rows[0].count).toBe(11);
    expect(rows[0].errorCount).toBe(1);
    expect(rows[0].maxMs).toBe(500);
    // p95 of 11 entries (10..100 + 500) — sorted index floor(0.95*11)=10 → 500.
    expect(rows[0].p95Ms).toBe(500);

    // /api/stats has its own row.
    const stats = rows.find((r) => r.pathTemplate === '/api/stats');
    expect(stats).toBeDefined();
    expect(stats?.count).toBe(1);
    expect(stats?.errorCount).toBe(0);
  });

  it('counts a status of 0 (network error) as an error', () => {
    record({ pathTemplate: '/api/x', method: 'GET', status: 0, durationMs: 100, startedAt: 0 });
    expect(summary()[0].errorCount).toBe(1);
  });

  it('evicts oldest entries past capacity (ring buffer)', () => {
    // Fill past capacity (100). After 105 records, the first 5 should be gone.
    for (let i = 0; i < 105; i++) {
      record({ pathTemplate: `/api/x${i}`, method: 'GET', status: 200, durationMs: i, startedAt: i });
    }
    const all = snapshot();
    expect(all.length).toBe(100);
    // First entry should be /api/x5 (5..104 retained), last should be /api/x104.
    expect(all[0].pathTemplate).toBe('/api/x5');
    expect(all[all.length - 1].pathTemplate).toBe('/api/x104');
  });
});

describe('installDevHandle', () => {
  it('attaches __ocmanPerf to window once', () => {
    // jsdom isn't loaded; simulate by constructing a window-ish stub.
    const stub = {} as unknown as Window;
    (globalThis as unknown as { window?: Window }).window = stub;
    try {
      installDevHandle();
      expect(stub.__ocmanPerf).toBeDefined();
      const first = stub.__ocmanPerf;
      installDevHandle();
      // Idempotent — same handle.
      expect(stub.__ocmanPerf).toBe(first);
    } finally {
      (globalThis as unknown as { window?: Window | undefined }).window = undefined;
    }
  });
});
