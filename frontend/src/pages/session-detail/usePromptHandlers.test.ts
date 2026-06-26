// @vitest-environment jsdom
//
// Tests for usePromptHandlers' permission reply path, focused on the
// "stale prompt" case: a prompt that outlived its server-side
// permission request (OpenCode timed it out / restarted / it was
// answered elsewhere) and whose SSE clearing event never arrived. The
// reply POST then fails with PermissionNotFoundError and the prompt
// must be CLEARED, not left on screen with the raw error.

import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';
import { usePromptHandlers } from './usePromptHandlers';
import type { PlatformCapabilities } from '../../lib/api';

const respondPermission = vi.fn();

vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({
      respondPermission,
      respondQuestion: vi.fn(),
      rejectQuestion: vi.fn(),
    }),
}));

vi.mock('../../lib/useToastNotify', () => ({ notifyPromptDismissed: vi.fn() }));

const caps = { respondPermission: true, respondQuestion: true } as unknown as PlatformCapabilities;

function makeHook(clearPrompt = vi.fn()) {
  return renderHook(() =>
    usePromptHandlers({
      session: { id: 'sess-1' },
      portAvailable: true,
      caps,
      pendingPermission: { permissionId: 'perm-1', permission: 'Run shell', patterns: [], sessionId: '', askedAt: 0 },
      pendingQuestion: null,
      clearPrompt,
    }),
  );
}

describe('usePromptHandlers — permission reply', () => {
  beforeEach(() => {
    respondPermission.mockReset();
  });

  it('clears the prompt on success', async () => {
    respondPermission.mockResolvedValue(undefined);
    const clearPrompt = vi.fn();
    const { result } = makeHook(clearPrompt);
    await act(async () => {
      await result.current.handlePermissionReply('once');
    });
    expect(clearPrompt).toHaveBeenCalledWith('permission', 'perm-1');
    expect(result.current.permissionError).toBeNull();
  });

  it('clears the stale prompt instead of showing the error on PermissionNotFoundError', async () => {
    respondPermission.mockRejectedValue(
      new Error('Permission request not found: perm-1\n{"_tag":"PermissionNotFoundError"}'),
    );
    const clearPrompt = vi.fn();
    const { result } = makeHook(clearPrompt);
    await act(async () => {
      await result.current.handlePermissionReply('once');
    });
    expect(clearPrompt).toHaveBeenCalledWith('permission', 'perm-1');
    expect(result.current.permissionError).toBeNull();
  });

  it('surfaces other errors and keeps the prompt up', async () => {
    respondPermission.mockRejectedValue(new Error('upstream HTTP 502'));
    const clearPrompt = vi.fn();
    const { result } = makeHook(clearPrompt);
    await act(async () => {
      await result.current.handlePermissionReply('once');
    });
    expect(clearPrompt).not.toHaveBeenCalled();
    expect(result.current.permissionError).toBe('upstream HTTP 502');
  });
});
