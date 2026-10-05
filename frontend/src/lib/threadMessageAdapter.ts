import type { ThreadMessageLike } from '@assistant-ui/react';

const NO_ARGS = Object.freeze({});

/**
 * The external-store `convertMessage` for OcmanRuntimeProvider. It must be
 * module-level: assistant-ui drops its per-message conversion cache whenever
 * this function's identity changes, which re-converted every message on each
 * SSE batch.
 *
 * Tool calls get empty `args` because ocman encodes them in `argsText`;
 * without `args`, assistant-ui partial-JSON-parses every argsText.
 */
export function convertThreadMessage(m: ThreadMessageLike): ThreadMessageLike {
  if (typeof m.content === 'string' || !m.content.some((p) => p.type === 'tool-call' && !p.args)) return m;
  return { ...m, content: m.content.map((p) => (p.type === 'tool-call' && !p.args ? { ...p, args: NO_ARGS } : p)) };
}
