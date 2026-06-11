import { useEffect } from 'react';
import {
  LAUNCH_STEP_ORDER,
  useLaunchProgressStore,
  type LaunchStepId,
} from '../lib/launchProgressStore';
import './LaunchProgressOverlay.css';

const STEP_LABELS: Record<LaunchStepId, string> = {
  launch: 'Starting OpenCode in tmux',
  wait: 'Waiting for OpenCode to come up',
  create: 'Creating session',
};

// How long the card lingers after the flow finishes. Success is a
// quick confirmation; errors stay long enough to read but still
// auto-dismiss so a stale card never blocks the corner forever.
const SUCCESS_HIDE_MS = 1800;
const ERROR_HIDE_MS = 10_000;

type StepState = 'done' | 'active' | 'error' | 'pending';

function StepIcon({ state }: { state: StepState }) {
  if (state === 'done') return <span className="oc-launch-icon done" aria-hidden>✓</span>;
  if (state === 'error') return <span className="oc-launch-icon error" aria-hidden>✕</span>;
  if (state === 'active') return <span className="oc-launch-icon spinner" aria-hidden />;
  return <span className="oc-launch-icon pending" aria-hidden />;
}

/**
 * Global fixed-position card that shows step-by-step progress while
 * createSessionWithLaunch boots a fresh opencode instance (tmux
 * launch → wait for opencode → create session). Mounted once in App
 * so the feedback survives palette close and route changes, and every
 * launch surface (command palette, /new, /clear) gets it for free.
 */
export function LaunchProgressOverlay() {
  const phase = useLaunchProgressStore((s) => s.phase);
  const directory = useLaunchProgressStore((s) => s.directory);
  const step = useLaunchProgressStore((s) => s.step);
  const attempt = useLaunchProgressStore((s) => s.attempt);
  const maxAttempts = useLaunchProgressStore((s) => s.maxAttempts);
  const skipLaunch = useLaunchProgressStore((s) => s.skipLaunch);
  const error = useLaunchProgressStore((s) => s.error);
  const dismiss = useLaunchProgressStore((s) => s.dismiss);

  useEffect(() => {
    if (phase !== 'success' && phase !== 'error') return;
    const t = setTimeout(dismiss, phase === 'success' ? SUCCESS_HIDE_MS : ERROR_HIDE_MS);
    return () => clearTimeout(t);
  }, [phase, dismiss]);

  if (phase === 'idle') return null;

  const steps = LAUNCH_STEP_ORDER.filter((id) => !(skipLaunch && id === 'launch'));
  const activeIndex = steps.indexOf(step);
  const projectName = directory.split('/').filter(Boolean).pop() || directory;

  const title =
    phase === 'success'
      ? 'Session ready'
      : phase === 'error'
      ? 'Failed to start session'
      : `Starting session in ${projectName}…`;

  return (
    <div
      className={`oc-launch-overlay ${phase}`}
      data-testid="launch-progress-overlay"
      role="status"
      aria-live="polite"
    >
      <div className="oc-launch-header">
        <span className="oc-launch-title" title={directory}>{title}</span>
        <button
          type="button"
          className="oc-launch-close"
          aria-label="Dismiss launch progress"
          onClick={dismiss}
        >
          ×
        </button>
      </div>
      <ul className="oc-launch-steps">
        {steps.map((id, i) => {
          const state: StepState =
            phase === 'success' || i < activeIndex
              ? 'done'
              : i > activeIndex
              ? 'pending'
              : phase === 'error'
              ? 'error'
              : 'active';
          const showAttempt = id === 'wait' && state === 'active' && attempt > 0;
          return (
            <li key={id} className={`oc-launch-step ${state}`} data-testid={`launch-step-${id}`}>
              <StepIcon state={state} />
              <span className="oc-launch-step-label">
                {STEP_LABELS[id]}
                {showAttempt && (
                  <span className="oc-launch-attempt"> · attempt {attempt}/{maxAttempts}</span>
                )}
              </span>
            </li>
          );
        })}
      </ul>
      {phase === 'error' && error && <div className="oc-launch-error">{error}</div>}
      {phase === 'running' && (
        <div className="oc-launch-hint">
          First start in a project can take 10–20 seconds.
        </div>
      )}
    </div>
  );
}
