import { create } from 'zustand';

/**
 * Global progress state for the "open a closed project" slow path. When
 * ocman has to spawn a fresh opencode instance in tmux, the whole dance —
 * tmux launch, opencode boot, port bind, health probe — can take 10-20
 * seconds. This store tracks which step is running so the
 * LaunchProgressCard can show the user what's happening, regardless
 * of which surface kicked the launch off (a new conversation's prepare,
 * the composer's launch button).
 */

export type LaunchStepId = 'launch' | 'wait';

export type LaunchPhase = 'idle' | 'running' | 'success' | 'error';

/** Ordered step list; the card renders steps in this order. */
export const LAUNCH_STEP_ORDER: readonly LaunchStepId[] = ['launch', 'wait'];

/**
 * Progress begins before the first request so the user is informed the
 * moment the process is invoked. When opencode is already running that
 * request returns almost instantly; a flow that finishes within this
 * window drops straight back to idle instead of flashing "ready". The
 * progress card itself appears immediately.
 */
export const LAUNCH_QUICK_MS = 400;

type LaunchProgressStore = {
  phase: LaunchPhase;
  /** Project directory being opened. */
  directory: string;
  /** Machine that owns `directory` ('local' for the hub). */
  remoteId: string;
  /** Date.now() at begin(); drives the quick-success suppression. */
  startedAt: number;
  /** Active step while phase === 'running' (or the step that failed). */
  step: LaunchStepId;
  /** 1-based retry attempt for the wait loop; 0 = not started. */
  attempt: number;
  maxAttempts: number;
  /**
   * True when opencode was launched externally (worktree flow) so the
   * 'launch' step should not be rendered.
   */
  skipLaunch: boolean;
  error: string | null;

  begin: (directory: string, opts?: LaunchBeginOpts) => void;
  setStep: (step: LaunchStepId) => void;
  setAttempt: (attempt: number, maxAttempts: number) => void;
  succeed: () => void;
  fail: (message: string) => void;
  dismiss: () => void;
};

export interface LaunchBeginOpts {
  skipLaunch?: boolean;
  /** Owner of the directory; omitted = 'local'. */
  remoteId?: string;
}

export const useLaunchProgressStore = create<LaunchProgressStore>((set) => ({
  phase: 'idle',
  directory: '',
  remoteId: 'local',
  startedAt: 0,
  step: 'launch',
  attempt: 0,
  maxAttempts: 0,
  skipLaunch: false,
  error: null,

  begin: (directory, opts) =>
    set({
      phase: 'running',
      directory,
      remoteId: opts?.remoteId || 'local',
      startedAt: Date.now(),
      step: opts?.skipLaunch ? 'wait' : 'launch',
      attempt: 0,
      maxAttempts: 0,
      skipLaunch: !!opts?.skipLaunch,
      error: null,
    }),
  setStep: (step) =>
    set((s) => (s.phase === 'running' ? { step } : {})),
  setAttempt: (attempt, maxAttempts) =>
    set((s) => (s.phase === 'running' ? { attempt, maxAttempts } : {})),
  succeed: () =>
    set((s) => {
      if (s.phase !== 'running') return {};
      // Fast path (opencode already up): nothing to confirm, go quiet.
      if (Date.now() - s.startedAt < LAUNCH_QUICK_MS) return { phase: 'idle' };
      return { phase: 'success' };
    }),
  fail: (message) =>
    set((s) => (s.phase === 'running' ? { phase: 'error', error: message } : {})),
  dismiss: () =>
    set({ phase: 'idle', error: null, attempt: 0, maxAttempts: 0 }),
}));

/**
 * Imperative reporter interface used by createSessionWithLaunch so the
 * lib helper doesn't need React. The default reporter forwards to the
 * global store; callers with their own progress UI (WorktreeFormModal)
 * can opt out via `reportProgress: false`.
 */
export interface LaunchProgressReporter {
  begin(directory: string, opts?: LaunchBeginOpts): void;
  step(step: LaunchStepId): void;
  attempt(attempt: number, maxAttempts: number): void;
  succeed(): void;
  fail(message: string): void;
}

export const launchProgressReporter: LaunchProgressReporter = {
  begin: (directory, opts) => useLaunchProgressStore.getState().begin(directory, opts),
  step: (step) => useLaunchProgressStore.getState().setStep(step),
  attempt: (attempt, maxAttempts) => useLaunchProgressStore.getState().setAttempt(attempt, maxAttempts),
  succeed: () => useLaunchProgressStore.getState().succeed(),
  fail: (message) => useLaunchProgressStore.getState().fail(message),
};

export const noopLaunchProgressReporter: LaunchProgressReporter = {
  begin: () => {},
  step: () => {},
  attempt: () => {},
  succeed: () => {},
  fail: () => {},
};
