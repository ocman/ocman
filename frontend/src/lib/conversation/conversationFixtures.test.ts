// Behaviour the OCE reducer deliberately changed from the retired v1 path,
// checked against the shared golden fixtures (design §13.2).
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';
import { decode } from '../sessionViewParts';
import { foldOce, loadFixtures, type Fixture } from '../../test/oceFixtures';
import type { OceEnvelope } from './events';

const FIXTURES = loadFixtures(import.meta.glob('../../../../internal/convevents/v1map/testdata/*/*.oce.jsonl', {
  query: '?raw', import: 'default', eager: true,
}) as Record<string, string>);

const fixture = (name: string): Fixture => {
  const fx = FIXTURES.find((f) => f.name === name);
  if (!fx) throw new Error(`missing fixture ${name}`);
  return fx;
};
const fold = (fx: Fixture, upTo?: (e: OceEnvelope) => boolean) => {
  const cut = upTo ? fx.envelopes.findIndex(upTo) + 1 : fx.envelopes.length;
  return foldOce(fx.sessionId, fx.envelopes.slice(0, cut)).view;
};

beforeAll(() => {
  vi.useFakeTimers();
  vi.setSystemTime(1_700_000_000_000);
  vi.stubGlobal('requestAnimationFrame', () => 0);
  vi.stubGlobal('cancelAnimationFrame', () => {});
});
afterAll(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe('golden fixtures', () => {
  it('cover every platform', () => {
    expect(new Set(FIXTURES.map((f) => f.name.split('/')[0]))).toEqual(new Set(['claudecode', 'ocman', 'ocv2', 'opencode']));
  });

  it.each([
    ['ocv2/revert', []],
    ['opencode/revert', []],
    ['opencode/streaming-text-tool-output', ['msg_u1', 'msg_a1']],
    ['opencode/subagent-child', ['msg_a6']],
  ])('%s: session info does not become a message', (name, ids) => {
    expect(fold(fixture(name)).messages.map((m) => m.id)).toEqual(ids);
  });

  it('applies session metadata updates', () => {
    expect(fold(fixture('ocv2/revert')).session?.title).toBe('Fix bug');
    expect(fold(fixture('opencode/revert')).session).toMatchObject({
      title: 'Fix a.go', shareUrl: 'https://opncd.ai/s/xyz', archived: true,
    });
    expect(fold(fixture('opencode/streaming-text-tool-output')).session).toMatchObject({
      summaryAdditions: 1, summaryDeletions: 1, summaryFiles: 1,
    });
  });

  it('withholds a delta whose part it has not seen instead of inventing one', () => {
    const view = fold(fixture('opencode/streaming-text-tool-output'));
    expect(view.parts.map((p) => p.id)).not.toContain('prt_old');
    expect(view.messages.map((m) => m.id)).not.toContain('msg_old');
  });

  it('records a user approval without judge reasoning', () => {
    const notices = fold(fixture('ocman/autoapprove-judge')).parts
      .filter((p) => p.id.startsWith('ocman-notice-')).map((p) => decode(p));
    expect(notices).toEqual([
      expect.objectContaining({ approvedBy: 'ai', reasoning: 'Running the test suite is safe.' }),
      expect.objectContaining({ approvedBy: 'user', reasoning: '', metadata: null }),
    ]);
  });

  it.each(['opencode/named-channel-recovery', 'opencode/question-answered-in-tui'])(
    '%s: a running tool does not make the session busy; status comes from the server', (name) => {
      const fx = fixture(name);
      const running = fold(fx, (e) => e.event.type === 'part.upserted' && JSON.stringify(e.event).includes('"status":"running"'));
      expect(running.parts.some((p) => JSON.stringify(decode(p)).includes('"status":"running"'))).toBe(true);
      expect(running.session?.status).toBe('done');
    },
  );

  it('tracks descendant status', () => {
    const fx = fixture('opencode/subagent-child');
    const deleted = fx.envelopes.findIndex((e) => e.event.type === 'child.upserted' && 'child' in e.event && e.event.child.deleted);
    expect(foldOce(fx.sessionId, fx.envelopes.slice(0, deleted)).view.childStatus).toEqual({ ses_child: { status: 'done' } });
    expect(fold(fx).childStatus).toEqual({});
  });

  it('shows a descendant prompt on the parent', () => {
    const view = fold(fixture('opencode/subagent-child'), (e) => e.event.type === 'prompt.asked');
    expect(view.pendingPermission).toMatchObject({ permissionId: 'per_child', sessionId: 'ses_child' });
  });
});
