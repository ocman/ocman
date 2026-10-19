import type { OceEnvelope, PartDelta } from './events';

function isDelta(env: OceEnvelope): env is OceEnvelope<PartDelta> {
  return env.event.type === 'part.delta';
}

/**
 * Merge consecutive part.delta envelopes for the same part and field whose
 * offsets are contiguous, so a burst becomes one append. The merged envelope
 * keeps the last envelope's cursor fields.
 */
export function coalesceDeltas(batch: readonly OceEnvelope[]): OceEnvelope[] {
  const out: OceEnvelope[] = [];
  for (const env of batch) {
    const prev = out[out.length - 1];
    if (prev && isDelta(prev) && isDelta(env) && prev.sessionId === env.sessionId) {
      const a = prev.event;
      const b = env.event;
      if (a.partId === b.partId && a.messageId === b.messageId && a.field === b.field
        && b.offset === a.offset + a.text.length) {
        out[out.length - 1] = { ...env, event: { ...a, text: a.text + b.text, partType: a.partType ?? b.partType } };
        continue;
      }
    }
    out.push(env);
  }
  return out;
}
