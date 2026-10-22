import { describe, expect, it, vi } from 'vitest';
import { seedView } from '../../test/oceFixtures';
import { hello, oceEnvelope } from '../../test/oce';
import { createConversationStream } from './conversationStream';
import { reduceLiveSessionView } from './liveSessionView';
import type { LiveOverlay } from './events';

describe('review 414 recovery', () => {
  it('keeps REST completion metadata when recovering a pre-fence running tool', () => {
    const running = reduceLiveSessionView(seedView('s'), { type: 'oceBatch', envelopes: [oceEnvelope('s', {
      type: 'part.upserted', final: false, part: { id: 'tool', messageId: 'm', sessionId: 's', data: { type: 'tool', state: { status: 'running', output: 'partial' } } },
    })] });
    const rest = { ...seedView('s'), parts: [{ id: 'tool', messageId: 'm', sessionId: 's', data: { type: 'tool', state: { status: 'completed', output: 'partial done' } } }] };
    const recovered = reduceLiveSessionView(running, { type: 'recover', view: rest, sinceFence: [] });
    expect(recovered.parts[0].data).toMatchObject({ state: { status: 'completed', output: 'partial done' } });
  });

  it('keeps a newer REST prefix but applies authoritative final replacements', () => {
    const base = seedView('s');
    const current = reduceLiveSessionView(base, { type: 'oceBatch', envelopes: [oceEnvelope('s', {
      type: 'part.upserted', final: false, part: { id: 'p', messageId: 'm', sessionId: 's', data: { type: 'text', text: 'ab' } },
    })] });
    const rest = { ...base, parts: [{ id: 'p', messageId: 'm', sessionId: 's', data: { type: 'text', text: 'abc' } }] };
    const recovered = reduceLiveSessionView(current, { type: 'recover', view: rest, sinceFence: [] });
    expect(recovered.parts[0].data).toMatchObject({ text: 'abc' });
    const final = reduceLiveSessionView(current, { type: 'oceBatch', envelopes: [oceEnvelope('s', {
      type: 'part.upserted', final: true, part: { id: 'p', messageId: 'm', sessionId: 's', data: { type: 'tool', state: { status: 'error' } } },
    })] });
    const finalRecovered = reduceLiveSessionView(final, { type: 'recover', view: rest, sinceFence: [] });
    expect(finalRecovered.parts[0].data).toEqual({ type: 'tool', state: { status: 'error' } });
  });
  it('keeps populated hello state when REST returns without intervening events', () => {
    const overlay: LiveOverlay = {
      status: { type: 'session.status', status: 'waiting' }, liveConnection: true,
      prompts: [{ id: 'permission', kind: 'permission', sessionId: 's', askedAt: 1, permission: { permission: 'bash', patterns: ['ls'], always: [] } }],
      judge: [{ type: 'prompt.judge', promptId: 'permission', phase: 'checking' }],
      background: { type: 'background.changed', count: 1, tasks: [{ id: 'task', description: 'shell' }] },
      inflight: { messages: [{ id: 'live', sessionId: 's', timeCreated: 2, data: { role: 'assistant' } }], parts: [{ id: 'live-part', messageId: 'live', sessionId: 's', data: { type: 'text', text: 'in progress' } }] },
    };
    const view = reduceLiveSessionView(seedView('s'), { type: 'oceBatch', envelopes: [oceEnvelope('s', hello(false, overlay))] });
    const recovered = reduceLiveSessionView(view, { type: 'recover', view: seedView('s'), sinceFence: [] });
    expect(recovered.pendingPermission?.permissionId).toBe('permission');
    expect(recovered.checkingPermissionId).toBe('permission');
    expect(recovered.backgroundTaskCount).toBe(1);
    expect(recovered.parts.some((p) => p.id === 'live-part')).toBe(true);
    expect(recovered.session?.status).toBe('waiting');
  });

  it('recovers on the first hello to close the REST/subscription gap', () => {
    const recover = vi.fn(async () => true);
    const stream = createConversationStream({ sessionId: 's', dispatch: vi.fn(), recover, reconcile: vi.fn(), dirty: vi.fn() });
    let listener!: (message: MessageEvent) => void;
    stream.attach({ addEventListener: (_: string, fn: typeof listener) => { listener = fn; } } as unknown as EventSource);
    listener({ data: JSON.stringify(oceEnvelope('s', hello(false))) } as MessageEvent);
    expect(recover).toHaveBeenCalledOnce();
    stream.dispose();
  });
});
