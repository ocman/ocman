import { useCapabilities } from '../../lib/useCapabilities';
import type { StartStep, StartStepState } from '../../lib/useGlobalEvents';
import '../../components/LaunchProgressCard.css';
import './LaunchSetup.css';

export type StartSteps = Partial<Record<StartStep, StartStepState>>;
const ORDER: StartStep[] = ['clone', 'platform', 'worktree', 'session', 'prompt'];
const LABELS: Record<StartStep, Record<StartStepState, string>> = {
  clone: { active: 'Cloning project', done: 'Project cloned', error: 'Project clone failed' },
  platform: { active: 'Launching platform', done: 'Platform ready', error: 'Platform failed to launch' },
  worktree: { active: 'Setting up worktree', done: 'Worktree ready', error: 'Worktree setup failed' },
  session: { active: 'Creating session', done: 'Session created', error: 'Creating session failed' },
  prompt: { active: 'Sending prompt', done: 'Prompt sent', error: 'Sending prompt failed' },
};

function Icon({ state }: { state: StartStepState }) {
  if (state === 'done') return <span className="oc-launch-icon done" aria-hidden>✓</span>;
  if (state === 'error') return <span className="oc-launch-icon error" aria-hidden>✕</span>;
  return <span className="oc-launch-icon spinner" aria-hidden />;
}

export function LaunchSetup({ steps, platform, complete }: { steps: StartSteps; platform?: string; complete?: boolean }) {
  const name = useCapabilities()?.platforms.find((entry) => entry.id === platform)?.displayName;
  const labels = { ...LABELS, platform: name
    ? { active: `Launching ${name}`, done: `${name} ready`, error: `${name} failed to launch` }
    : LABELS.platform };
  const shown = ORDER.filter((id) => steps[id]);
  const done = complete ?? (steps.prompt === 'done' && shown.every((id) => steps[id] === 'done'));
  return <details key={done ? 'done' : 'running'} className="oc-launch-setup" open={!done}>
    <summary>Setup ocman session</summary>
    <ul className="oc-launch-steps oc-start-steps" role="status" aria-live="polite" data-testid="start-progress">
      {shown.length === 0 && <li className="oc-launch-step"><Icon state="active" /><span>Starting session</span></li>}
      {shown.map((id) => <li key={id} className={`oc-launch-step ${steps[id]}`} data-testid={`start-step-${id}`}>
        <Icon state={steps[id]!} /><span className="oc-launch-step-label">{labels[id][steps[id]!]}</span>
      </li>)}
    </ul>
  </details>;
}
