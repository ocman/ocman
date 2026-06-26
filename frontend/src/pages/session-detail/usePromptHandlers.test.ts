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
import {
  usePromptHandlers,
  storePendingQuestion,
  loadPendingQuestion,
  clearPendingQuestion,
} from './usePromptHandlers';
import type { PlatformCapabilities } from '../../lib/api';
import type { PendingQuestion } from '../../components/session/QuestionPrompt';

const respondPermission = vi.fn();
const respondQuestion = vi.fn();
const rejectQuestion = vi.fn();

vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (s: Record<string, unknown>) => unknown) =>
    selector({
      respondPermission,
      respondQuestion,
      rejectQuestion,
    }),
}));

vi.mock('../../lib/useToastNotify', () => ({ notifyPromptDismissed: vi.fn() }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

const caps = { respondPermission: true, respondQuestion: true } as unknown as PlatformCapabilities;

const question: PendingQuestion = {
  requestId: 'q-1',
  questions: [{ question: 'pick one', header: 'h', options: [{ label: 'a' }] }],
} as unknown as PendingQuestion;

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

function makeQuestionHook(clearPrompt = vi.fn()) {
  return renderHook(() =>
    usePromptHandlers({
      session: { id: 'sess-1' },
      portAvailable: true,
      caps,
      pendingPermission: null,
      pendingQuestion: question,
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

  it('uses the generic fallback message when a non-Error is thrown', async () => {
    respondPermission.mockRejectedValue('boom');
    const clearPrompt = vi.fn();
    const { result } = makeHook(clearPrompt);
    await act(async () => {
      await result.current.handlePermissionReply('once');
    });
    expect(clearPrompt).not.toHaveBeenCalled();
    expect(result.current.permissionError).toBe('Failed to respond to permission request');
  });

  it('no-ops when there is no pending permission', async () => {
    const clearPrompt = vi.fn();
    const { result } = renderHook(() =>
      usePromptHandlers({
        session: { id: 'sess-1' },
        portAvailable: true,
        caps,
        pendingPermission: null,
        pendingQuestion: null,
        clearPrompt,
      }),
    );
    await act(async () => {
      await result.current.handlePermissionReply('once');
    });
    expect(respondPermission).not.toHaveBeenCalled();
    expect(clearPrompt).not.toHaveBeenCalled();
  });
});

describe('usePromptHandlers — question reply', () => {
  beforeEach(() => {
    respondQuestion.mockReset();
    rejectQuestion.mockReset();
  });

  it('clears the prompt and persisted state on success', async () => {
    respondQuestion.mockResolvedValue(undefined);
    const clearPrompt = vi.fn();
    const { result } = makeQuestionHook(clearPrompt);
    await act(async () => {
      await result.current.handleQuestionReply([['a']]);
    });
    expect(respondQuestion).toHaveBeenCalledWith('sess-1', 'q-1', [['a']]);
    expect(clearPrompt).toHaveBeenCalledWith('question', 'q-1');
    expect(result.current.questionError).toBeNull();
  });

  it('surfaces the error when respondQuestion rejects', async () => {
    respondQuestion.mockRejectedValue(new Error('nope'));
    const clearPrompt = vi.fn();
    const { result } = makeQuestionHook(clearPrompt);
    await act(async () => {
      await result.current.handleQuestionReply([['a']]);
    });
    expect(clearPrompt).not.toHaveBeenCalled();
    expect(result.current.questionError).toBe('nope');
  });

  it('falls back to a generic message for a non-Error rejection', async () => {
    respondQuestion.mockRejectedValue('weird');
    const { result } = makeQuestionHook();
    await act(async () => {
      await result.current.handleQuestionReply([['a']]);
    });
    expect(result.current.questionError).toBe('Failed to submit answer');
  });

  it('no-ops when there is no pending question', async () => {
    const { result } = makeHook(); // pendingQuestion is null here
    await act(async () => {
      await result.current.handleQuestionReply([['a']]);
    });
    expect(respondQuestion).not.toHaveBeenCalled();
  });
});

describe('usePromptHandlers — question reject', () => {
  beforeEach(() => {
    rejectQuestion.mockReset();
  });

  it('clears the prompt on success', async () => {
    rejectQuestion.mockResolvedValue(undefined);
    const clearPrompt = vi.fn();
    const { result } = makeQuestionHook(clearPrompt);
    await act(async () => {
      await result.current.handleQuestionReject();
    });
    expect(rejectQuestion).toHaveBeenCalledWith('sess-1', 'q-1');
    expect(clearPrompt).toHaveBeenCalledWith('question', 'q-1');
  });

  it('swallows rejection errors without clearing the prompt', async () => {
    rejectQuestion.mockRejectedValue(new Error('fail'));
    const clearPrompt = vi.fn();
    const { result } = makeQuestionHook(clearPrompt);
    await act(async () => {
      await result.current.handleQuestionReject();
    });
    expect(clearPrompt).not.toHaveBeenCalled();
  });

  it('no-ops when there is no pending question', async () => {
    const { result } = makeHook();
    await act(async () => {
      await result.current.handleQuestionReject();
    });
    expect(rejectQuestion).not.toHaveBeenCalled();
  });
});

describe('pending-question persistence helpers', () => {
  beforeEach(() => {
    sessionStorage.clear();
  });

  it('stores, loads, and clears a pending question round-trip', () => {
    storePendingQuestion('sess-1', question);
    expect(loadPendingQuestion('sess-1')).toEqual(question);
    clearPendingQuestion('sess-1');
    expect(loadPendingQuestion('sess-1')).toBeNull();
  });

  it('returns null for a missing question', () => {
    expect(loadPendingQuestion('absent')).toBeNull();
  });

  it('returns null for a corrupt/invalid stored value', () => {
    sessionStorage.setItem('ocman:pendingQuestion:sess-1', '{not json');
    expect(loadPendingQuestion('sess-1')).toBeNull();
    sessionStorage.setItem('ocman:pendingQuestion:sess-1', JSON.stringify({ requestId: 'x', questions: [] }));
    expect(loadPendingQuestion('sess-1')).toBeNull();
  });
});
