// @vitest-environment jsdom
import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { startHandoffs, useStartHandoff } from './startHandoffs';

const message = { id: 'm1', sessionId: 's1', timeCreated: 1, data: { role: 'user' } };
const part = { id: 'p1', messageId: 'm1', sessionId: 's1', timeCreated: 1, data: { type: 'text', text: 'Fix login' } };

describe('useStartHandoff', () => {
  it('shows the handoff until the first message, then drops it for good', () => {
    const handoff = { prompt: 'Fix login', steps: { prompt: 'done' as const } };
    startHandoffs.set('s1', handoff);
    const { result, rerender } = renderHook(({ count }) => useStartHandoff('s1', 's1', count ? [message] : [], count ? [part] : []), { initialProps: { count: 0 } });
    expect(result.current).toBe(handoff);
    rerender({ count: 1 });
    expect(result.current).toBeUndefined();
    expect(startHandoffs.has('s1')).toBe(false);
    // An emptied thread (e.g. a dismissed message) does not bring it back.
    rerender({ count: 0 });
    expect(result.current).toBeUndefined();
  });

  it('ignores the previous session\'s messages while the view switches', () => {
    const handoff = { prompt: 'Fix login', steps: {} };
    startHandoffs.set('s2', handoff);
    const { result, rerender } = renderHook(({ view, count }) => useStartHandoff('s2', view, count ? [message] : [], count ? [part] : []), {
      initialProps: { view: 'old', count: 5 },
    });
    expect(result.current).toBe(handoff);
    expect(startHandoffs.has('s2')).toBe(true);
    rerender({ view: 's2', count: 0 });
    expect(result.current).toBe(handoff);
  });

  it('has nothing for an unknown or missing session', () => {
    expect(renderHook(() => useStartHandoff('other', 'other', [], [])).result.current).toBeUndefined();
    expect(renderHook(() => useStartHandoff(undefined, undefined, [], [])).result.current).toBeUndefined();
  });

  it.each([
    { type: 'text', text: '' },
    { type: 'text', text: '   ' },
    { type: 'file' },
    { type: 'file', url: '/note.txt' },
    '{invalid',
  ])('keeps the handoff for an invisible part: %j', (data) => {
    const handoff = { prompt: 'Fix login', steps: {} };
    startHandoffs.set('s1', handoff);
    expect(renderHook(() => useStartHandoff('s1', 's1', [message], [{ ...part, data }])).result.current).toBe(handoff);
  });

  it.each([
    JSON.stringify({ type: 'text', text: 'Fix login' }),
    { type: 'file', mime: 'image/png', url: 'data:image/png;base64,AAAA' },
    { type: 'file', url: '/note.txt', filename: 'note.txt' },
  ])('finishes the handoff for a rendered user part: %j', (data) => {
    startHandoffs.set('s1', { prompt: 'Fix login', steps: {} });
    expect(renderHook(() => useStartHandoff('s1', 's1', [message], [{ ...part, data }])).result.current).toBeUndefined();
  });

  it('ignores assistant content and parts from another message', () => {
    const handoff = { prompt: 'Fix login', steps: {} };
    startHandoffs.set('s1', handoff);
    expect(renderHook(() => useStartHandoff('s1', 's1', [{ ...message, data: { role: 'assistant' } }], [part])).result.current).toBe(handoff);
    expect(renderHook(() => useStartHandoff('s1', 's1', [message], [{ ...part, messageId: 'other' }])).result.current).toBe(handoff);
  });
});
