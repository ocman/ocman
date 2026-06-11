import { describe, it, expect, vi, beforeEach } from 'vitest';

// Spy on the collaborators before importing the module under test so
// the module captures the mocked references.
const notifyPromptDismissed = vi.fn();
const recheckNotifyData = vi.fn();

vi.mock('./useToastNotify', () => ({
  notifyPromptDismissed: (...args: unknown[]) => notifyPromptDismissed(...args),
}));
vi.mock('./useNotifyData', () => ({
  recheckNotifyData: (...args: unknown[]) => recheckNotifyData(...args),
}));

import { __handleResolvedForTests, __handleSurfaceForTests } from './useGlobalEvents';

describe('useGlobalEvents resolved handler', () => {
  beforeEach(() => {
    notifyPromptDismissed.mockClear();
    recheckNotifyData.mockClear();
  });

  it('dismisses the toast and rechecks notify on a valid payload', () => {
    __handleResolvedForTests(
      JSON.stringify({ sessionID: 'sess-1', permissionId: 'p1', reason: 'auto-approved' }),
    );
    expect(notifyPromptDismissed).toHaveBeenCalledWith('sess-1');
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
  });

  it('dismisses the toast for a resolved question payload', () => {
    __handleResolvedForTests(
      JSON.stringify({ sessionID: 'sess-q', requestId: 'r1', reason: 'rejected' }),
    );
    expect(notifyPromptDismissed).toHaveBeenCalledWith('sess-q');
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
  });

  it('ignores malformed JSON', () => {
    __handleResolvedForTests('not json');
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });

  it('ignores payloads without a sessionID', () => {
    __handleResolvedForTests(JSON.stringify({ permissionId: 'p1' }));
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });
});

describe('useGlobalEvents surface handler', () => {
  beforeEach(() => {
    notifyPromptDismissed.mockClear();
    recheckNotifyData.mockClear();
  });

  it('rechecks notify without dismissing a toast (flagged/idle)', () => {
    __handleSurfaceForTests(JSON.stringify({ sessionID: 'sess-2', reason: 'flagged' }));
    expect(recheckNotifyData).toHaveBeenCalledTimes(1);
    expect(notifyPromptDismissed).not.toHaveBeenCalled();
  });

  it('ignores malformed or session-less payloads', () => {
    __handleSurfaceForTests('nope');
    __handleSurfaceForTests(JSON.stringify({ reason: 'flagged' }));
    expect(recheckNotifyData).not.toHaveBeenCalled();
  });
});
