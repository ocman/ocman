import { create } from 'zustand';

/**
 * Global progress state for the "create a session in a closed project"
 * slow path (createSessionWithLaunch). When ocman has to spawn a fresh
 * opencode instance in tmux, the whole dance — tmux launch, opencode
 * boot, port bind, lsof cache expiry, session create — can take 10-20
 * seconds. This store tracks which step is running so the
 * LaunchProgressOverlay can show the user what's happening, regardless
 * of which surface kicked the launch off (command palette, /new,
 * /clear, worktree flow).
 */

export type LaunchStepId = 'launch' | 'wait' | 'create';

export type LaunchPhase = 'idle' | 'running' | 'success' | 'error';

/** Ordered step list; the overlay renders steps in this order. */
export const LAUNCH_STEP_ORDER: readonly LaunchStepId[] = ['launch', 'wait', 'create'];

type LaunchProgressStore = {
  phase: LaunchPhase;
  /** Directory the session is being created in. */
  directory: string;
  /** Active step while phase === 'running' (or the step that failed). */
  step: LaunchStepId;
  /** 1-based retry attempt for the wait/create loop; 0 = not started. */
  attempt: number;
  maxAttempts: number;
  /**
   * True when opencode was launched externally (worktree flow) so the
   * 'launch' step should not be rendered.
   */
  skipLaunch: boolean;
  error: string | null;

  begin: (directory: string, opts?: { skipLaunch?: boolean }) => void;
  setStep: (step: LaunchStepId) => void;
  setAttempt: (attempt: number, maxAttempts: number) => void;
  succeed: () => void;
  fail: (message: string) => void;
  dismiss: () => void;
};

export const useLaunchProgressStore = create<LaunchProgressStore>((set) => ({
  phase: 'idle',
  directory: '',
  step: 'launch',
  attempt: 0,
  maxAttempts: 0,
  skipLaunch: false,
  error: null,

  begin: (directory, opts) =>
    set({
      phase: 'running',
      directory,
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
    set((s) => (s.phase === 'running' ? { phase: 'success' } : {})),
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
  begin(directory: string, opts?: { skipLaunch?: boolean }): void;
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
