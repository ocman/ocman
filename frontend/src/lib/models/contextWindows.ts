/**
 * Static map of model id → context-window size (in tokens) for the
 * models we care about in the composer's token-usage popover. The
 * list is intentionally hand-curated; new entries land here when a
 * model becomes prominent enough to render in the picker.
 *
 * Entries should match the model id reported by the platform exactly,
 * excluding any provider prefix.
 */
export const MODEL_CONTEXT_WINDOWS: Record<string, number> = {
  'gpt-4o': 128_000,
  'gpt-4o-mini': 128_000,
  'gpt-4-turbo': 128_000,
  'gpt-4': 8_192,
  'gpt-4.1': 1_047_576,
  'gpt-4.1-mini': 1_047_576,
  'gpt-4.1-nano': 1_047_576,
  'o1': 200_000,
  'o1-mini': 128_000,
  'o1-pro': 200_000,
  'o3': 200_000,
  'o3-mini': 200_000,
  'o3-pro': 200_000,
  'o4-mini': 200_000,
  'claude-sonnet-4-20250514': 200_000,
  'claude-opus-4-20250514': 200_000,
  'claude-opus-4-6-20250616': 200_000,
  'claude-3-7-sonnet-20250219': 200_000,
  'claude-3-5-sonnet-20241022': 200_000,
  'claude-3-5-sonnet-20240620': 200_000,
  'claude-3-5-haiku-20241022': 200_000,
  'claude-3-opus-20240229': 200_000,
  'claude-3-haiku-20240307': 200_000,
  'claude-sonnet-4': 200_000,
  'claude-opus-4-6': 200_000,
  'claude-opus-4': 200_000,
  'gemini-2.5-pro': 1_048_576,
  'gemini-2.5-flash': 1_048_576,
  'gemini-2.0-flash': 1_048_576,
  'gemini-1.5-pro': 2_097_152,
  'gemini-1.5-flash': 1_048_576,
  'grok-3': 131_072,
  'grok-3-mini': 131_072,
  'deepseek-chat': 64_000,
  'deepseek-reasoner': 64_000,
  'mistral-large-latest': 128_000,
  'codestral-latest': 256_000,
};

/**
 * Resolve the context-window size for a given model id.
 *
 * The final segment of a provider-qualified id must match a known model.
 * A similar id may have a different limit, so don't guess from a substring.
 */
export function getContextWindow(modelId: string | undefined): number | null {
  if (!modelId) return null;
  const key = modelId.slice(modelId.lastIndexOf('/') + 1).toLowerCase();
  return Object.hasOwn(MODEL_CONTEXT_WINDOWS, key) ? MODEL_CONTEXT_WINDOWS[key] : null;
}

/**
 * Format a token count as a compact human-readable string. `12_345`
 * becomes `12.3K`; `1_234_567` becomes `1.2M`; values under 1000 are
 * rendered as a plain integer. Used in the composer's
 * tokens-used display.
 */
export function formatTokenCount(n: number): string {
  if (n >= 1e6) return (n / 1e6).toFixed(1) + 'M';
  if (n >= 1e3) return (n / 1e3).toFixed(1) + 'K';
  return String(n);
}
