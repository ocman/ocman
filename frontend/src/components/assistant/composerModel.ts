import type { SessionModelEntry } from '../../lib/api';
import { qualifyModelRef } from '../../lib/modelRef';

export interface ModelDescription {
  /** Human-readable label for the model button ('' when no model). */
  label: string;
  /** Selected model's provider is reported disconnected on this session's host. */
  unavailable: boolean;
  /** Reasoning variants the model exposes. */
  reasoningOptions: string[];
}

/** Derive the composer's model-button facts from the rich entry list. */
export function describeModel(effectiveModel: string, modelEntries: SessionModelEntry[] | undefined): ModelDescription {
  if (!effectiveModel) return { label: '', unavailable: false, reasoningOptions: [] };
  // A new conversation lists several platforms' models and selects a
  // platform-qualified reference; a session's own picks are plain.
  const ref = (e: SessionModelEntry) => `${e.provider}/${e.model}`;
  const match = modelEntries?.find((e) => e.provider && qualifyModelRef(e.platform, ref(e)) === effectiveModel)
    ?? modelEntries?.find((e) => e.provider && ref(e) === effectiveModel);
  const slash = effectiveModel.indexOf('/');
  return {
    // Prefer the human-readable `modelName` (e.g. "Claude Opus 4.7"),
    // falling back to the bare model id from the "provider/model" string.
    label: match ? (match.modelName || match.model) : slash > 0 ? effectiveModel.slice(slash + 1) : effectiveModel,
    // Only trust the rich entries; the string fallback has no
    // availability data so we stay silent there.
    unavailable: match?.isAvailable === false,
    reasoningOptions: match?.reasoning ?? [],
  };
}
