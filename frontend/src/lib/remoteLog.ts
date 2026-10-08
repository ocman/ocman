// Remote logging helper.
//
// Mirrors console output to the backend via POST /api/debug/log. Designed
// for environments where the browser devtools aren't reachable (iPad,
// embedded webviews, etc.). Logs still appear in the console as usual —
// the remote copy is additive.
//
// Also installs one-time global handlers for `window.onerror` and
// `unhandledrejection` so React render errors, fetch failures, etc. show
// up in the server log without any extra call sites.

import { api } from './api';

type Level = 'debug' | 'info' | 'warn' | 'error';

// Serialise arbitrary values into something JSON.stringify can handle.
// Errors get flattened to { name, message, stack } so we keep the stack.
function toSerializable(value: unknown): unknown {
  if (value instanceof Error) {
    return { name: value.name, message: value.message, stack: value.stack };
  }
  if (Array.isArray(value)) return value.map(toSerializable);
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {};
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = toSerializable(v);
    }
    return out;
  }
  return value;
}

function send(level: Level, message: string, data?: unknown) {
  const serialisable = data === undefined ? undefined : toSerializable(data);
  // Fire-and-forget; api.debugLog already swallows fetch errors.
  void api.debugLog(level, message, serialisable);
}

// Cancellation (an aborted fetch, Safari dropping requests when the tab is
// backgrounded) is control flow, and an unreachable backend during a
// restart is expected; neither should be reported as an error.
function errorName(data: unknown): string | undefined {
  const value = data && typeof data === 'object' && 'reason' in data ? (data as { reason: unknown }).reason : data;
  return value && typeof value === 'object' && 'name' in value ? String((value as { name: unknown }).name) : undefined;
}

export const remoteLog = {
  debug(message: string, data?: unknown) {
    console.debug(message, data);
    send('debug', message, data);
  },
  info(message: string, data?: unknown) {
    console.info(message, data);
    send('info', message, data);
  },
  warn(message: string, data?: unknown) {
    console.warn(message, data);
    send('warn', message, data);
  },
  error(message: string, data?: unknown) {
    const name = errorName(data);
    if (name === 'AbortError') {
      console.debug(message, data);
      return;
    }
    if (name === 'BackendUnavailableError') {
      remoteLog.warn(message, data);
      return;
    }
    console.error(message, data);
    send('error', message, data);
  },
};

let installed = false;

// Install global error handlers. Safe to call more than once — subsequent
// calls are ignored. Call from main.tsx so it runs before the app boots.
export function installRemoteLogHandlers(): void {
  if (installed) return;
  installed = true;

  window.addEventListener('error', (event) => {
    remoteLog.error('window.onerror', {
      message: event.message,
      filename: event.filename,
      lineno: event.lineno,
      colno: event.colno,
      error: event.error,
    });
  });

  window.addEventListener('unhandledrejection', (event) => {
    remoteLog.error('unhandledrejection', {
      reason: event.reason,
    });
  });
}
