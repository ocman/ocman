import { expect, it } from 'vitest';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { BroadcastChannel as NativeBroadcastChannel } from 'node:worker_threads';

it('isolates application channels from other test environments using the same name', () => {
  const local = new BroadcastChannel('ocman.drafts.v1');
  const external = new NativeBroadcastChannel('ocman.drafts.v1');
  try {
    expect(local.name).not.toBe(external.name);
  } finally {
    local.close();
    external.close();
  }
});

it('preserves communication between simulated tabs in the same test environment', async () => {
  const first = new BroadcastChannel('ocman.drafts.v1');
  const second = new BroadcastChannel('ocman.drafts.v1');
  try {
    const received = new Promise((resolve) => { second.onmessage = (event) => resolve(event.data); });
    first.postMessage({ texts: ['shared-draft'] });
    await expect(received).resolves.toEqual({ texts: ['shared-draft'] });
  } finally {
    first.close();
    second.close();
  }
});
