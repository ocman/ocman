// The first prompt of a new conversation, shown as its first message while
// the session starts, with the server's steps underneath (no card chrome).
// Steps that run in parallel (the platform and the worktree) spin together.

import type { StartStep, StartStepState } from '../../lib/useGlobalEvents';
import type { AttachedImage } from '../../components/assistant/useComposerAttachments';
import { ImageDisplay } from '../../components/AssistantThread';
import { useCapabilities } from '../../lib/useCapabilities';
import '../../components/LaunchProgressCard.css';

export type StartSteps = Partial<Record<StartStep, StartStepState>>;

const ORDER: StartStep[] = ['opencode', 'worktree', 'session', 'prompt'];
const LABELS: Record<StartStep, Record<StartStepState, string>> = {
  opencode: { active: 'Launching platform', done: 'Platform ready', error: 'Platform failed to launch' },
  worktree: { active: 'Setting up worktree', done: 'Worktree ready', error: 'Worktree setup failed' },
  session: { active: 'Creating session', done: 'Session created', error: 'Creating session failed' },
  prompt: { active: 'Sending prompt', done: 'Prompt sent', error: 'Sending prompt failed' },
};

function Icon({ state }: { state: StartStepState }) {
  if (state === 'done') return <span className="oc-launch-icon done" aria-hidden>✓</span>;
  if (state === 'error') return <span className="oc-launch-icon error" aria-hidden>✕</span>;
  return <span className="oc-launch-icon spinner" aria-hidden />;
}

export function StartProgress({ prompt, images, steps, platform }: { prompt: string; images?: AttachedImage[]; steps: StartSteps; platform?: string }) {
  const name = useCapabilities()?.platforms.find((entry) => entry.id === platform)?.displayName;
  const labels = { ...LABELS, opencode: name
    ? { active: `Launching ${name}`, done: `${name} ready`, error: `${name} failed to launch` }
    : LABELS.opencode };
  const shown = ORDER.filter((id) => steps[id]);
  return (
    <>
      <div className="oc-msg oc-msg-user" data-testid="pending-prompt">
        <div className="oc-msg-body oc-md">{prompt}
          {images?.map((image, index) => <ImageDisplay key={index} image={image.url} />)}
        </div>
      </div>
      <ul className="oc-launch-steps oc-start-steps" role="status" aria-live="polite" data-testid="start-progress">
        {shown.length === 0 && (
          // Before the first step arrives (or without the event stream).
          <li className="oc-launch-step"><Icon state="active" /><span>Starting session</span></li>
        )}
        {shown.map((id) => {
          const state = steps[id]!;
          return (
            <li key={id} className={`oc-launch-step ${state}`} data-testid={`start-step-${id}`}>
              <Icon state={state} />
              <span className="oc-launch-step-label">{labels[id][state]}</span>
            </li>
          );
        })}
      </ul>
    </>
  );
}
