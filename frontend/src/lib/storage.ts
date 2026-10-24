// Best-effort localStorage access: blocked storage (private mode, no DOM)
// and quota errors degrade to in-memory behaviour instead of throwing.

/** The stored string, or null when missing or storage is unavailable. */
export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

/** Parses a stored value; `fallback` when missing, unreadable or `parse` throws. */
export function readStored<T>(key: string, fallback: T, parse: (raw: string) => T = JSON.parse): T {
  try {
    const raw = window.localStorage.getItem(key);
    return raw === null ? fallback : parse(raw);
  } catch {
    return fallback;
  }
}

/** Stores `value`, or removes the key for null. Returns whether it succeeded. */
export function writeStorage(key: string, value: string | null): boolean {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
    return true;
  } catch {
    return false;
  }
}
