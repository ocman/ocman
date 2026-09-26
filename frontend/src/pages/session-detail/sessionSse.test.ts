// @vitest-environment jsdom
import { afterEach, expect, it, vi } from 'vitest';
import { initialSessionView } from '../../lib/sessionReducer';
import { createSessionSse, reduceBatchedSessionView } from './sessionSse';

afterEach(() => {
  vi.useRealTimers();
  vi.restoreAllMocks();
});

it('dispatches one reducer action for a burst, rather than relying on React render batching', () => {
  vi.useFakeTimers();
  vi.spyOn(window, 'requestAnimationFrame').mockReturnValue(1);
  vi.spyOn(window, 'cancelAnimationFrame').mockImplementation(() => {});
  const dispatch = vi.fn();
  const stream = createSessionSse({
    sessionId: 's', dispatch, reconcile: vi.fn(async () => true), dirty: vi.fn(),
  });
  const source = { onmessage: null, addEventListener: vi.fn() } as unknown as EventSource;
  stream.attach(source);
  for (let i = 0; i < 100; i++) {
    source.onmessage!(new MessageEvent('message', { data: JSON.stringify({
      type: 'message.part.delta',
      properties: { sessionID: 's', messageID: 'm', partID: 'p', field: 'text', delta: 'x' },
    }) }));
  }
  expect(dispatch).not.toHaveBeenCalled();
  vi.advanceTimersByTime(50);
  expect(dispatch).toHaveBeenCalledTimes(1);
  const action = dispatch.mock.calls[0][0];
  expect(action.type).toBe('sseBatch');
  const view = reduceBatchedSessionView(initialSessionView('s'), action);
  expect(view.parts[0].data).toEqual({ type: 'text', text: 'x'.repeat(100) });
  stream.dispose();
  vi.runAllTimers();
  expect(dispatch).toHaveBeenCalledTimes(1);
});

it('ignores malformed default-channel payloads without interrupting the stream', () => {
  const dispatch = vi.fn();
  const stream = createSessionSse({
    sessionId: 's', dispatch, reconcile: vi.fn(async () => true), dirty: vi.fn(),
  });
  const source = { onmessage: null, addEventListener: vi.fn() } as unknown as EventSource;
  stream.attach(source);
  for (const data of ['{bad json', '{}', '[]', '42', 'true', '"text"', 'null']) {
    expect(() => source.onmessage!(new MessageEvent('message', { data }))).not.toThrow();
  }
  stream.flush();
  expect(dispatch).not.toHaveBeenCalled();
  stream.dispose();
});

it('invalidates old reconcile completions when a new connection is attached', async () => {
  let finishOld!: (success: boolean) => void;
  const reconcile = vi.fn(async () => true)
    .mockImplementationOnce(() => new Promise<boolean>((resolve) => { finishOld = resolve; }));
  const stream = createSessionSse({ sessionId: 's', dispatch: vi.fn(), reconcile, dirty: vi.fn() });
  const source = () => ({ onmessage: null, addEventListener: vi.fn() }) as unknown as EventSource;
  const old = source();
  stream.attach(old);
  const idle = new MessageEvent('message', { data: '{"type":"session.idle","properties":{}}' });
  old.onmessage!(idle);
  const current = source();
  stream.attach(current);
  current.onmessage!(idle);
  await Promise.resolve();
  expect(reconcile).toHaveBeenCalledTimes(2);
  finishOld(false);
  await Promise.resolve();
  current.onmessage!(idle);
  expect(reconcile).toHaveBeenCalledTimes(2);
  stream.dispose();
});
