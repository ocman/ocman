import { beforeEach, describe, expect, it, vi } from 'vitest';

const debugLog = vi.fn();
vi.mock('./api', () => ({ api: { debugLog: (...args: unknown[]) => debugLog(...args) } }));

import { remoteLog } from './remoteLog';
import { BackendUnavailableError } from './api.requests';

describe('remoteLog.error', () => {
  beforeEach(() => {
    debugLog.mockClear();
    vi.spyOn(console, 'error').mockImplementation(() => {});
    vi.spyOn(console, 'warn').mockImplementation(() => {});
    vi.spyOn(console, 'debug').mockImplementation(() => {});
  });

  it('does not report an aborted request', () => {
    remoteLog.error('Failed to refresh active session', new DOMException('Fetch is aborted', 'AbortError'));
    remoteLog.error('unhandledrejection', { reason: new DOMException('signal is aborted', 'AbortError') });
    expect(debugLog).not.toHaveBeenCalled();
    expect(console.error).not.toHaveBeenCalled();
  });

  it('downgrades an unreachable backend to a warning', () => {
    remoteLog.error('Failed to mark session seen', new BackendUnavailableError());
    expect(debugLog).toHaveBeenCalledWith('warn', 'Failed to mark session seen', expect.anything());
  });

  it('still reports genuine failures as errors', () => {
    remoteLog.error('Failed to archive session', new Error('boom'));
    expect(debugLog).toHaveBeenCalledWith('error', 'Failed to archive session', expect.objectContaining({ message: 'boom' }));
  });
});
