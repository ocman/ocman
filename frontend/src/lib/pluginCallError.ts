const categories = new Set([
  'invalid_argument', 'permission_denied', 'not_found', 'conflict',
  'unavailable', 'deadline_exceeded', 'cancelled', 'internal',
]);

/** HTTP failure of a plugin UI call. Never includes diagnostic response text. */
export class PluginCallError extends Error {
  readonly status: number;
  readonly category: string | null;
  readonly retryAfterMs: number | null;

  constructor(status: number, category: string | null, retryAfterMs: number | null) {
    super(`Plugin request failed (HTTP ${status})`);
    this.name = 'PluginCallError';
    this.status = status;
    this.category = category;
    this.retryAfterMs = retryAfterMs;
  }
}

export async function pluginCallResponseError(response: Response): Promise<PluginCallError> {
  let category: string | null = null;
  try {
    const body = await response.json() as { error?: { category?: unknown } } | null;
    const value = body?.error?.category;
    if (typeof value === 'string' && categories.has(value)) category = value;
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    // Proxies can return non-JSON errors. Preserve status, never their body.
  }
  return new PluginCallError(response.status, category, retryAfterMs(response.headers.get('Retry-After')));
}

function retryAfterMs(value: string | null): number | null {
  if (value === null) return null;
  const trimmed = value.trim();
  if (/^\d+$/.test(trimmed)) {
    const ms = Number(trimmed) * 1000;
    return Number.isSafeInteger(ms) ? ms : null;
  }
  // Require an HTTP-date shape rather than accepting Date.parse's numeric dates.
  if (!/^[A-Za-z]{3}, /.test(trimmed)) return null;
  const date = Date.parse(trimmed);
  return Number.isFinite(date) ? Math.max(0, date - Date.now()) : null;
}
